package handlers

import (
	"context"
	"crypto/sha256"
	"log"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	// 2 Megabytes
	size := 2 * 1024 * 1024
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
	sessionTimeout := 30 * time.Second
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

// UpdatePlaylist updates a playlist's properties and resyncs channels.
func UpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	pID := chi.URLParam(r, "id")

	var req PlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req.Type = strings.ToUpper(req.Type)

	_, err := database.DB.Exec(
		"UPDATE playlists SET name = ?, url_path = ? WHERE id = ?",
		req.Name, req.URLPath, pID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update playlist: "+err.Error())
		return
	}

	// Trigger resync with new URL
	if err := syncPlaylistSource(pID, req.URLPath, req.Type, req.Username, req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed channels sync after update: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Playlist updated and channels resynced successfully",
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

// EPG Source Handlers

type EpgSourceRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func GetEpgSources(w http.ResponseWriter, r *http.Request) {
	rows, err := database.DB.Query("SELECT id, name, url, created_at FROM epg_sources ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch EPG sources")
		return
	}
	defer rows.Close()

	sources := []models.EpgSource{}
	for rows.Next() {
		var s models.EpgSource
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.CreatedAt); err != nil {
			continue
		}
		sources = append(sources, s)
	}
	writeJSON(w, http.StatusOK, sources)
}

func AddEpgSource(w http.ResponseWriter, r *http.Request) {
	var req EpgSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	sID := uuid.New().String()
	_, err := database.DB.Exec(
		"INSERT INTO epg_sources (id, name, url) VALUES (?, ?, ?)",
		sID, req.Name, req.URL,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to add EPG source: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"source_id": sID,
	})
}

func UpdateEpgSource(w http.ResponseWriter, r *http.Request) {
	sID := chi.URLParam(r, "id")
	var req EpgSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	_, err := database.DB.Exec(
		"UPDATE epg_sources SET name = ?, url = ? WHERE id = ?",
		req.Name, req.URL, sID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update EPG source: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func DeleteEpgSource(w http.ResponseWriter, r *http.Request) {
	sID := chi.URLParam(r, "id")
	_, err := database.DB.Exec("DELETE FROM epg_sources WHERE id = ?", sID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete EPG source")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func SyncEpgSourceHandler(w http.ResponseWriter, r *http.Request) {
	sID := chi.URLParam(r, "id")
	var url string
	err := database.DB.QueryRow("SELECT url FROM epg_sources WHERE id = ?", sID).Scan(&url)
	if err != nil {
		writeError(w, http.StatusNotFound, "EPG source not found")
		return
	}

	// Use existing syncEPGSource logic
	err = syncEPGSource(url, sID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to parse EPG: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "EPG synced successfully"})
}

// GetChannels returns all channels, optionally filtered by playlist_id.
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

	query := "SELECT id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden FROM channels WHERE 1=1"
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
		var guideNumOpt sql.NullString
		var isHiddenRaw interface{}
		if err := rows.Scan(&c.ID, &c.PlaylistID, &groupIDOpt, &c.Name, &c.StreamURL, &logoURLOpt, &c.ChannelNumber, &guideNumOpt, &isHiddenRaw); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		c.GroupID = groupIDOpt.String
		c.LogoURL = logoURLOpt.String
		c.GuideNumber = guideNumOpt.String
		c.IsHidden = parseSQLiteBool(isHiddenRaw)
		channels = append(channels, c)
	}

	// Sort channels: channel_number ASC, then parse guide_number for major/minor, then name
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].ChannelNumber != channels[j].ChannelNumber {
			return channels[i].ChannelNumber < channels[j].ChannelNumber
		}
		g1 := channels[i].GuideNumber
		g2 := channels[j].GuideNumber
		if g1 != "" && g2 != "" {
			parts1 := strings.Split(strings.ReplaceAll(g1, "-", "."), ".")
			parts2 := strings.Split(strings.ReplaceAll(g2, "-", "."), ".")
			for k := 0; k < len(parts1) && k < len(parts2); k++ {
				num1, err1 := strconv.Atoi(parts1[k])
				num2, err2 := strconv.Atoi(parts2[k])
				if err1 == nil && err2 == nil {
					if num1 != num2 {
						return num1 < num2
					}
				} else {
					if parts1[k] != parts2[k] {
						return parts1[k] < parts2[k]
					}
				}
			}
			if len(parts1) != len(parts2) {
				return len(parts1) < len(parts2)
			}
		}
		return channels[i].Name < channels[j].Name
	})

	writeJSON(w, http.StatusOK, channels)
}

