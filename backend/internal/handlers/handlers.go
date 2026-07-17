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
	"strconv"
	"strings"
	"sync"
	"time"

	"streamapp/backend/internal/database"
	"streamapp/backend/internal/models"
	"streamapp/backend/internal/parser"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SpeedTest generates a 5MB payload of random (or zeroes) bytes
// so the Flutter client can measure its connection speed.
func SpeedTest(w http.ResponseWriter, r *http.Request) {
	// 5 Megabytes
	size := 5 * 1024 * 1024
	payload := make([]byte, size)
	
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(size))
	
	// Just write zeroes. The client only cares about download time.
	w.Write(payload)
}

type HLSSession struct {
	ID           string
	Dir          string
	LastAccessed time.Time
	Cmd          *exec.Cmd
	Cancel       context.CancelFunc
	IsPrewarm    bool
	Done         chan struct{}
}

var (
	hlsSessions   = make(map[string]*HLSSession)
	hlsSessionsMu sync.Mutex
)

func init() {
	sessionTimeout := 5 * time.Minute
	if rawTimeout := strings.TrimSpace(os.Getenv("FFMPEG_HLS_SESSION_TIMEOUT_SECONDS")); rawTimeout != "" {
		if seconds, err := strconv.Atoi(rawTimeout); err == nil && seconds > 0 {
			sessionTimeout = time.Duration(seconds) * time.Second
		}
	}

	prewarmTimeout := 30 * time.Second
	if rawTimeout := strings.TrimSpace(os.Getenv("FFMPEG_HLS_PREWARM_TIMEOUT_SECONDS")); rawTimeout != "" {
		if seconds, err := strconv.Atoi(rawTimeout); err == nil && seconds > 0 {
			prewarmTimeout = time.Duration(seconds) * time.Second
		}
	}

	// Clean any orphaned HLS artifacts from prior process runs.
	os.RemoveAll(filepath.Join(os.TempDir(), "streamapp_hls"))

	go func() {
		for {
			time.Sleep(5 * time.Second)
			hlsSessionsMu.Lock()
			now := time.Now()
			for id, sess := range hlsSessions {
				timeout := sessionTimeout
				if sess.IsPrewarm {
					timeout = prewarmTimeout
				}

				if now.Sub(sess.LastAccessed) > timeout {
					fmt.Printf("[HLS Manager] Session %s timed out, cleaning up...\n", id)
					killProcessGracefully(sess.Cmd, sess.Cancel)
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

func hlsBufsizeFromBitrate(bitrate string) string {
	trimmed := strings.TrimSpace(strings.ToLower(bitrate))
	if trimmed == "" {
		return bitrate
	}

	multiplier := 1.0
	numberPart := trimmed
	unit := ""

	if strings.HasSuffix(trimmed, "m") {
		unit = "M"
		numberPart = strings.TrimSuffix(trimmed, "m")
		multiplier = 2.0
	} else if strings.HasSuffix(trimmed, "k") {
		unit = "k"
		numberPart = strings.TrimSuffix(trimmed, "k")
		multiplier = 2.0
	} else {
		value, err := strconv.ParseFloat(numberPart, 64)
		if err != nil {
			return bitrate
		}
		return strconv.FormatInt(int64(value*2), 10)
	}

	value, err := strconv.ParseFloat(numberPart, 64)
	if err != nil {
		return bitrate
	}

	return fmt.Sprintf("%g%s", value*multiplier, unit)
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

// PlayStream returns the raw HDHomeRun URL for the native app to play directly
func PlayStream(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"stream_url": streamURL,
	})
}

func killProcessGracefully(cmd *exec.Cmd, cancel context.CancelFunc) {
	if cmd == nil || cmd.Process == nil {
		if cancel != nil {
			cancel()
		}
		return
	}

	// Send SIGINT to allow graceful TCP FIN close (avoids HDHomeRun TCP RST bug).
	_ = cmd.Process.Signal(os.Interrupt)

	// Ensure the context is canceled (triggering SIGKILL) if the process hangs longer than 1s
	go func(c context.CancelFunc, p *os.Process) {
		time.Sleep(1000 * time.Millisecond)
		if c != nil {
			c()
		}
		_ = p.Kill()
	}(cancel, cmd.Process)
}

// StartHLSStream starts an HLS transcoding session with a specific bitrate cap.
func StartHLSStream(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}

	bitrate := r.URL.Query().Get("bitrate")
	if bitrate == "" {
		bitrate = "4M" // Default to 4 Mbps if not specified
	}

	fastSwitch := strings.EqualFold(r.URL.Query().Get("fast"), "1") ||
		strings.EqualFold(r.URL.Query().Get("fast"), "true")
	prewarm := strings.EqualFold(r.URL.Query().Get("prewarm"), "1") ||
		strings.EqualFold(r.URL.Query().Get("prewarm"), "true")
	transmux := strings.EqualFold(r.URL.Query().Get("transmux"), "1") ||
		strings.EqualFold(r.URL.Query().Get("transmux"), "true")
	engine := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("engine")))
	if engine == "" {
		engine = "ffmpeg"
	}

	// Create unique ID based on URL, bitrate, transmux, and engine to prevent collisions
	hashInput := fmt.Sprintf("%s-%s-%t-%s", streamURL, bitrate, transmux, engine)
	hash := sha256.Sum256([]byte(hashInput))
	id := hex.EncodeToString(hash[:])[:16]

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		if !prewarm && sess.IsPrewarm {
			sess.IsPrewarm = false
		}
		sess.LastAccessed = time.Now()
		hlsSessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"hls_url": fmt.Sprintf("/api/streams/hls/%s/stream.m3u8", id),
		})
		return
	}

	tempDir := filepath.Join(os.TempDir(), "streamapp_hls", id)
	os.RemoveAll(tempDir) // CRITICAL: Purge any left-over chunks from previous crashes
	os.MkdirAll(tempDir, 0755)

	ctx, cancel := context.WithCancel(context.Background())
	sess = &HLSSession{
		ID:           id,
		Dir:          tempDir,
		LastAccessed: time.Now(),
		Cancel:       cancel,
		IsPrewarm:    prewarm,
		Done:         make(chan struct{}),
	}
	hlsSessions[id] = sess
	hlsSessionsMu.Unlock()

	playlistPath := filepath.ToSlash(filepath.Join(tempDir, "stream.m3u8"))
	segmentExt := ".m4s"
	if transmux || engine == "gstreamer" || fastSwitch {
		segmentExt = ".ts"
	}
	segmentPath := filepath.ToSlash(filepath.Join(tempDir, "segment_%05d"+segmentExt))

	vaapiDevice := os.Getenv("FFMPEG_VAAPI_DEVICE")
	if vaapiDevice == "" {
		vaapiDevice = "/dev/dri/renderD128"
	}

	bufsize := hlsBufsizeFromBitrate(bitrate)

	probeSize := "20M"
	analyzeDuration := "20M"
	hlsTime := "2"
	hlsListSize := "8"
	if fastSwitch {
		probeSize = "500k"
		analyzeDuration = "500k"
		hlsTime = "1"
		hlsListSize = "4"
	}

	binaryName := "ffmpeg"
	var args []string

	if engine == "gstreamer" {
		binaryName = "gst-launch-1.0"
		gstTime := "4"
		gstListSize := "6"
		gstQueueBuffers := "40"
		if fastSwitch {
			gstTime = "1"
			gstListSize = "4"
			gstQueueBuffers = "15"
		}

		// Convert bitrate (e.g. "4M" or "1.5M") to kbps for GStreamer's vaapih264enc (e.g. "4000" or "1500")
		gstBitrate := "4500" // Default fallback
		trimmedBitrate := strings.TrimSpace(strings.ToUpper(bitrate))
		if strings.HasSuffix(trimmedBitrate, "M") {
			numStr := strings.TrimSuffix(trimmedBitrate, "M")
			if val, err := strconv.ParseFloat(numStr, 64); err == nil {
				gstBitrate = strconv.Itoa(int(val * 1000))
			}
		} else if strings.HasSuffix(trimmedBitrate, "K") {
			numStr := strings.TrimSuffix(trimmedBitrate, "K")
			if val, err := strconv.ParseFloat(numStr, 64); err == nil {
				gstBitrate = strconv.Itoa(int(val))
			}
		} else if val, err := strconv.Atoi(trimmedBitrate); err == nil && val > 0 {
			if val < 10000 {
				gstBitrate = strconv.Itoa(val)
			} else {
				gstBitrate = strconv.Itoa(val / 1000)
			}
		}

		defaultGstPipeline := `-e souphttpsrc location={url} is-live=true do-timestamp=true keep-alive=true blocksize=16384 ! decodebin name=dec dec. ! queue max-size-buffers={queue_buffers} max-size-time=0 max-size-bytes=0 ! videoconvert ! video/x-raw,format=NV12 ! vaapih264enc bitrate={bitrate} keyframe-period=60 rate-control=vbr quality-level=5 ! h264parse config-interval=1 ! queue max-size-buffers={queue_buffers} ! hls.video dec. ! queue max-size-buffers={queue_buffers} max-size-time=0 max-size-bytes=0 ! audioconvert ! audioresample ! volume volume=1.8 ! voaacenc bitrate=128000 ! aacparse ! queue max-size-buffers={queue_buffers} ! hls.audio hlssink2 name=hls playlist-location={playlist} location={segment} target-duration={time} playlist-length={list_size} max-files=20`
		pipelineStr := os.Getenv("GSTREAMER_PIPELINE")
		if pipelineStr == "" {
			pipelineStr = defaultGstPipeline
		}

		pipelineStr = strings.ReplaceAll(pipelineStr, "{url}", streamURL)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{bitrate}", gstBitrate)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{time}", gstTime)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{list_size}", gstListSize)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{queue_buffers}", gstQueueBuffers)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{segment}", segmentPath)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{playlist}", playlistPath)

		args = strings.Fields(pipelineStr)
	} else if transmux {
		// Transmux mode: copy source codecs into HLS-TS segments (no re-encode).
		args = []string{
			"-fflags", "+genpts",
			"-err_detect", "ignore_err",
			"-analyzeduration", analyzeDuration,
			"-probesize", probeSize,
			"-i", streamURL,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-sn",
			"-c:v", "copy",
			"-c:a", "copy",
			"-f", "hls",
			"-hls_time", hlsTime,
			"-hls_list_size", hlsListSize,
			"-hls_flags", "delete_segments+append_list+omit_endlist",
			"-hls_segment_filename", segmentPath,
			playlistPath,
		}
	} else {
		// Resilient Intel VAAPI hardware pipeline for dirty OTA MPEG-TS feeds.
		gopSize := "60"
		if fastSwitch {
			gopSize = "30"
		}

		args = []string{
			"-vaapi_device", vaapiDevice,
			"-fflags", "+genpts",
			"-err_detect", "ignore_err",
			"-analyzeduration", analyzeDuration,
			"-probesize", probeSize,
			"-i", streamURL,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-sn",
			"-vf", "sidedata=mode=delete,format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			"-profile:v", "main",
			"-b:v", bitrate,
			"-maxrate", bitrate,
			"-bufsize", bufsize,
			"-bf", "0",
			"-g", gopSize,
			"-keyint_min", gopSize,
			"-fps_mode", "passthrough",
			"-af", "aresample=async=1",
			"-c:a", "aac",
			"-b:a", "128k",
			"-ac", "2",
			"-ar", "48000",
			"-f", "hls",
			"-hls_time", hlsTime,
			"-hls_list_size", hlsListSize,
		}

		if fastSwitch {
			args = append(args, "-hls_flags", "delete_segments+append_list+omit_endlist")
		} else {
			args = append(args,
				"-hls_flags", "delete_segments+append_list+independent_segments+omit_endlist",
				"-hls_segment_type", "fmp4",
				"-hls_fmp4_init_filename", "init.mp4",
			)
		}

		args = append(args,
			"-hls_segment_filename", segmentPath,
			playlistPath,
		)
	}

	cmd := exec.CommandContext(ctx, binaryName, args...)
	sess.Cmd = cmd

	if engine == "gstreamer" {
		cmd.Env = os.Environ()
		if vaapiDevice != "" {
			cmd.Env = append(cmd.Env, "GST_VAAPI_DRM_DEVICE="+vaapiDevice)
		}
	}

	stderr, err := cmd.StderrPipe()
	if err == nil {
		go func() {
			buf := make([]byte, 2048)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					tag := "[FFMPEG-HLS]"
					if engine == "gstreamer" {
						tag = "[GSTREAMER-HLS]"
					}
					fmt.Printf("%s %s", tag, string(buf[:n]))
				}
				if err != nil {
					break
				}
			}
		}()
	}

	if err := cmd.Start(); err != nil {
		sess.Cancel()
		close(sess.Done)
		writeError(w, http.StatusInternalServerError, "failed to start transcoder")
		return
	}

	go func(id string, session *HLSSession) {
		err := cmd.Wait()
		if err != nil {
			fmt.Printf("[HLS Manager] %s exited for session %s: %v\n", binaryName, id, err)
		} else {
			fmt.Printf("[HLS Manager] %s exited cleanly for session %s\n", binaryName, id)
		}

		hlsSessionsMu.Lock()
		active, exists := hlsSessions[id]
		if exists && active == session {
			delete(hlsSessions, id)
			go os.RemoveAll(session.Dir)
		}
		hlsSessionsMu.Unlock()
		close(session.Done)
	}(id, sess)

	// Wait for the m3u8 file to be created and contain at least one segment entry before returning
	startupTimeout := 60 * time.Second
	if rawTimeout := strings.TrimSpace(os.Getenv("FFMPEG_HLS_START_TIMEOUT_SECONDS")); rawTimeout != "" {
		if seconds, err := strconv.Atoi(rawTimeout); err == nil && seconds > 0 {
			startupTimeout = time.Duration(seconds) * time.Second
		}
	}
	deadline := time.Now().Add(startupTimeout)
	found := false
	for time.Now().Before(deadline) {
		playlistFile := filepath.Join(tempDir, "stream.m3u8")
		if _, err := os.Stat(playlistFile); err == nil {
			content, err := os.ReadFile(playlistFile)
			if err == nil {
				contentStr := string(content)
				if strings.Contains(contentStr, "segment_") || strings.Contains(contentStr, ".ts") || strings.Contains(contentStr, ".m4s") {
					found = true
					break
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !found {
		sess.Cancel()
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("%s failed to create stream.m3u8 in time with segments", binaryName))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hls_url": fmt.Sprintf("/api/streams/hls/%s/stream.m3u8", id),
	})
}

// StopHLSStream forces a transcoding session to terminate early.
func StopHLSStream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id parameter is required")
		return
	}

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		delete(hlsSessions, id)
	}
	hlsSessionsMu.Unlock()

	if exists {
		killProcessGracefully(sess.Cmd, sess.Cancel)
		<-sess.Done
		os.RemoveAll(sess.Dir)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "stopped"})
}

