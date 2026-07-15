package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"streamapp/backend/internal/database"
	"streamapp/backend/internal/models"
	"streamapp/backend/internal/parser"
)

type HLSSession struct {
	ID           string
	Dir          string
	LastAccessed time.Time
	Cmd          *exec.Cmd
	Cancel       context.CancelFunc
}

var (
	hlsSessions   = make(map[string]*HLSSession)
	hlsSessionsMu sync.Mutex
)

func init() {
	go func() {
		for {
			time.Sleep(5 * time.Second)
			hlsSessionsMu.Lock()
			now := time.Now()
			for id, sess := range hlsSessions {
				if now.Sub(sess.LastAccessed) > 15*time.Second {
					fmt.Printf("[HLS Manager] Session %s timed out, cleaning up...\n", id)
					sess.Cancel()
					go func(dir string) {
						time.Sleep(2 * time.Second)
						os.RemoveAll(dir)
					}(sess.Dir)
					delete(hlsSessions, id)
				}
			}
			hlsSessionsMu.Unlock()
		}
	}()
}

// PlaylistRequest represents the POST payload to add a playlist source.
type PlaylistRequest struct {
	Name     string `json:"name"`
	URLPath  string `json:"url_path"`
	Type     string `json:"type"` // "M3U", "Xtream", "HDHomeRun"
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// GetPlaylists lists all configured playlists.
func GetPlaylists(w http.ResponseWriter, r *http.Request) {
	rows, err := database.DB.Query("SELECT id, name, url_path, type, created_at FROM playlists ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	playlists := []models.Playlist{}
	for rows.Next() {
		var p models.Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.URLPath, &p.Type, &p.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		playlists = append(playlists, p)
	}

	writeJSON(w, http.StatusOK, playlists)
}

// AddPlaylist registers a new playlist and performs an initial channels sync.
func AddPlaylist(w http.ResponseWriter, r *http.Request) {
	var req PlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.Type = strings.ToUpper(req.Type)
	if req.Type != "M3U" && req.Type != "XTREAM" && req.Type != "HDHOMERUN" {
		writeError(w, http.StatusBadRequest, "Invalid playlist type. Must be M3U, Xtream, or HDHomeRun")
		return
	}

	pID := uuid.New().String()

	// Insert playlist metadata
	_, err := database.DB.Exec(
		"INSERT INTO playlists (id, name, url_path, type) VALUES (?, ?, ?, ?)",
		pID, req.Name, req.URLPath, req.Type,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create playlist: "+err.Error())
		return
	}

	// Trigger initial sync
	if err := syncPlaylistSource(pID, req.URLPath, req.Type, req.Username, req.Password); err != nil {
		// Rollback playlist insertion if sync failed
		database.DB.Exec("DELETE FROM playlists WHERE id = ?", pID)
		writeError(w, http.StatusInternalServerError, "Failed initial channels sync: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success":     true,
		"playlist_id": pID,
		"message":     "Playlist created and synced successfully",
	})
}

// SyncPlaylist triggers a manual resync of channels for a playlist.
func SyncPlaylist(w http.ResponseWriter, r *http.Request) {
	pID := chi.URLParam(r, "id")

	var urlPath, pType string
	err := database.DB.QueryRow("SELECT url_path, type FROM playlists WHERE id = ?", pID).Scan(&urlPath, &pType)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "Playlist not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// For Xtream sync, we don't store credentials in the playlist table.
	// Credentials must either be passed in body or we return error.
	// We'll support an optional body for username/password.
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := syncPlaylistSource(pID, urlPath, pType, req.Username, req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, "Resync failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Playlist channels resynced successfully",
	})
}

// DeletePlaylist removes a playlist and cascades all its channels and categories.
func DeletePlaylist(w http.ResponseWriter, r *http.Request) {
	pID := chi.URLParam(r, "id")

	tx, err := database.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	// 1. Delete EPG programs associated with this playlist's channels
	_, err = tx.Exec("DELETE FROM epg_programs WHERE channel_id IN (SELECT id FROM channels WHERE playlist_id = ?)", pID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 2. Delete channels
	_, err = tx.Exec("DELETE FROM channels WHERE playlist_id = ?", pID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 3. Delete channel groups
	_, err = tx.Exec("DELETE FROM channel_groups WHERE playlist_id = ?", pID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 4. Delete the playlist itself
	res, err := tx.Exec("DELETE FROM playlists WHERE id = ?", pID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "Playlist not found")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Playlist and all associated channels deleted successfully",
	})
}

// GetGroups retrieves all channel categories (ChannelGroups).
func GetGroups(w http.ResponseWriter, r *http.Request) {
	playlistID := r.URL.Query().Get("playlistId")

	var rows *sql.Rows
	var err error
	if playlistID != "" {
		rows, err = database.DB.Query("SELECT id, playlist_id, name FROM channel_groups WHERE playlist_id = ? ORDER BY name ASC", playlistID)
	} else {
		rows, err = database.DB.Query("SELECT id, playlist_id, name FROM channel_groups ORDER BY name ASC")
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	groups := []models.ChannelGroup{}
	for rows.Next() {
		var g models.ChannelGroup
		if err := rows.Scan(&g.ID, &g.PlaylistID, &g.Name); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		groups = append(groups, g)
	}

	writeJSON(w, http.StatusOK, groups)
}

// GetChannels returns a list of channels with optional filter query parameters.
func GetChannels(w http.ResponseWriter, r *http.Request) {
	playlistID := r.URL.Query().Get("playlistId")
	groupID := r.URL.Query().Get("groupId")
	search := r.URL.Query().Get("search")

	query := "SELECT id, group_id, name, stream_url, logo_url, channel_number FROM channels WHERE 1=1"
	args := []interface{}{}

	if playlistID != "" {
		query += " AND playlist_id = ?"
		args = append(args, playlistID)
	}
	if groupID != "" {
		query += " AND group_id = ?"
		args = append(args, groupID)
	}
	if search != "" {
		query += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY channel_number ASC, name ASC"

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	channels := []models.Channel{}
	for rows.Next() {
		var c models.Channel
		var groupIDOpt sql.NullString
		var logoURLOpt sql.NullString
		if err := rows.Scan(&c.ID, &groupIDOpt, &c.Name, &c.StreamURL, &logoURLOpt, &c.ChannelNumber); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		c.GroupID = groupIDOpt.String
		c.LogoURL = logoURLOpt.String
		channels = append(channels, c)
	}

	writeJSON(w, http.StatusOK, channels)
}

// SyncEPGHandler parses and syncs EPG data from an XMLTV URL.
func SyncEPGHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, http.StatusBadRequest, "Invalid request body. URL is required")
		return
	}

	if err := syncEPGSource(req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, "EPG Sync failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "EPG XMLTV data synced successfully",
	})
}