// UpdateChannelLogo handles manual overrides of a channel's logo.
func UpdateChannelLogo(w http.ResponseWriter, r *http.Request) {
	chanID := chi.URLParam(r, "id")
	if chanID == "" {
		writeError(w, http.StatusBadRequest, "Missing channel ID")
		return
	}

	var req struct {
		LogoURL string `json:"logo_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	_, err := database.DB.Exec("UPDATE channels SET logo_url = ? WHERE id = ?", req.LogoURL, chanID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// UpdateChannelVisibility handles toggling a channel's visibility.
func UpdateChannelVisibility(w http.ResponseWriter, r *http.Request) {
	chanID := chi.URLParam(r, "id")
	if chanID == "" {
		writeError(w, http.StatusBadRequest, "Missing channel ID")
		return
	}

	var req struct {
		IsHidden bool `json:"is_hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	isHiddenInt := 0
	if req.IsHidden {
		isHiddenInt = 1
	}

	res, err := database.DB.Exec("UPDATE channels SET is_hidden = ? WHERE id = ?", isHiddenInt, chanID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database error: "+err.Error())
		return
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "Channel not found or stale ID")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// StartEPGAutoSync runs an infinite loop in the background to automatically
// sync all configured EPG sources periodically.
// SyncEPGHandler parses and syncs EPG data from an XMLTV URL.
func SyncEPGHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, http.StatusBadRequest, "Invalid request body. URL is required")
		return
	}

	if err := syncEPGSource(req.URL, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "EPG Sync failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "EPG XMLTV data synced successfully",
	})
}

func StartEPGAutoSync() {
	// Initial delay so we don't bog down server startup
	time.Sleep(5 * time.Minute)

	for {
		log.Println("[EPG Auto-Sync] Starting background sync for all sources...")
		rows, err := database.DB.Query("SELECT id, url FROM epg_sources")
		if err != nil {
			log.Printf("[EPG Auto-Sync] Error querying epg_sources: %v\n", err)
		} else {
			type EpgSrc struct {
				ID  string
				URL string
			}
			var sources []EpgSrc
			for rows.Next() {
				var src EpgSrc
				if err := rows.Scan(&src.ID, &src.URL); err == nil {
					sources = append(sources, src)
				}
			}
			rows.Close()

			for _, s := range sources {
				log.Printf("[EPG Auto-Sync] Syncing source: %s\n", s.URL)
				if err := syncEPGSource(s.URL, s.ID); err != nil {
					log.Printf("[EPG Auto-Sync] Failed to sync %s: %v\n", s.URL, err)
				}
			}
			log.Println("[EPG Auto-Sync] Background sync complete.")
		}

		// Sleep for 12 hours before the next sync
		time.Sleep(12 * time.Hour)
	}
}