// ShutdownAllStreams cleans up all running HLS sessions.
func ShutdownAllStreams() {
	hlsSessionsMu.Lock()
	for id, sess := range hlsSessions {
		killProcessGracefully(sess.Cmd, sess.Cancel)
		<-sess.Done
		go func(dir string) {
			os.RemoveAll(dir)
		}(sess.Dir)
		delete(hlsSessions, id)
	}
	hlsSessionsMu.Unlock()

	// Brutal fallback: kill any orphaned ffmpeg or gst-launch-1.0 processes 
	// that might have leaked from previous crashes to guarantee tuner release.
	_ = exec.Command("killall", "-9", "ffmpeg", "gst-launch-1.0").Run()
}

// StopAllStreams kills all running HLS sessions. Useful for freeing up tuners quickly.
func StopAllStreams(w http.ResponseWriter, r *http.Request) {
	ShutdownAllStreams()
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "all_stopped"})
}

// ServeHLSSegments serves the m3u8 playlist and segment files generated by FFmpeg.
func ServeHLSSegments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	file := chi.URLParam(r, "*")

	if id == "" || file == "" {
		writeError(w, http.StatusBadRequest, "invalid hls path")
		return
	}

	hlsSessionsMu.Lock()
	if sess, exists := hlsSessions[id]; exists {
		sess.LastAccessed = time.Now()
	}
	hlsSessionsMu.Unlock()

	filePath := filepath.Join(os.TempDir(), "streamapp_hls", id, file)

	if strings.HasSuffix(file, ".m3u8") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(file, ".m4s") {
		w.Header().Set("Content-Type", "video/iso.segment")
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else if strings.HasSuffix(file, ".mp4") {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else if strings.HasSuffix(file, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}

	http.ServeFile(w, r, filePath)
}

// StartWebRTCStream publishes an HDHomeRun stream to a local MediaMTX instance via RTSP.
func StartWebRTCStream(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}

	hashInput := fmt.Sprintf("webrtc-%s", streamURL)
	hash := sha256.Sum256([]byte(hashInput))
	id := hex.EncodeToString(hash[:])[:16]

	// Determine the server IP from the request host
	serverHost := r.Host
	if strings.Contains(serverHost, ":") {
		serverHost = strings.Split(serverHost, ":")[0]
	}

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		sess.LastAccessed = time.Now()
		hlsSessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"webrtc_url": fmt.Sprintf("http://%s:8889/%s/whep", serverHost, id),
		})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	sess = &HLSSession{
		ID:           id,
		Dir:          "", // No dir needed for WebRTC
		LastAccessed: time.Now(),
		Cancel:       cancel,
		IsPrewarm:    false,
		Done:         make(chan struct{}),
	}
	hlsSessions[id] = sess
	hlsSessionsMu.Unlock()

	// Push the stream to MediaMTX via RTSP on port 8554
	rtspUrl := fmt.Sprintf("rtsp://127.0.0.1:8554/%s", id)

	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi",
		"-i", streamURL,
		"-vf", "deinterlace_vaapi",
		"-c:v", "h264_vaapi", "-bf", "0", "-sei", "0", "-b:v", "3M", "-maxrate", "3M", "-bufsize", "6M",
		"-c:a", "libopus", "-ac", "2",
		"-f", "rtsp", "-rtsp_transport", "tcp", "-pkt_size", "1200", rtspUrl,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	
	// Create a log file to capture why GStreamer might crash
	logFile, err := os.OpenFile(filepath.Join(os.TempDir(), "webrtc_gst.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err == nil {
		cmd.Stderr = logFile
	}
	
	sess.Cmd = cmd

	if err := cmd.Start(); err != nil {
		sess.Cancel()
		close(sess.Done)
		writeError(w, http.StatusInternalServerError, "failed to start webrtc transcoder")
		return
	}

	go func(id string, session *HLSSession) {
		err := cmd.Wait()
		if err != nil {
			fmt.Printf("[WebRTC] ffmpeg exited for session %s: %v\n", id, err)
		}

		hlsSessionsMu.Lock()
		active, exists := hlsSessions[id]
		if exists && active == session {
			delete(hlsSessions, id)
		}
		hlsSessionsMu.Unlock()
		close(session.Done)
	}(id, sess)

	// Since we don't need to wait for chunks, we can just return immediately!
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"webrtc_url": fmt.Sprintf("http://%s:8889/%s/whep", serverHost, id),
		"session_id": id,
	})
}