// GetLiveEPG retrieves the current and next program listing for active channels.
func GetLiveEPG(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Format("2006-01-02 15:04:05")

	// Get current active programs
	currentQuery := `
		SELECT channel_id, title, description, start_time, end_time 
		FROM epg_programs 
		WHERE start_time <= ? AND end_time > ?`
	
	rows, err := database.DB.Query(currentQuery, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type ProgramDetails struct {
		Title       string    `json:"title"`
		Description string    `json:"description"`
		StartTime   time.Time `json:"start_time"`
		EndTime     time.Time `json:"end_time"`
	}

	type ChannelEPG struct {
		Current *ProgramDetails `json:"current"`
		Next    *ProgramDetails `json:"next"`
	}

	epgMap := make(map[string]*ChannelEPG)

	for rows.Next() {
		var chanID string
		var p ProgramDetails
		if err := rows.Scan(&chanID, &p.Title, &p.Description, &p.StartTime, &p.EndTime); err == nil {
			epgMap[chanID] = &ChannelEPG{
				Current: &p,
			}
		}
	}

	// Fetch next programs (programs starting after now, sorted by start_time, grouped by channel)
	nextQuery := `
		SELECT channel_id, title, description, start_time, end_time 
		FROM epg_programs 
		WHERE start_time > ? 
		ORDER BY start_time ASC`
	
	nextRows, err := database.DB.Query(nextQuery, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer nextRows.Close()

	for nextRows.Next() {
		var chanID string
		var p ProgramDetails
		if err := nextRows.Scan(&chanID, &p.Title, &p.Description, &p.StartTime, &p.EndTime); err == nil {
			if entry, exists := epgMap[chanID]; exists {
				if entry.Next == nil {
					entry.Next = &p
				}
			} else {
				epgMap[chanID] = &ChannelEPG{
					Next: &p,
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, epgMap)
}

// Helpers

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"success": false,
		"error":   msg,
	})
}

func syncPlaylistSource(pID, urlPath, pType, username, password string) error {
	pType = strings.ToUpper(pType)
	switch pType {
	case "M3U":
		return syncM3U(pID, urlPath)
	case "XTREAM":
		if username == "" || password == "" {
			return fmt.Errorf("username and password are required for Xtream Codes sync")
		}
		return syncXtream(pID, urlPath, username, password)
	case "HDHOMERUN":
		return syncHDHomeRun(pID, urlPath)
	default:
		return fmt.Errorf("unsupported playlist type: %s", pType)
	}
}

func syncM3U(pID, urlPath string) error {
	var r io.Reader
	if strings.HasPrefix(urlPath, "http://") || strings.HasPrefix(urlPath, "https://") {
		resp, err := http.Get(urlPath)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		r = resp.Body
	} else {
		r = strings.NewReader(urlPath)
	}

	channels, err := parser.ParseM3U(r)
	if err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear old data
	_, _ = tx.Exec("DELETE FROM channel_groups WHERE playlist_id = ?", pID)

	groupMap := make(map[string]string)
	for _, ch := range channels {
		gName := ch.GroupID
		if gName == "" {
			gName = "Uncategorized"
		}
		if _, exists := groupMap[gName]; !exists {
			gID := uuid.New().String()
			_, err = tx.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)", gID, pID, gName)
			if err != nil {
				return err
			}
			groupMap[gName] = gID
		}
	}

	stmt, err := tx.Prepare("INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ch := range channels {
		gName := ch.GroupID
		if gName == "" {
			gName = "Uncategorized"
		}
		gID := groupMap[gName]
		_, err = stmt.Exec(ch.ID, pID, gID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func syncXtream(pID, urlPath, username, password string) error {
	groups, channels, err := parser.FetchXtream(urlPath, username, password)
	if err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM channel_groups WHERE playlist_id = ?", pID)

	groupStmt, err := tx.Prepare("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer groupStmt.Close()

	for _, g := range groups {
		_, err = groupStmt.Exec(g.ID, pID, g.Name)
		if err != nil {
			return err
		}
	}

	chanStmt, err := tx.Prepare("INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer chanStmt.Close()

	for _, ch := range channels {
		_, err = chanStmt.Exec(ch.ID, pID, ch.GroupID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func syncHDHomeRun(pID, urlPath string) error {
	channels, err := parser.FetchHDHomeRunChannels(urlPath)
	if err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM channel_groups WHERE playlist_id = ?", pID)

	gID := uuid.New().String()
	_, err = tx.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)", gID, pID, "HDHomeRun")
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ch := range channels {
		_, err = stmt.Exec(ch.ID, pID, gID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func syncEPGSource(xmltvURL string) error {
	var r io.Reader
	if strings.HasPrefix(xmltvURL, "http://") || strings.HasPrefix(xmltvURL, "https://") {
		resp, err := http.Get(xmltvURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		r = resp.Body
	} else {
		return fmt.Errorf("EPG URL must start with http or https")
	}

	// Fetch channels for matching
	rows, err := database.DB.Query("SELECT id, name FROM channels")
	if err != nil {
		return err
	}
	defer rows.Close()

	channelMap := make(map[string]string)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err == nil {
			channelMap[strings.ToLower(name)] = id
		}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear old guide data
	_, _ = tx.Exec("DELETE FROM epg_programs")

	stmt, err := tx.Prepare("INSERT INTO epg_programs (id, channel_id, title, description, start_time, end_time) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	err = parser.ParseXMLTV(r, func(prog models.EPGProgram) error {
		epgChanID := strings.ToLower(prog.ChannelID)
		var matchedChanID string

		if id, exists := channelMap[epgChanID]; exists {
			matchedChanID = id
		} else {
			for name, id := range channelMap {
				if strings.Contains(epgChanID, name) || strings.Contains(name, epgChanID) {
					matchedChanID = id
					break
				}
			}
		}

		if matchedChanID != "" {
			progID := uuid.New().String()
			_, err = stmt.Exec(progID, matchedChanID, prog.Title, prog.Description, prog.StartTime, prog.EndTime)
			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return tx.Commit()
}

// flushWriter wraps a writer and flushes it on every write to reduce streaming latency.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (n int, err error) {
	n, err = fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return
}

// ProxyStream proxies HTTP video streams to bypass browser CORS blocks and optionally transcodes to H.264 (e.g. for HDHomeRun).
func ProxyStream(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}

	transcode := r.URL.Query().Get("transcode") == "true"

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if transcode {
		w.Header().Set("Content-Type", "video/mp2t")

		// 1. Fetch the input stream from HDHomeRun in Go
		req, err := http.NewRequestWithContext(r.Context(), "GET", streamURL, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		req.Header.Set("User-Agent", r.UserAgent())

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to reach tuner: "+err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			writeError(w, resp.StatusCode, "tuner returned error status: "+resp.Status)
			return
		}

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		var cmd *exec.Cmd
		var stdin io.WriteCloser
		var stdout io.ReadCloser
		var stderr io.ReadCloser
		var selectedEncoder string

		// Hardware-accelerated and software fallback encoders to test
		encodersToTry := []string{"h264_nvenc", "h264_amf", "h264_qsv", "h264_mf", "libx264"}

		for _, encoder := range encodersToTry {
			fmt.Printf("[Transcoder] Probing H.264 encoder: %s\n", encoder)

			args := []string{
				"-hwaccel", "auto",
				"-fflags", "+nobuffer+genpts", // Minimize input buffering latency and generate missing timestamps
				"-f", "mpegts", // Force input format to MPEG-TS
				"-i", "pipe:0", // Read from Go network pipe
			}

			// Add encoder-specific low-latency tuning flags
			if encoder == "libx264" {
				args = append(args,
					"-c:v", "libx264",
					"-preset", "ultrafast",
					"-tune", "zerolatency",
					"-profile:v", "baseline",
					"-level", "3.0",
				)
			} else {
				args = append(args, "-c:v", encoder)
				if encoder == "h264_nvenc" {
					args = append(args, "-preset", "p1", "-tune", "ull") // Ultra-low latency NVENC
				}
				if encoder == "h264_qsv" {
					args = append(args, "-preset", "veryfast")
				}
			}

			// Common transcoding filter and output parameters
			args = append(args,
				"-threads", "0", // Use all CPU cores if software fallbacks kick in
				"-vf", "bwdif=0:-1:0,scale=-2:720:flags=fast_bilinear", // Deinterlace live OTA streams and scale to 720p
				"-pix_fmt", "yuv420p", // Force browser-friendly pixel format
				"-g", "30",
				"-keyint_min", "30",
				"-sc_threshold", "0",
				"-c:a", "aac",
				"-ac", "2",
				"-b:a", "128k",
				"-ar", "44100",
				"-f", "mpegts",
				"-mpegts_flags", "+pat_pmt_at_frames",
				"pipe:1",
			)

			trialCmd := exec.CommandContext(ctx, "ffmpeg", args...)

			trialStdin, err := trialCmd.StdinPipe()
			if err != nil {
				continue
			}

			trialStdout, err := trialCmd.StdoutPipe()
			if err != nil {
				trialStdin.Close()
				continue
			}

			trialStderr, err := trialCmd.StderrPipe()
			if err != nil {
				trialStdin.Close()
				trialStdout.Close()
				continue
			}

			if err := trialCmd.Start(); err != nil {
				trialStdin.Close()
				trialStdout.Close()
				trialStderr.Close()
				continue
			}

			// Monitor the encoder process for 300ms to catch hardware unsupported crashes
			exitChan := make(chan error, 1)
			go func() {
				exitChan <- trialCmd.Wait()
			}()

			select {
			case err := <-exitChan:
				fmt.Printf("[Transcoder] Encoder %s unsupported or exited immediately: %v\n", encoder, err)
				trialStdin.Close()
				trialStdout.Close()
				trialStderr.Close()
				continue
			case <-time.After(300 * time.Millisecond):
				fmt.Printf("[Transcoder] Successfully initialized encoder: %s\n", encoder)
				cmd = trialCmd
				stdin = trialStdin
				stdout = trialStdout
				stderr = trialStderr
				selectedEncoder = encoder

				// Start streaming response body from Go client to FFmpeg stdin
				go func() {
					defer stdin.Close()
					buf := make([]byte, 128*1024) // 128KB network buffer
					_, _ = io.CopyBuffer(stdin, resp.Body, buf)
				}()

				// Run Wait in background so exitChan doesn't leak
				go func() {
					err := <-exitChan
					fmt.Printf("[Transcoder] Encoder %s stopped: %v\n", selectedEncoder, err)
				}()
				break
			}
			if cmd != nil {
				break
			}
		}

		if cmd == nil {
			writeError(w, http.StatusInternalServerError, "all H.264 video encoders failed to initialize")
			return
		}

		// Goroutine to drain stderr logs
		go func() {
			buf := make([]byte, 2048)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					fmt.Printf("[FFMPEG] %s", string(buf[:n]))
				}
				if err != nil {
					break
				}
			}
		}()

		var writer io.Writer = w
		if flusher, ok := w.(http.Flusher); ok {
			writer = flushWriter{
				w: w,
				f: flusher,
			}
		}

		// Direct stdout pipe copy to HTTP ResponseWriter
		_, _ = io.Copy(writer, stdout)
		return
	}

	// Normal non-transcoded proxy stream
	req, err := http.NewRequestWithContext(r.Context(), "GET", streamURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	req.Header.Set("User-Agent", r.UserAgent())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to reach stream target: "+err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	if length := resp.Header.Get("Content-Length"); length != "" {
		w.Header().Set("Content-Length", length)
	}

	w.WriteHeader(resp.StatusCode)

	var writer io.Writer = w
	if flusher, ok := w.(http.Flusher); ok {
		writer = flushWriter{
			w: w,
			f: flusher,
		}
	}

	_, _ = io.Copy(writer, resp.Body)
}

// StartHLSStream starts an HLS transcoding session for the given stream URL and returns the playlist URL.
func StartHLSStream(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}

	hash := sha256.Sum256([]byte(streamURL))
	id := hex.EncodeToString(hash[:])[:16]

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		sess.LastAccessed = time.Now()
		hlsSessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"hls_url": fmt.Sprintf("/api/streams/hls/%s/stream.m3u8", id),
		})
		return
	}

	tempDir := filepath.Join(os.TempDir(), "streamapp_hls", id)
	os.MkdirAll(tempDir, 0755)

	ctx, cancel := context.WithCancel(context.Background())
	sess = &HLSSession{
		ID:           id,
		Dir:          tempDir,
		LastAccessed: time.Now(),
		Cancel:       cancel,
	}
	hlsSessions[id] = sess
	hlsSessionsMu.Unlock()

	// 1. Fetch the input stream from HDHomeRun in Go
	req, err := http.NewRequestWithContext(ctx, "GET", streamURL, nil)
	if err != nil {
		sess.Cancel()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("User-Agent", r.UserAgent())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		sess.Cancel()
		writeError(w, http.StatusBadGateway, "failed to reach tuner: "+err.Error())
		return
	}

	var cmd *exec.Cmd
	var stdin io.WriteCloser
	var stderr io.ReadCloser
	var selectedEncoder string

	encodersToTry := []string{"h264_nvenc", "h264_amf", "h264_qsv", "h264_mf", "libx264"}

	for _, encoder := range encodersToTry {
		fmt.Printf("[HLS Transcoder] Probing H.264 encoder: %s\n", encoder)

		args := []string{
			"-hwaccel", "auto",
			"-fflags", "+nobuffer+genpts",
			"-f", "mpegts",
			"-i", "pipe:0",
		}

		if encoder == "libx264" {
			args = append(args,
				"-c:v", "libx264",
				"-preset", "ultrafast",
				"-tune", "zerolatency",
				"-profile:v", "baseline",
				"-level", "3.0",
			)
		} else {
			args = append(args, "-c:v", encoder)
			if encoder == "h264_nvenc" {
				args = append(args, "-preset", "p1", "-tune", "ull")
			}
			if encoder == "h264_qsv" {
				args = append(args, "-preset", "veryfast")
			}
		}

		args = append(args,
			"-threads", "0",
			"-vf", "bwdif=0:-1:0,scale=-2:720:flags=fast_bilinear",
			"-pix_fmt", "yuv420p",
			"-g", "30",
			"-keyint_min", "30",
			"-sc_threshold", "0",
			"-c:a", "aac",
			"-ac", "2",
			"-b:a", "128k",
			"-ar", "44100",
			"-f", "hls",
			"-hls_time", "2",
			"-hls_list_size", "5",
			"-hls_flags", "delete_segments+append_list",
			"-hls_segment_filename", filepath.Join(tempDir, "segment_%03d.ts"),
			filepath.Join(tempDir, "stream.m3u8"),
		)

		trialCmd := exec.CommandContext(ctx, "ffmpeg", args...)

		trialStdin, err := trialCmd.StdinPipe()
		if err != nil {
			continue
		}

		trialStderr, err := trialCmd.StderrPipe()
		if err != nil {
			trialStdin.Close()
			continue
		}

		if err := trialCmd.Start(); err != nil {
			trialStdin.Close()
			trialStderr.Close()
			continue
		}

		exitChan := make(chan error, 1)
		go func() {
			exitChan <- trialCmd.Wait()
		}()

		select {
		case err := <-exitChan:
			fmt.Printf("[HLS Transcoder] Encoder %s unsupported or exited immediately: %v\n", encoder, err)
			trialStdin.Close()
			trialStderr.Close()
			continue
		case <-time.After(300 * time.Millisecond):
			fmt.Printf("[HLS Transcoder] Successfully initialized encoder: %s\n", encoder)
			cmd = trialCmd
			stdin = trialStdin
			stderr = trialStderr
			selectedEncoder = encoder

			go func() {
				defer stdin.Close()
				defer resp.Body.Close()
				buf := make([]byte, 128*1024)
				_, _ = io.CopyBuffer(stdin, resp.Body, buf)
			}()

			go func() {
				err := <-exitChan
				fmt.Printf("[HLS Transcoder] Encoder %s stopped: %v\n", selectedEncoder, err)
			}()
			break
		}
		if cmd != nil {
			break
		}
	}

	if cmd == nil {
		sess.Cancel()
		writeError(w, http.StatusInternalServerError, "all H.264 video encoders failed to initialize")
		return
	}
	sess.Cmd = cmd

	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				fmt.Printf("[FFMPEG-HLS] %s", string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()

	// Wait for the m3u8 file to be created before returning
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(filepath.Join(tempDir, "stream.m3u8")); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hls_url": fmt.Sprintf("/api/streams/hls/%s/stream.m3u8", id),
	})
}

// ServeHLSSegments serves the requested HLS m3u8 playlist or ts segment
func ServeHLSSegments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	file := chi.URLParam(r, "*")

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		sess.LastAccessed = time.Now()
	}
	hlsSessionsMu.Unlock()

	if !exists {
		http.Error(w, "stream session not found or expired", http.StatusNotFound)
		return
	}

	// Disable caching for the playlist and chunks
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	filePath := filepath.Join(sess.Dir, file)
	http.ServeFile(w, r, filePath)
}