// GetLiveEPG retrieves the program listing for active channels for the next 4 hours.
func GetLiveEPG(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	futureStr := now.Add(4 * time.Hour).Format(time.RFC3339)

	query := `
		SELECT id, channel_id, title, description, start_time, end_time, poster_url 
		FROM epg_programs 
		WHERE end_time > ? AND start_time < ?
		ORDER BY channel_id, start_time ASC`

	rows, err := database.DB.Query(query, nowStr, futureStr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type ProgramDetails struct {
		ID          string    `json:"id"`
		Title       string    `json:"title"`
		Description string    `json:"description"`
		StartTime   time.Time `json:"start_time"`
		EndTime     time.Time `json:"end_time"`
		PosterURL   string    `json:"poster_url"`
	}

	type ChannelEPG struct {
		Programs []ProgramDetails `json:"programs"`
	}

	epgMap := make(map[string]*ChannelEPG)

	for rows.Next() {
		var id, chanID string
		var p ProgramDetails
		var posterOpt sql.NullString
		if err := rows.Scan(&id, &chanID, &p.Title, &p.Description, &p.StartTime, &p.EndTime, &posterOpt); err == nil {
			p.PosterURL = posterOpt.String
			p.ID = id
			if entry, exists := epgMap[chanID]; exists {
				entry.Programs = append(entry.Programs, p)
			} else {
				epgMap[chanID] = &ChannelEPG{
					Programs: []ProgramDetails{p},
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, epgMap)
}

// GetCurrentProgram retrieves the currently playing program for a specific channel.
func GetCurrentProgram(w http.ResponseWriter, r *http.Request) {
	chanID := chi.URLParam(r, "id")
	if chanID == "" {
		writeError(w, http.StatusBadRequest, "Channel ID is required")
		return
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)

	query := `
		SELECT id, title, description, start_time, end_time, poster_url 
		FROM epg_programs 
		WHERE channel_id = ? AND end_time > ? AND start_time <= ?
		ORDER BY start_time DESC LIMIT 1`

	var p models.EPGProgram
	var posterOpt sql.NullString
	var startStr, endStr string

	err := database.DB.QueryRow(query, chanID, nowStr, nowStr).Scan(&p.ID, &p.Title, &p.Description, &startStr, &endStr, &posterOpt)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "No active program found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	p.ChannelID = chanID
	p.PosterURL = posterOpt.String
	p.StartTime, _ = time.Parse(time.RFC3339, startStr)
	p.EndTime, _ = time.Parse(time.RFC3339, endStr)

	writeJSON(w, http.StatusOK, p)
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

func parseSQLiteBool(val interface{}) bool {
	switch v := val.(type) {
	case bool:
		return v
	case int64:
		return v > 0
	case int32:
		return v > 0
	case int:
		return v > 0
	case float64:
		return v > 0
	case []byte:
		return string(v) == "1" || string(v) == "true"
	case string:
		return v == "1" || v == "true"
	default:
		return false
	}
}

type chanState struct {
	ID       string
	LogoURL  string
	IsHidden bool
}

func getExistingChannelState(pID string) (map[string]chanState, error) {
	rows, err := database.DB.Query("SELECT id, name, logo_url, is_hidden FROM channels WHERE playlist_id = ?", pID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	state := make(map[string]chanState)
	for rows.Next() {
		var id, name string
		var logoOpt sql.NullString
		var isHiddenRaw interface{}
		if err := rows.Scan(&id, &name, &logoOpt, &isHiddenRaw); err == nil {
			state[name] = chanState{
				ID:       id,
				LogoURL:  logoOpt.String,
				IsHidden: parseSQLiteBool(isHiddenRaw),
			}
		}
	}
	return state, nil
}

func applyExistingState(ch *models.Channel, state map[string]chanState) {
	if existing, ok := state[ch.Name]; ok {
		ch.ID = existing.ID
		ch.LogoURL = existing.LogoURL
		ch.IsHidden = existing.IsHidden
	}
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

	state, _ := getExistingChannelState(pID)

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

	stmt, err := tx.Prepare("INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ch := range channels {
		applyExistingState(&ch, state)
		gName := ch.GroupID
		if gName == "" {
			gName = "Uncategorized"
		}
		gID := groupMap[gName]
		isHiddenInt := 0
		if ch.IsHidden {
			isHiddenInt = 1
		}
		_, err = stmt.Exec(ch.ID, pID, gID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber, ch.GuideNumber, isHiddenInt)
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

	state, _ := getExistingChannelState(pID)

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

	chanStmt, err := tx.Prepare("INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer chanStmt.Close()

	for _, ch := range channels {
		applyExistingState(&ch, state)
		isHiddenInt := 0
		if ch.IsHidden {
			isHiddenInt = 1
		}
		_, err = chanStmt.Exec(ch.ID, pID, ch.GroupID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber, ch.GuideNumber, isHiddenInt)
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

	state, _ := getExistingChannelState(pID)

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

	stmt, err := tx.Prepare("INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ch := range channels {
		applyExistingState(&ch, state)
		isHiddenInt := 0
		if ch.IsHidden {
			isHiddenInt = 1
		}
		_, err = stmt.Exec(ch.ID, pID, gID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber, ch.GuideNumber, isHiddenInt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func syncEPGSource(xmltvURL string, sourceID string) error {
	var r io.Reader
	isHDHomeRunAuto := (xmltvURL == "HDHOMERUN_AUTO")

	if !isHDHomeRunAuto {
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
	}

	// Fetch channels for matching
	rows, err := database.DB.Query("SELECT id, name, channel_number, guide_number, logo_url FROM channels")
	if err != nil {
		return err
	}
	defer rows.Close()

	channelMap := make(map[string]string)
	guideNumMap := make(map[string]string)
	idToName := make(map[string]string)
	idToLogo := make(map[string]string)
	for rows.Next() {
		var id, name string
		var chnoOpt sql.NullInt64
		var guideNumOpt sql.NullString
		var logoOpt sql.NullString
		if err := rows.Scan(&id, &name, &chnoOpt, &guideNumOpt, &logoOpt); err == nil {
			channelMap[strings.ToLower(name)] = id
			if guideNumOpt.Valid && guideNumOpt.String != "" {
				guideNumMap[guideNumOpt.String] = id
			}
			idToName[id] = name
			if logoOpt.Valid {
				idToLogo[id] = logoOpt.String
			}
		}
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear old guide data for this specific source
	_, err = tx.Exec("DELETE FROM epg_programs WHERE source_id = ?", sourceID)
	if err != nil {
		return fmt.Errorf("failed to clear old epg_programs: %w", err)
	}

	stmt, err := tx.Prepare("INSERT INTO epg_programs (id, source_id, channel_id, title, description, start_time, end_time, poster_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	logoMap := make(map[string]string)

	callback := func(prog models.EPGProgram, xmlChan *parser.XMLTVChannel) error {
		epgChanID := strings.ToLower(prog.ChannelID)
		var matchedChanID string

		if id, exists := channelMap[epgChanID]; exists {
			matchedChanID = id
		} else {
			if xmlChan != nil {
				// Try guide number match first
				for _, dn := range xmlChan.DisplayName {
					if id, exists := guideNumMap[dn]; exists {
						matchedChanID = id
						break
					}
				}

				if matchedChanID == "" {
					for _, dn := range xmlChan.DisplayName {
						dnLower := strings.ToLower(dn)
						if id, exists := channelMap[dnLower]; exists {
							matchedChanID = id
							break
						}
					}
				}
				
				// Fallback: contains match for display name (e.g. '1.6 JTV' matching 'JTV')
				if matchedChanID == "" {
					for _, dn := range xmlChan.DisplayName {
						dnLower := strings.ToLower(strings.TrimSpace(dn))
						for dbName, dbID := range channelMap {
							if dbName == "" || len(dbName) < 2 { continue }
							// Check if the display name contains the DB name as a distinct word
							if strings.Contains(dnLower, dbName) || strings.Contains(dbName, dnLower) {
								matchedChanID = dbID
								break
							}
						}
						if matchedChanID != "" {
							break
						}
					}
				}
			}

			// Fallback 2: Loose match on XML channel ID
			if matchedChanID == "" {
				for name, id := range channelMap {
					if name != "" && len(name) > 2 && (strings.Contains(epgChanID, name) || strings.Contains(name, epgChanID)) {
						matchedChanID = id
						break
					}
				}
			}
		}

		if matchedChanID != "" {
			existingLogo := idToLogo[matchedChanID]
			isOverride := existingLogo != "" && !strings.Contains(existingLogo, "hdhomerun") && !strings.Contains(existingLogo, "silicondust") && !strings.Contains(existingLogo, "githubusercontent")

			if !isOverride {
				hasSiliconDust := false
				if xmlChan != nil && len(xmlChan.Icon) > 0 && xmlChan.Icon[0].Src != "" {
					logoMap[matchedChanID] = xmlChan.Icon[0].Src
					hasSiliconDust = true
				}

				if !hasSiliconDust {
					dbName := idToName[matchedChanID]
					callsign := strings.ToLower(dbName)
					callsign = strings.ReplaceAll(callsign, "-hd", "")
					callsign = strings.ReplaceAll(callsign, "-dt", "")
					callsign = strings.ReplaceAll(callsign, " ", "")
					logoMap[matchedChanID] = fmt.Sprintf("https://raw.githubusercontent.com/tv-logo/tv-logos/main/countries/united-states/%s.png", callsign)
				}
			} else {
				logoMap[matchedChanID] = existingLogo
			}

			progID := uuid.New().String()
			_, err = stmt.Exec(progID, sourceID, matchedChanID, prog.Title, prog.Description, prog.StartTime.UTC().Format(time.RFC3339), prog.EndTime.UTC().Format(time.RFC3339), prog.PosterURL)
			if err != nil {
				return fmt.Errorf("constraint error on channel_id=%s: %w", matchedChanID, err)
			}
			return nil
		}

		return nil
	}

	if isHDHomeRunAuto {
		var hdhrIP string
		_ = database.DB.QueryRow("SELECT url_path FROM playlists WHERE type = 'HDHOMERUN' LIMIT 1").Scan(&hdhrIP)
		err = parser.FetchHDHomeRunEPG(hdhrIP, callback)
	} else {
		err = parser.ParseXMLTV(r, callback)
	}

	if err != nil {
		return err
	}

	for chanID, newLogoURL := range logoMap {
		_, _ = tx.Exec("UPDATE channels SET logo_url = ? WHERE id = ?", newLogoURL, chanID)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("tx.Commit failed: %w", err)
	}
	return nil
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

	// Determine the server IP from the request host
	serverHost := r.Host
	if strings.Contains(serverHost, ":") {
		serverHost = strings.Split(serverHost, ":")[0]
	}

	hlsSessionsMu.Lock()
	sess, exists := hlsSessions[id]
	if exists {
		if !prewarm {
			sess.IsPrewarm = false
		}
		sess.LastAccessed = time.Now()
		hlsSessionsMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"hls_url": fmt.Sprintf("http://%s:8888/hls_%s/index.m3u8", serverHost, id),
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
	if fastSwitch {
		probeSize = "500k"
		analyzeDuration = "500k"
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

		// Use SRT to push to MediaMTX
		// Highly optimized low-latency GStreamer pipeline
		defaultGstPipeline := `-e souphttpsrc location={url} is-live=true do-timestamp=true keep-alive=true blocksize=16384 ! decodebin name=dec dec. ! queue max-size-buffers={queue_buffers} max-size-time=0 max-size-bytes=0 ! videoconvert ! video/x-raw,format=NV12 ! vaapih264enc bitrate={bitrate} keyframe-period=30 max-bframes=0 rate-control=vbr quality-level=5 ! h264parse config-interval=-1 ! queue max-size-buffers={queue_buffers} ! mpegtsmux name=mux alignment=7 ! srtclientsink uri="srt://127.0.0.1:8890?streamid=publish:hls_{id}" latency=0 dec. ! queue max-size-buffers={queue_buffers} max-size-time=0 max-size-bytes=0 ! audioconvert ! audioresample ! volume volume=1.8 ! voaacenc bitrate=128000 ! aacparse ! queue max-size-buffers={queue_buffers} ! mux.`
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
		pipelineStr = strings.ReplaceAll(pipelineStr, "{id}", id)
		pipelineStr = strings.ReplaceAll(pipelineStr, "{id}", id)

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
			"-f", "rtsp", "-rtsp_transport", "tcp", "-pkt_size", "1200", fmt.Sprintf("rtsp://127.0.0.1:8554/hls_%s", id),
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
			"-vf", "sidedata=mode=delete,format=nv12,hwupload,deinterlace_vaapi=rate=frame:auto=1",
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
			"-f", "rtsp", "-rtsp_transport", "tcp", "-pkt_size", "1200", fmt.Sprintf("rtsp://127.0.0.1:8554/hls_%s", id),
		}
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

	// Wait for MediaMTX to start serving the HLS stream
	mediaMTXUrl := fmt.Sprintf("http://%s:8888/hls_%s/index.m3u8", serverHost, id)

	startupTimeout := 15 * time.Second
	deadline := time.Now().Add(startupTimeout)
	found := false
	
	// Poll localhost to avoid firewall/NAT loopback issues on the server
	pollUrl := fmt.Sprintf("http://127.0.0.1:8888/hls_%s/index.m3u8", id)
	
	for time.Now().Before(deadline) {
		resp, err := http.Get(pollUrl)
		if err == nil && resp.StatusCode == 200 {
			found = true
			resp.Body.Close()
			break
		}
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !found {
		sess.Cancel()
		writeError(w, http.StatusInternalServerError, "Failed to connect to MediaMTX HLS stream in time")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hls_url": mediaMTXUrl,
	})
}

// HeartbeatStream updates the session's LastAccessed time to keep it alive.
func HeartbeatStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	hlsSessionsMu.Lock()
	defer hlsSessionsMu.Unlock()

	sess, exists := hlsSessions[id]
	if exists {
		sess.LastAccessed = time.Now()
		writeJSON(w, http.StatusOK, map[string]string{"status": "heartbeat received"})
	} else {
		writeError(w, http.StatusNotFound, "session not found")
	}
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