// StartSRTStream publishes an HDHomeRun stream to a local MediaMTX instance via SRT.
func StartSRTStream(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}

	hashInput := fmt.Sprintf("srt-%s", streamURL)
	hash := sha256.Sum256([]byte(hashInput))
	id := hex.EncodeToString(hash[:])[:16]

	serverHost := r.Host
	if strings.Contains(serverHost, ":") {
		serverHost = strings.Split(serverHost, ":")[0]
	}

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		sess.LastAccessed = time.Now()
		hlsSessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"srt_url": fmt.Sprintf("srt://%s:8890?streamid=read:%s", serverHost, id),
		})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	sess = &HLSSession{
		ID:           id,
		Dir:          "",
		LastAccessed: time.Now(),
		Cancel:       cancel,
		IsPrewarm:    false,
		Done:         make(chan struct{}),
	}
	hlsSessions[id] = sess
	hlsSessionsMu.Unlock()

	// Push the stream to MediaMTX via SRT on port 8890
	srtUrl := fmt.Sprintf("srt://127.0.0.1:8890?streamid=publish:%s", id)
	
	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-i", streamURL,
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-c:a", "aac", "-ac", "2",
		"-f", "mpegts", srtUrl,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	
	logFile, err := os.OpenFile(filepath.Join(os.TempDir(), "srt_ffmpeg.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err == nil {
		cmd.Stderr = logFile
	}
	
	sess.Cmd = cmd

	if err := cmd.Start(); err != nil {
		sess.Cancel()
		close(sess.Done)
		writeError(w, http.StatusInternalServerError, "failed to start srt transcoder")
		return
	}

	go func(id string, session *HLSSession) {
		err := cmd.Wait()
		if err != nil {
			fmt.Printf("[SRT] ffmpeg exited for session %s: %v\n", id, err)
		}

		hlsSessionsMu.Lock()
		active, exists := hlsSessions[id]
		if exists && active == session {
			delete(hlsSessions, id)
		}
		hlsSessionsMu.Unlock()
		close(session.Done)
	}(id, sess)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"srt_url": fmt.Sprintf("srt://%s:8890?streamid=read:%s", serverHost, id),
	})
}

