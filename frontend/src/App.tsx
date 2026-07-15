import { useState, useEffect, useRef } from 'react';

// Declarations to satisfy TypeScript compiler for CDN-loaded libraries
declare global {
  interface Window {
    Hls: any;
    mpegts: any;
  }
}

interface Playlist {
  id: string;
  name: string;
  url_path: string;
  type: string;
  created_at: string;
}

interface ChannelGroup {
  id: string;
  playlist_id: string;
  name: string;
}

interface Channel {
  id: string;
  group_id: string;
  name: string;
  stream_url: string;
  logo_url: string;
  channel_number: number;
}

interface ProgramDetails {
  title: string;
  description: string;
  start_time: string;
  end_time: string;
}

interface ChannelEPG {
  current?: ProgramDetails;
  next?: ProgramDetails;
}

function App() {
  // Playlists, Groups, Channels
  const [playlists, setPlaylists] = useState<Playlist[]>([]);
  const [groups, setGroups] = useState<ChannelGroup[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [epgData, setEpgData] = useState<Record<string, ChannelEPG>>({});

  // Filtering & Selection
  const [selectedPlaylist, setSelectedPlaylist] = useState<string>('');
  const [selectedGroup, setSelectedGroup] = useState<string>('');
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [selectedChannel, setSelectedChannel] = useState<Channel | null>(null);

  // Loading & State flags
  const [loading, setLoading] = useState(false);
  const [showManager, setShowManager] = useState(false);
  const [surfToast, setSurfToast] = useState<string>('');
  const [playerError, setPlayerError] = useState<string | null>(null);
  const [libsLoaded, setLibsLoaded] = useState(false);

  // Clear player error when active channel changes
  useEffect(() => {
    setPlayerError(null);
  }, [selectedChannel]);

  // Form states for new sources
  const [newPlaylistName, setNewPlaylistName] = useState('');
  const [newPlaylistURL, setNewPlaylistURL] = useState('');
  const [newPlaylistType, setNewPlaylistType] = useState('M3U');
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newEpgURL, setNewEpgURL] = useState('');
  const [syncingSources, setSyncingSources] = useState<Record<string, boolean>>({});

  // Refs for video player elements & library cleaning
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const hlsPlayerRef = useRef<any>(null);
  const tsPlayerRef = useRef<any>(null);

  const apiBase = window.location.port === '5173' ? 'http://localhost:8080' : '';

  // 1. Dynamic script loader for HLS and MPEG-TS playback
  useEffect(() => {
    const loadScript = (src: string): Promise<void> => {
      return new Promise((resolve, reject) => {
        if (document.querySelector(`script[src="${src}"]`)) {
          resolve();
          return;
        }
        const script = document.createElement('script');
        script.src = src;
        script.onload = () => resolve();
        script.onerror = () => reject();
        document.body.appendChild(script);
      });
    };

    Promise.all([
      loadScript('/hls.min.js'),
      loadScript('/mpegts.min.js')
    ])
      .then(() => setLibsLoaded(true))
      .catch((err) => {
        console.error('Failed to load streaming helper libraries:', err);
        setPlayerError('Failed to load required player libraries (hls.js/mpegts.js).');
      });
  }, []);

  // 2. Initial Data Loading
  useEffect(() => {
    fetchPlaylists();
    fetchGroups();
    fetchChannels();
    fetchEPG();
  }, []);

  // 3. Auto refetch channels & groups when filters change
  useEffect(() => {
    fetchChannels();
  }, [selectedPlaylist, selectedGroup, searchQuery]);

  // 4. Keyboard Channel Surfing Handler (ArrowUp / ArrowDown)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (channels.length === 0 || document.activeElement?.tagName === 'INPUT' || document.activeElement?.tagName === 'TEXTAREA') {
        return;
      }

      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const currentIndex = selectedChannel 
          ? channels.findIndex(c => c.id === selectedChannel.id)
          : -1;

        let nextIndex = 0;
        if (currentIndex !== -1) {
          if (e.key === 'ArrowDown') {
            nextIndex = (currentIndex + 1) % channels.length;
          } else {
            nextIndex = (currentIndex - 1 + channels.length) % channels.length;
          }
        }

        const nextChan = channels[nextIndex];
        setSelectedChannel(nextChan);
        
        // Show temporary surf channel overlay HUD
        setSurfToast(`CH ${nextChan.channel_number || nextIndex + 1}: ${nextChan.name}`);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [channels, selectedChannel]);

  // Hide the surf HUD toast after 3 seconds
  useEffect(() => {
    if (surfToast) {
      const timer = setTimeout(() => setSurfToast(''), 3000);
      return () => clearTimeout(timer);
    }
  }, [surfToast]);

  // 5. Video Player setup when active channel changes
  useEffect(() => {
    if (!videoRef.current || !selectedChannel) return;

    const video = videoRef.current;
    const streamURL = selectedChannel.stream_url;
    let isActive = true;

    // Destroy existing players
    if (hlsPlayerRef.current) {
      try { hlsPlayerRef.current.destroy(); } catch (e) {}
      hlsPlayerRef.current = null;
    }
    if (tsPlayerRef.current) {
      try { tsPlayerRef.current.destroy(); } catch (e) {}
      tsPlayerRef.current = null;
    }

    video.src = '';

    const isHDHR = streamURL.includes(':5004/') || streamURL.includes('/auto/');
    const isHLS = streamURL.includes('.m3u8') || isHDHR;
    const isTS = streamURL.includes('.ts') && !isHDHR;

    const handleVideoError = () => {
      if (video.error) {
        setPlayerError(`Video Element Error: ${video.error.message} (Code ${video.error.code})`);
      }
    };
    video.addEventListener('error', handleVideoError);

    const setupPlayer = async () => {
      let playURL = streamURL;
      
      try {
        if (isHDHR) {
          // Request transcoded HLS stream from backend
          const resp = await fetch(`${apiBase || window.location.origin}/api/streams/start?url=${encodeURIComponent(streamURL)}`);
          if (!resp.ok) throw new Error("Failed to start HLS stream session");
          const data = await resp.json();
          if (data.error) throw new Error(data.error);
          playURL = `${apiBase || window.location.origin}${data.hls_url}`;
        } else if (isTS) {
          playURL = `${apiBase || window.location.origin}/api/streams/proxy?url=${encodeURIComponent(streamURL)}`;
        }

        if (!isActive) return;

        if (isHLS && window.Hls) {
          if (window.Hls.isSupported()) {
            const hls = new window.Hls({
              maxMaxBufferLength: 10,
              lowLatencyMode: true
            });
            hls.loadSource(playURL);
            hls.attachMedia(video);
            hls.on(window.Hls.Events.MANIFEST_PARSED, () => {
              video.play().catch(e => console.log('Autoplay blocked:', e));
            });
            hls.on(window.Hls.Events.ERROR, (_: any, data: any) => {
              if (data.fatal) {
                setPlayerError(`HLS Playback Error: ${data.type} - ${data.details}`);
              }
            });
            hlsPlayerRef.current = hls;
          } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
            video.src = playURL;
            video.addEventListener('canplay', () => {
              video.play().catch(e => console.log('Autoplay blocked:', e));
            });
          }
        } else if (isTS && window.mpegts) {
          if (window.mpegts.isSupported()) {
            const player = window.mpegts.createPlayer({
              type: 'mpegts',
              url: playURL,
              isLive: true
            }, {
              enableWorker: true,
              lazyLoad: false,
              enableStashBuffer: true,
              stashInitialSize: 1024 * 1024 * 3, // 3MB initial buffer for jitter
              liveBufferLatencyChasing: true,
              liveBufferLatencyMaxLatency: 3.5,
              liveBufferLatencyMinRemain: 0.5
            });
            player.attachMediaElement(video);
            player.load();
            player.play().catch((e: any) => console.log('Autoplay blocked:', e));
            
            player.on(window.mpegts.Events.ERROR, (type: any, detail: any, info: any) => {
              setPlayerError(`MPEG-TS Playback Error: ${type} - ${detail} (${info})`);
            });

            tsPlayerRef.current = player;
          } else {
            video.src = playURL;
            video.play().catch(e => console.log('Autoplay blocked:', e));
          }
        } else {
          video.src = playURL;
          video.play().catch(e => console.log('Autoplay blocked:', e));
        }
      } catch (err: any) {
        if (!isActive) return;
        console.error("Failed to initialize player:", err);
        setPlayerError(err.message || "Failed to initialize player");
      }
    };

    setupPlayer();

    return () => {
      isActive = false;
      video.removeEventListener('error', handleVideoError);
      if (hlsPlayerRef.current) {
        try { hlsPlayerRef.current.destroy(); } catch (e) {}
        hlsPlayerRef.current = null;
      }
      if (tsPlayerRef.current) {
        try { tsPlayerRef.current.destroy(); } catch (e) {}
        tsPlayerRef.current = null;
      }
    };
  }, [selectedChannel, libsLoaded]);

  // API Call functions

  const fetchPlaylists = async () => {
    try {
      const r = await fetch(`${apiBase}/api/playlists`);
      const data = await r.json();
      setPlaylists(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  };

  const fetchGroups = async () => {
    try {
      const r = await fetch(`${apiBase}/api/groups${selectedPlaylist ? `?playlistId=${selectedPlaylist}` : ''}`);
      const data = await r.json();
      setGroups(Array.isArray(data) ? data : []);
    } catch (err) {
      console.error(err);
    }
  };

  const fetchChannels = async () => {
    setLoading(true);
    try {
      let url = `${apiBase}/api/channels?`;
      if (selectedPlaylist) url += `playlistId=${selectedPlaylist}&`;
      if (selectedGroup) url += `groupId=${selectedGroup}&`;
      if (searchQuery) url += `search=${encodeURIComponent(searchQuery)}&`;

      const r = await fetch(url);
      const data = await r.json();
      const loadedChans = Array.isArray(data) ? data : [];
      setChannels(loadedChans);
      
      // Auto select first channel if none is selected
      if (loadedChans.length > 0 && !selectedChannel) {
        setSelectedChannel(loadedChans[0]);
      }
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const fetchEPG = async () => {
    try {
      const r = await fetch(`${apiBase}/api/epg/live`);
      const data = await r.json();
      setEpgData(data && typeof data === 'object' ? data : {});
    } catch (err) {
      console.error(err);
    }
  };

  const handleAddPlaylist = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPlaylistName || !newPlaylistURL) return;

    setLoading(true);
    try {
      const resp = await fetch(`${apiBase}/api/playlists`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newPlaylistName,
          url_path: newPlaylistURL,
          type: newPlaylistType,
          username: newUsername,
          password: newPassword
        })
      });
      const res = await resp.json();
      if (res.success) {
        setNewPlaylistName('');
        setNewPlaylistURL('');
        setNewUsername('');
        setNewPassword('');
        fetchPlaylists();
        fetchGroups();
        fetchChannels();
      } else {
        alert(res.error || 'Failed to add source');
      }
    } catch (err: any) {
      alert('Error registering source: ' + err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleSyncPlaylist = async (pID: string) => {
    setSyncingSources(prev => ({ ...prev, [pID]: true }));
    try {
      const resp = await fetch(`${apiBase}/api/playlists/${pID}/sync`, { method: 'POST' });
      const res = await resp.json();
      if (res.success) {
        fetchGroups();
        fetchChannels();
        alert('Sync complete!');
      } else {
        alert('Sync failed: ' + res.error);
      }
    } catch (err: any) {
      alert('Sync failed: ' + err.message);
    } finally {
      setSyncingSources(prev => ({ ...prev, [pID]: false }));
    }
  };

  const handleDeletePlaylist = async (pID: string) => {
    if (!confirm('Are you sure you want to delete this source? All associated channels and groups will be removed.')) return;
    try {
      await fetch(`${apiBase}/api/playlists/${pID}`, { method: 'DELETE' });
      if (selectedPlaylist === pID) setSelectedPlaylist('');
      fetchPlaylists();
      fetchGroups();
      fetchChannels();
    } catch (err) {
      console.error(err);
    }
  };

  const handleSyncEPG = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newEpgURL) return;

    setLoading(true);
    try {
      const resp = await fetch(`${apiBase}/api/epg/sync`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: newEpgURL })
      });
      const res = await resp.json();
      if (res.success) {
        setNewEpgURL('');
        fetchEPG();
        alert('EPG Guide synced successfully!');
      } else {
        alert(res.error || 'Failed to sync EPG');
      }
    } catch (err: any) {
      alert('EPG Sync error: ' + err.message);
    } finally {
      setLoading(false);
    }
  };

  // Helper to compute program progress bar width
  const getProgressPercentage = (start: string, end: string) => {
    if (!start || !end) return 0;
    const s = new Date(start).getTime();
    const e = new Date(end).getTime();
    const now = new Date().getTime();

    if (isNaN(s) || isNaN(e) || e <= s) return 0;
    if (now >= e) return 100;
    if (now <= s) return 0;

    return ((now - s) / (e - s)) * 100;
  };

  const getRemainingTime = (end: string) => {
    if (!end) return '';
    const d = new Date(end).getTime();
    if (isNaN(d)) return '';
    const diff = d - new Date().getTime();
    if (diff <= 0) return 'Ended';
    const mins = Math.round(diff / 60000);
    return `${mins} min left`;
  };

  const formatTimeStr = (dateStr: string | undefined) => {
    if (!dateStr) return '';
    try {
      const d = new Date(dateStr);
      if (isNaN(d.getTime())) return '';
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } catch (e) {
      return '';
    }
  };

  const activeEPG = selectedChannel ? epgData[selectedChannel.id] : null;

  return (
    <div className="flex h-screen bg-slate-950 text-slate-100 font-sans overflow-hidden select-none">
      {/* 1. Main Side Navigation (Channel List & Filter panel) */}
      <aside className="w-80 border-r border-slate-800 flex flex-col bg-slate-900/60 backdrop-blur-md flex-shrink-0">
        {/* Brand Header */}
        <div className="p-4 border-b border-slate-800 flex justify-between items-center bg-slate-900/90">
          <div className="flex items-center gap-2">
            <span className="flex w-3.5 h-3.5 bg-cyan-500 rounded-full animate-pulse shadow-lg shadow-cyan-400/40"></span>
            <h1 className="font-extrabold text-lg tracking-tight bg-gradient-to-r from-cyan-400 to-indigo-400 bg-clip-text text-transparent">
              STREAMTV
            </h1>
          </div>
          <button
            onClick={() => setShowManager(true)}
            className="p-2 rounded-lg bg-slate-800 hover:bg-slate-700 border border-slate-700 text-slate-300 transition duration-150"
            title="Manage Sources"
          >
            <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth="2.5" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
            </svg>
          </button>
        </div>

        {/* Filters */}
        <div className="p-4 space-y-3 border-b border-slate-800 bg-slate-900/30">
          {/* Playlist selector */}
          <select
            value={selectedPlaylist}
            onChange={(e) => {
              setSelectedPlaylist(e.target.value);
              setSelectedGroup('');
            }}
            className="w-full bg-slate-950/80 border border-slate-800 rounded-lg py-2 px-3 text-xs outline-none focus:border-cyan-500 transition duration-150"
          >
            <option value="">All Sources</option>
            {playlists.map(p => (
              <option key={p.id} value={p.id}>{p.name} ({p.type})</option>
            ))}
          </select>

          {/* Category selector */}
          <select
            value={selectedGroup}
            onChange={(e) => setSelectedGroup(e.target.value)}
            className="w-full bg-slate-950/80 border border-slate-800 rounded-lg py-2 px-3 text-xs outline-none focus:border-cyan-500 transition duration-150"
          >
            <option value="">All Categories</option>
            {groups.map(g => (
              <option key={g.id} value={g.id}>{g.name}</option>
            ))}
          </select>

          {/* Search channel */}
          <div className="relative">
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Search channels..."
              className="w-full bg-slate-950/80 border border-slate-800 rounded-lg py-2 pl-8 pr-3 text-xs outline-none focus:border-cyan-500 transition duration-150 placeholder-slate-500"
            />
            <span className="absolute left-2.5 top-2.5 text-slate-500">
              <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2.5" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
            </span>
          </div>
        </div>

        {/* Channel Grid List */}
        <div className="flex-1 overflow-y-auto divide-y divide-slate-800/40">
          {loading ? (
            <div className="p-8 text-center text-xs text-slate-500">
              <div className="w-6 h-6 border-2 border-slate-700 border-t-cyan-500 rounded-full animate-spin mx-auto mb-2"></div>
              Loading channel list...
            </div>
          ) : channels.length === 0 ? (
            <div className="p-8 text-center text-xs text-slate-500">
              No channels found. Add a source playlist to get started!
            </div>
          ) : (
            channels.map((chan) => {
              const active = selectedChannel?.id === chan.id;
              const chanEpg = epgData[chan.id];
              return (
                <button
                  key={chan.id}
                  onClick={() => setSelectedChannel(chan)}
                  className={`w-full p-3.5 text-left flex items-start gap-3 transition duration-150 ${active ? 'bg-gradient-to-r from-cyan-950/40 to-slate-900 border-l-4 border-cyan-500 bg-slate-900/90' : 'hover:bg-slate-800/40'}`}
                >
                  {chan.logo_url ? (
                    <img src={chan.logo_url} alt="" className="w-10 h-10 rounded-lg bg-slate-950 object-contain p-1 border border-slate-800/60" onError={(e) => { e.currentTarget.style.display = 'none'; }} />
                  ) : (
                    <div className="w-10 h-10 rounded-lg bg-slate-950 border border-slate-800/60 flex items-center justify-center font-black text-xs text-slate-600 uppercase">
                      {chan.name.slice(0, 2)}
                    </div>
                  )}

                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5">
                      {chan.channel_number > 0 && (
                        <span className="text-[10px] font-bold text-cyan-400/90 bg-cyan-950/60 px-1.5 py-0.5 rounded border border-cyan-800/30">
                          {chan.channel_number}
                        </span>
                      )}
                      <h4 className="text-xs font-semibold text-slate-200 truncate">{chan.name}</h4>
                    </div>

                    {chanEpg?.current ? (
                      <p className="text-[10px] text-slate-400 truncate mt-1">
                        📺 {chanEpg.current.title}
                      </p>
                    ) : (
                      <p className="text-[10px] text-slate-500 truncate mt-1">
                        No guide info available
                      </p>
                    )}
                  </div>
                </button>
              );
            })
          )}
        </div>

        {/* Surfing instructions footer */}
        <div className="p-3 bg-slate-950 border-t border-slate-800 text-[10px] text-center text-slate-500 font-medium">
          ⌨️ Surf channels using <kbd className="bg-slate-900 border border-slate-700 px-1 py-0.5 rounded text-slate-300">▲</kbd> / <kbd className="bg-slate-900 border border-slate-700 px-1 py-0.5 rounded text-slate-300">▼</kbd> keys
        </div>
      </aside>

      {/* 2. Main content area (Theater view player & EPG Timeline Grid) */}
      <main className="flex-1 flex flex-col min-w-0 bg-slate-950 relative">
        {/* Channel Surfing HUD overlay */}
        {surfToast && (
          <div className="absolute top-8 right-8 z-50 pointer-events-none animate-in fade-in zoom-in-95 duration-150">
            <div className="bg-cyan-950/90 text-cyan-300 border-2 border-cyan-500/50 shadow-2xl shadow-cyan-500/20 backdrop-blur-md rounded-2xl px-6 py-4 flex items-center gap-4">
              <span className="text-4xl font-extrabold tracking-tight select-none">📶</span>
              <div>
                <div className="text-[10px] uppercase font-bold tracking-widest text-cyan-400">Surfing Channel</div>
                <div className="text-lg font-black text-white leading-tight mt-0.5">{surfToast}</div>
              </div>
            </div>
          </div>
        )}

        {/* Video Player Display Container */}
        <div className="flex-1 bg-black relative flex items-center justify-center group overflow-hidden">
          {selectedChannel ? (
            <>
              <video
                ref={videoRef}
                controls
                autoPlay
                className="w-full h-full object-contain"
              />
              {playerError && (
                <div className="absolute inset-0 bg-slate-950/95 flex flex-col items-center justify-center p-6 text-center z-20">
                  <span className="text-4xl animate-bounce">⚠️</span>
                  <h3 className="text-md font-black text-red-500 mt-4 tracking-wide uppercase">Playback Interrupted</h3>
                  <p className="text-xs text-slate-300 max-w-md mt-2 font-mono bg-slate-900 border border-slate-800 p-3 rounded-lg leading-relaxed">{playerError}</p>
                  <button 
                    onClick={() => { setPlayerError(null); setSelectedChannel({...selectedChannel}); }} 
                    className="mt-6 px-5 py-2.5 bg-cyan-600 hover:bg-cyan-500 text-xs font-black tracking-widest uppercase rounded-xl text-white transition duration-150 shadow-lg shadow-cyan-600/35 border-0 cursor-pointer"
                  >
                    Retry Channel
                  </button>
                </div>
              )}
            </>
          ) : (
            <div className="text-center text-slate-500 space-y-2 p-8">
              <span className="text-5xl">📺</span>
              <p className="text-sm font-semibold">Select a channel to start surfing</p>
            </div>
          )}

          {/* Bottom HUD bar of Player showing EPG details */}
          {selectedChannel && activeEPG?.current && (
            <div className="absolute bottom-12 left-0 right-0 p-4 bg-gradient-to-t from-slate-950/90 via-slate-950/80 to-transparent flex items-end justify-between gap-6 pointer-events-none opacity-0 group-hover:opacity-100 transition duration-300">
              <div className="flex-1 max-w-xl space-y-1">
                <span className="text-[10px] font-bold text-cyan-400 bg-cyan-950/80 border border-cyan-500/20 px-2 py-0.5 rounded-full uppercase tracking-wider">
                  Live Show
                </span>
                <h2 className="text-lg font-extrabold text-white leading-tight">{activeEPG.current.title}</h2>
                <p className="text-xs text-slate-300 line-clamp-2 mt-1">{activeEPG.current.description}</p>
                
                {/* Time slider */}
                <div className="flex items-center gap-3 pt-2">
                  <div className="flex-1 h-1.5 bg-slate-800 rounded-full overflow-hidden">
                    <div 
                      className="h-full bg-cyan-500 rounded-full"
                      style={{ width: `${getProgressPercentage(activeEPG.current.start_time, activeEPG.current.end_time)}%` }}
                    ></div>
                  </div>
                  <span className="text-[10px] font-semibold text-cyan-400 flex-shrink-0">
                    {getRemainingTime(activeEPG.current.end_time)}
                  </span>
                </div>
              </div>

              {activeEPG.next && (
                <div className="w-64 bg-slate-900/80 border border-slate-800/80 rounded-xl p-3 shadow-lg flex-shrink-0 text-left">
                  <div className="text-[9px] uppercase font-bold tracking-wider text-slate-400">Up Next</div>
                  <h4 className="text-xs font-bold text-slate-200 truncate mt-0.5">{activeEPG.next.title}</h4>
                  <p className="text-[10px] text-slate-400 truncate mt-0.5">{formatTimeStr(activeEPG.next.start_time)}</p>
                </div>
              )}
            </div>
          )}
        </div>

        {/* 3. Bottom Guide / EPG Grid Timeline */}
        <footer className="h-64 border-t border-slate-800 flex flex-col bg-slate-900/40 backdrop-blur-md flex-shrink-0">
          <div className="p-3 border-b border-slate-800 flex justify-between items-center bg-slate-900/60">
            <span className="text-xs font-bold text-slate-300 uppercase tracking-wider flex items-center gap-2">
              📅 Electronic Program Guide (EPG)
            </span>
            <div className="text-[10px] text-slate-500">
              Showing active programs synced to the guide database
            </div>
          </div>

          <div className="flex-1 overflow-auto p-4">
            {channels.length === 0 ? (
              <div className="text-center text-xs text-slate-500 pt-8">
                EPG timeline is empty. Sync a guide feed to view schedules.
              </div>
            ) : (
              <div className="space-y-3 min-w-[600px]">
                {channels.slice(0, 10).map(chan => {
                  const epg = epgData[chan.id];
                  return (
                    <div key={chan.id} className="grid grid-cols-[180px_1fr] items-center gap-4 bg-slate-900/20 border border-slate-800/40 hover:border-slate-800 rounded-xl p-3.5 transition duration-150">
                      <div className="flex items-center gap-3 min-w-0">
                        {chan.logo_url && (
                          <img src={chan.logo_url} alt="" className="w-8 h-8 rounded bg-slate-950 object-contain p-1 border border-slate-800" onError={(e) => { e.currentTarget.style.display = 'none'; }} />
                        )}
                        <h4 className="text-xs font-bold text-slate-200 truncate">{chan.name}</h4>
                      </div>

                      <div className="grid grid-cols-2 gap-4">
                        {/* Current program block */}
                        {epg?.current ? (
                          <div className="bg-slate-900/80 border border-slate-800 rounded-lg p-2.5 text-left relative overflow-hidden">
                            <div className="text-[9px] font-bold text-cyan-400 uppercase tracking-wider flex justify-between">
                              <span>Now Playing</span>
                              <span>{formatTimeStr(epg.current.start_time)} - {formatTimeStr(epg.current.end_time)}</span>
                            </div>
                            <h5 className="text-xs font-bold text-slate-200 truncate mt-1">{epg.current.title}</h5>
                            <p className="text-[10px] text-slate-400 line-clamp-1 mt-0.5">{epg.current.description}</p>
                            
                            <div className="absolute bottom-0 left-0 right-0 h-1 bg-slate-850">
                              <div 
                                className="h-full bg-cyan-500" 
                                style={{ width: `${getProgressPercentage(epg.current.start_time, epg.current.end_time)}%` }}
                              ></div>
                            </div>
                          </div>
                        ) : (
                          <div className="bg-slate-900/20 border border-dashed border-slate-800/60 rounded-lg p-2.5 text-left flex items-center justify-center">
                            <span className="text-[10px] text-slate-500 font-medium">No live show data</span>
                          </div>
                        )}

                        {/* Next program block */}
                        {epg?.next ? (
                          <div className="bg-slate-900/40 border border-slate-800/60 rounded-lg p-2.5 text-left">
                            <div className="text-[9px] font-bold text-slate-400 uppercase tracking-wider flex justify-between">
                              <span>Up Next</span>
                              <span>{formatTimeStr(epg.next.start_time)}</span>
                            </div>
                            <h5 className="text-xs font-bold text-slate-300 truncate mt-1">{epg.next.title}</h5>
                            <p className="text-[10px] text-slate-500 truncate mt-0.5">{epg.next.description || 'No description available'}</p>
                          </div>
                        ) : (
                          <div className="bg-slate-900/10 border border-dashed border-slate-800/30 rounded-lg p-2.5 text-left flex items-center justify-center">
                            <span className="text-[10px] text-slate-600 font-medium">No program scheduled next</span>
                          </div>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </footer>
      </main>

      {/* 4. Settings Manager Modal (Manage feeds/tuner sources & EPG URLs) */}
      {showManager && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-md p-4 animate-in fade-in duration-200">
          <div className="w-full max-w-4xl bg-slate-900 border border-slate-800 rounded-3xl shadow-2xl flex flex-col h-[600px] overflow-hidden">
            {/* Modal Header */}
            <div className="p-6 border-b border-slate-800 flex justify-between items-center bg-slate-900/95">
              <div>
                <h2 className="text-xl font-extrabold text-white flex items-center gap-2">
                  ⚙️ TV Playlist & EPG Manager
                </h2>
                <p className="text-xs text-slate-400 mt-1">Configure your IPTV playlist files, Xtream Servers, HDHomeRun Tuners, and XMLTV Guide links.</p>
              </div>
              <button
                onClick={() => setShowManager(false)}
                className="text-slate-400 hover:text-white p-2 rounded-lg bg-slate-800 hover:bg-slate-700 transition"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" strokeWidth="2.5" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            {/* Modal Body */}
            <div className="flex-1 overflow-y-auto p-6 grid grid-cols-1 md:grid-cols-[1fr_1.2fr] gap-8">
              {/* Left Column: Form inputs */}
              <div className="space-y-8">
                {/* Add Playlist Form */}
                <form onSubmit={handleAddPlaylist} className="space-y-4">
                  <h3 className="text-xs font-bold text-cyan-400 uppercase tracking-widest border-b border-slate-800 pb-2">
                    ➕ Register Tuner / Feed Source
                  </h3>

                  <div className="space-y-2">
                    <label className="text-[10px] uppercase font-bold text-slate-400">Playlist Name</label>
                    <input
                      type="text"
                      value={newPlaylistName}
                      onChange={(e) => setNewPlaylistName(e.target.value)}
                      placeholder="My Cable TV, Home Antenna"
                      required
                      className="w-full bg-slate-950 border border-slate-800 focus:border-cyan-500 rounded-lg py-2 px-3 text-xs outline-none transition duration-150"
                    />
                  </div>

                  <div className="space-y-2">
                    <label className="text-[10px] uppercase font-bold text-slate-400">Source Type</label>
                    <select
                      value={newPlaylistType}
                      onChange={(e) => setNewPlaylistType(e.target.value)}
                      className="w-full bg-slate-950 border border-slate-800 focus:border-cyan-500 rounded-lg py-2 px-3 text-xs outline-none transition duration-150"
                    >
                      <option value="M3U">M3U Playlist URL or Raw String</option>
                      <option value="XTREAM">Xtream Codes Server URL</option>
                      <option value="HDHOMERUN">HDHomeRun Tuner IP/URL</option>
                    </select>
                  </div>

                  <div className="space-y-2">
                    <label className="text-[10px] uppercase font-bold text-slate-400">
                      {newPlaylistType === 'HDHOMERUN' ? 'Tuner IP / Lineup URL' : 'File URL / Server URL'}
                    </label>
                    <input
                      type="text"
                      value={newPlaylistURL}
                      onChange={(e) => setNewPlaylistURL(e.target.value)}
                      placeholder={newPlaylistType === 'HDHOMERUN' ? '192.168.1.100' : 'http://server.com/playlist.m3u'}
                      required
                      className="w-full bg-slate-950 border border-slate-800 focus:border-cyan-500 rounded-lg py-2 px-3 text-xs outline-none transition duration-150"
                    />
                  </div>

                  {newPlaylistType === 'XTREAM' && (
                    <div className="grid grid-cols-2 gap-4">
                      <div className="space-y-2">
                        <label className="text-[10px] uppercase font-bold text-slate-400">Username</label>
                        <input
                          type="text"
                          value={newUsername}
                          onChange={(e) => setNewUsername(e.target.value)}
                          required
                          className="w-full bg-slate-950 border border-slate-800 focus:border-cyan-500 rounded-lg py-2 px-3 text-xs outline-none transition"
                        />
                      </div>
                      <div className="space-y-2">
                        <label className="text-[10px] uppercase font-bold text-slate-400">Password</label>
                        <input
                          type="password"
                          value={newPassword}
                          onChange={(e) => setNewPassword(e.target.value)}
                          required
                          className="w-full bg-slate-950 border border-slate-800 focus:border-cyan-500 rounded-lg py-2 px-3 text-xs outline-none transition"
                        />
                      </div>
                    </div>
                  )}

                  <button
                    type="submit"
                    disabled={loading}
                    className="w-full py-2.5 rounded-lg bg-gradient-to-r from-cyan-500 to-indigo-600 hover:from-cyan-400 hover:to-indigo-500 text-white font-bold text-xs shadow-lg transition duration-150 disabled:opacity-50"
                  >
                    Add and Sync Source
                  </button>
                </form>

                {/* Add EPG Form */}
                <form onSubmit={handleSyncEPG} className="space-y-4 pt-4 border-t border-slate-800">
                  <h3 className="text-xs font-bold text-cyan-400 uppercase tracking-widest border-b border-slate-800 pb-2">
                    📅 Sync XMLTV EPG Guide
                  </h3>

                  <div className="space-y-2">
                    <label className="text-[10px] uppercase font-bold text-slate-400">XMLTV EPG URL</label>
                    <input
                      type="url"
                      value={newEpgURL}
                      onChange={(e) => setNewEpgURL(e.target.value)}
                      placeholder="http://server.com/epg.xml"
                      required
                      className="w-full bg-slate-950 border border-slate-800 focus:border-cyan-500 rounded-lg py-2 px-3 text-xs outline-none transition duration-150"
                    />
                  </div>

                  <button
                    type="submit"
                    disabled={loading}
                    className="w-full py-2.5 rounded-lg bg-slate-800 hover:bg-slate-700 border border-slate-700 text-cyan-400 font-bold text-xs transition duration-150 disabled:opacity-50"
                  >
                    Sync Guide Database
                  </button>
                </form>
              </div>

              {/* Right Column: Registered sources list */}
              <div className="space-y-4 flex flex-col h-full">
                <h3 className="text-xs font-bold text-cyan-400 uppercase tracking-widest border-b border-slate-800 pb-2">
                  ⚙️ Active Sources
                </h3>

                <div className="flex-1 overflow-y-auto space-y-3 pr-2">
                  {playlists.length === 0 ? (
                    <p className="text-xs text-slate-500 text-center py-12">No sources registered yet.</p>
                  ) : (
                    playlists.map(p => (
                      <div key={p.id} className="bg-slate-950 border border-slate-800 rounded-xl p-4 flex flex-col gap-3 justify-between">
                        <div>
                          <div className="flex items-center justify-between">
                            <h4 className="text-sm font-bold text-slate-200">{p.name}</h4>
                            <span className="text-[9px] font-bold text-cyan-400 bg-cyan-950/80 border border-cyan-800/30 px-2 py-0.5 rounded">
                              {p.type}
                            </span>
                          </div>
                          <p className="text-[10px] text-slate-500 mt-1 truncate max-w-[340px]">{p.url_path}</p>
                        </div>

                        <div className="flex justify-end gap-3 border-t border-slate-800/40 pt-3">
                          <button
                            onClick={() => handleSyncPlaylist(p.id)}
                            disabled={syncingSources[p.id]}
                            className="text-[10px] font-bold text-cyan-400 hover:text-cyan-300 transition disabled:opacity-50"
                          >
                            {syncingSources[p.id] ? 'Syncing...' : 'Sync'}
                          </button>
                          <button
                            onClick={() => handleDeletePlaylist(p.id)}
                            className="text-[10px] font-bold text-rose-400 hover:text-rose-300 transition"
                          >
                            Delete
                          </button>
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default App;
