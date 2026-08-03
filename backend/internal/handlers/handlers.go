package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"log"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	rows, err := database.DB.Query("SELECT id, name, url_path, type, created_at, created_from_favorites FROM playlists ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	playlists := []models.Playlist{}
	for rows.Next() {
		var p models.Playlist
		var createdFromFavoritesRaw interface{}
		if err := rows.Scan(&p.ID, &p.Name, &p.URLPath, &p.Type, &p.CreatedAt, &createdFromFavoritesRaw); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		p.CreatedFromFavorites = parseSQLiteBool(createdFromFavoritesRaw)
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

	query := "SELECT c.id, c.playlist_id, cg.name, c.name, c.stream_url, c.logo_url, c.channel_number, c.guide_number, c.is_hidden, c.is_favorite FROM channels c LEFT JOIN channel_groups cg ON c.group_id = cg.id WHERE 1=1"
	args := []interface{}{}

	if playlistID != "" {
		query += " AND c.playlist_id = ?"
		args = append(args, playlistID)
	}
	if groupID != "" {
		query += " AND c.group_id = ?"
		args = append(args, groupID)
	}
	if search != "" {
		query += " AND c.name LIKE ?"
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
		var isFavoriteRaw interface{}
		if err := rows.Scan(&c.ID, &c.PlaylistID, &groupIDOpt, &c.Name, &c.StreamURL, &logoURLOpt, &c.ChannelNumber, &guideNumOpt, &isHiddenRaw, &isFavoriteRaw); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		c.GroupID = groupIDOpt.String
		c.LogoURL = logoURLOpt.String
		c.GuideNumber = guideNumOpt.String
		c.IsHidden = parseSQLiteBool(isHiddenRaw)
		c.IsFavorite = parseSQLiteBool(isFavoriteRaw)
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

// ReorderChannels handles bulk updating of channel numbers for a given playlist.
func ReorderChannels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelIDs []string `json:"channel_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tx, err := database.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to start transaction")
		return
	}
	defer tx.Rollback()

	// Update channel_number sequentially based on the array order (starting at 1)
	for i, id := range req.ChannelIDs {
		_, err := tx.Exec("UPDATE channels SET channel_number = ? WHERE id = ?", i+1, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update channel number: "+err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to commit transaction")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// UpdateChannelMetadata handles manual overrides of a channel's metadata.
func UpdateChannelMetadata(w http.ResponseWriter, r *http.Request) {
	chanID := chi.URLParam(r, "id")
	if chanID == "" {
		writeError(w, http.StatusBadRequest, "Missing channel ID")
		return
	}

	var req struct {
		Name          string `json:"name"`
		ChannelNumber int    `json:"channel_number"`
		GuideNumber   string `json:"guide_number"`
		GroupID       string `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Check if group exists, if not, create it on the fly
	if req.GroupID != "" {
		var existingGroupID string
		err := database.DB.QueryRow("SELECT id FROM channel_groups WHERE id = ?", req.GroupID).Scan(&existingGroupID)
		if err != nil { // Group ID doesn't exist, meaning the user passed a category name (e.g. "Movies") instead of an ID.
			// Let's create a custom group for this channel's playlist
			var pID string
			database.DB.QueryRow("SELECT playlist_id FROM channels WHERE id = ?", chanID).Scan(&pID)
			
			if pID != "" {
				// See if there's already a group with this NAME in the playlist
				err = database.DB.QueryRow("SELECT id FROM channel_groups WHERE playlist_id = ? AND name = ?", pID, req.GroupID).Scan(&existingGroupID)
				if err != nil { // Still doesn't exist, create it
					newID := uuid.New().String()
					database.DB.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)", newID, pID, req.GroupID)
					req.GroupID = newID
				} else {
					req.GroupID = existingGroupID
				}
			}
		}
	}

	_, err := database.DB.Exec("UPDATE channels SET name = ?, channel_number = ?, guide_number = ?, group_id = ? WHERE id = ?", 
		req.Name, req.ChannelNumber, req.GuideNumber, req.GroupID, chanID)
	
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Database error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
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

// UpdateChannelFavorite handles toggling a channel's favorite status.
func UpdateChannelFavorite(w http.ResponseWriter, r *http.Request) {
	chanID := chi.URLParam(r, "id")
	if chanID == "" {
		writeError(w, http.StatusBadRequest, "Missing channel ID")
		return
	}

	var req struct {
		IsFavorite bool `json:"is_favorite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	isFavoriteInt := 0
	if req.IsFavorite {
		isFavoriteInt = 1
	}

	res, err := database.DB.Exec("UPDATE channels SET is_favorite = ? WHERE id = ?", isFavoriteInt, chanID)
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

var hlsURIAttrRegex = regexp.MustCompile(`URI="([^"]+)"`)

// getBestHlsVariant fetches an HLS master playlist, parses it, and returns the variant URL with the highest BANDWIDTH.
func getBestHlsVariant(masterUrl string) string {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")
			return nil
		},
	}

	req, err := http.NewRequest("GET", masterUrl, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(body), "\n")
	var bestUrl string
	var maxBandwidth int

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			parts := strings.Split(line, ",")
			bw := 0
			for _, p := range parts {
				if strings.HasPrefix(p, "BANDWIDTH=") || strings.Contains(p, "BANDWIDTH=") {
					bwStr := strings.TrimPrefix(p, "BANDWIDTH=")
					if idx := strings.Index(bwStr, "="); idx != -1 {
						bwStr = bwStr[idx+1:]
					}
					if parsed, err := strconv.Atoi(bwStr); err == nil {
						bw = parsed
					}
				}
			}
			if i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if next != "" && !strings.HasPrefix(next, "#") {
					if bw >= maxBandwidth {
						maxBandwidth = bw
						bestUrl = next
					}
				}
			}
		}
	}

	if bestUrl != "" {
		if !strings.HasPrefix(bestUrl, "http") {
			base, err := url.Parse(resp.Request.URL.String())
			if err == nil {
				ref, err := url.Parse(bestUrl)
				if err == nil {
					return base.ResolveReference(ref).String()
				}
			}
		}
		return bestUrl
	}
	return ""
}

// ProxyM3U8 acts as a lightweight proxy for HLS master playlists.
// It fetches the remote playlist and strips out any SUBTITLES lines,
// ensuring the client player never attempts to load broken subtitle tracks.
func ProxyM3U8(w http.ResponseWriter, r *http.Request) {
	targetUrl := r.URL.Query().Get("url")
	if targetUrl == "" {
		writeError(w, http.StatusBadRequest, "url parameter is required")
		return
	}
	preferBest := strings.EqualFold(r.URL.Query().Get("best"), "1") ||
		strings.EqualFold(r.URL.Query().Get("best"), "true")

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")
			return nil
		},
	}

	req, err := http.NewRequest("GET", targetUrl, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create request")
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")

	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch playlist")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read playlist")
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !isLikelyHLSPlaylist(body, contentType) {
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}

	baseURL, err := url.Parse(resp.Request.URL.String())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve playlist base URL")
		return
	}

	// Parse and clean the playlist
	lines := strings.Split(string(body), "\n")
	var cleanedLines []string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		// Skip dedicated subtitle variant entries entirely
		if strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE=SUBTITLES") {
			continue
		}

		line = rewritePlaylistURIAttributes(line, baseURL)

		// If it's a stream definition, strip the SUBTITLES="subs" parameter
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			parts := strings.Split(line, ",")
			var newParts []string
			for _, p := range parts {
				if !strings.HasPrefix(strings.TrimSpace(p), "SUBTITLES=") {
					newParts = append(newParts, p)
				}
			}
			line = strings.Join(newParts, ",")
			
			// We MUST convert relative variant URLs to absolute URLs because
			// the client is reading this playlist from our localhost proxy, not the remote server!
			cleanedLines = append(cleanedLines, line)
			if i+1 < len(lines) {
				nextLine := strings.TrimSpace(lines[i+1])
				if nextLine != "" && !strings.HasPrefix(nextLine, "#") {
					nextLine = resolvePlaylistURL(baseURL, nextLine)
					cleanedLines = append(cleanedLines, nextLine)
					i++ // Skip the next line in the outer loop since we just processed it
				}
			}
			continue
		}

		if !strings.HasPrefix(line, "#") {
			line = resolvePlaylistURL(baseURL, line)
		}

		// Pass through everything else
		cleanedLines = append(cleanedLines, line)
	}

	if preferBest {
		maxBandwidth := -1
		bestInfo := ""
		bestURL := ""
		baseLines := make([]string, 0, len(cleanedLines))

		for i := 0; i < len(cleanedLines); i++ {
			line := cleanedLines[i]
			if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
				bandwidth := 0
				for _, part := range strings.Split(line, ",") {
					piece := strings.TrimSpace(part)
					if strings.HasPrefix(piece, "BANDWIDTH=") {
						bwStr := strings.TrimPrefix(piece, "BANDWIDTH=")
						if parsed, err := strconv.Atoi(bwStr); err == nil {
							bandwidth = parsed
						}
						break
					}
				}

				if i+1 < len(cleanedLines) {
					next := strings.TrimSpace(cleanedLines[i+1])
					if next != "" && !strings.HasPrefix(next, "#") {
						if bandwidth >= maxBandwidth {
							maxBandwidth = bandwidth
							bestInfo = line
							bestURL = next
						}
						i++
						continue
					}
				}
			}

			if !strings.HasPrefix(line, "#EXT-X-MEDIA:") {
				baseLines = append(baseLines, line)
			}
		}

		if bestInfo != "" && bestURL != "" {
			cleanedLines = append(baseLines, bestInfo, bestURL)
		}
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(strings.Join(cleanedLines, "\n") + "\n"))
}

func rewritePlaylistURIAttributes(line string, baseURL *url.URL) string {
	if baseURL == nil || !strings.Contains(line, "URI=") {
		return line
	}

	return hlsURIAttrRegex.ReplaceAllStringFunc(line, func(match string) string {
		parts := hlsURIAttrRegex.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		resolved := resolvePlaylistURL(baseURL, parts[1])
		return fmt.Sprintf("URI=\"%s\"", resolved)
	})
}

func resolvePlaylistURL(baseURL *url.URL, value string) string {
	if baseURL == nil {
		return value
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return value
	}

	if parsed.IsAbs() || strings.HasPrefix(trimmed, "data:") {
		return trimmed
	}

	return baseURL.ResolveReference(parsed).String()
}

func isLikelyHLSPlaylist(body []byte, contentType string) bool {
	if strings.Contains(contentType, "application/vnd.apple.mpegurl") ||
		strings.Contains(contentType, "application/x-mpegurl") ||
		strings.Contains(contentType, "audio/mpegurl") ||
		strings.Contains(contentType, "application/mpegurl") {
		return true
	}

	trimmed := bytes.TrimSpace(body)
	return bytes.HasPrefix(trimmed, []byte("#EXTM3U"))
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
	ID         string
	LogoURL    string
	IsHidden   bool
	IsFavorite bool
}

func getExistingChannelState(pID string) (map[string]chanState, error) {
	rows, err := database.DB.Query("SELECT id, name, logo_url, is_hidden, is_favorite FROM channels WHERE playlist_id = ?", pID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	state := make(map[string]chanState)
	for rows.Next() {
		var id, name string
		var logoOpt sql.NullString
		var isHiddenRaw, isFavoriteRaw interface{}
		if err := rows.Scan(&id, &name, &logoOpt, &isHiddenRaw, &isFavoriteRaw); err == nil {
			state[name] = chanState{
				ID:         id,
				LogoURL:    logoOpt.String,
				IsHidden:   parseSQLiteBool(isHiddenRaw),
				IsFavorite: parseSQLiteBool(isFavoriteRaw),
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
		ch.IsFavorite = existing.IsFavorite
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
	case "VIRTUAL":
		return nil // Virtual tuners don't sync from external sources
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
	_, _ = tx.Exec("DELETE FROM channels WHERE playlist_id = ?", pID)

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

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden, is_favorite) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
		isFavoriteInt := 0
		if ch.IsFavorite {
			isFavoriteInt = 1
		}
		uniqueChID := pID + "-" + ch.ID
		_, err = stmt.Exec(uniqueChID, pID, gID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber, ch.GuideNumber, isHiddenInt, isFavoriteInt)
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
	_, _ = tx.Exec("DELETE FROM channels WHERE playlist_id = ?", pID)

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

	chanStmt, err := tx.Prepare("INSERT OR REPLACE INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden, is_favorite) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
		isFavoriteInt := 0
		if ch.IsFavorite {
			isFavoriteInt = 1
		}
		uniqueChID := pID + "-" + ch.ID
		_, err = chanStmt.Exec(uniqueChID, pID, ch.GroupID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber, ch.GuideNumber, isHiddenInt, isFavoriteInt)
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
	_, _ = tx.Exec("DELETE FROM channels WHERE playlist_id = ?", pID)

	groupMap := make(map[string]string)
	for _, ch := range channels {
		gName := ch.GroupID
		if gName == "" {
			gName = "HDHomeRun"
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

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, is_hidden, is_favorite) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ch := range channels {
		applyExistingState(&ch, state)
		gName := ch.GroupID
		if gName == "" {
			gName = "HDHomeRun"
		}
		gID := groupMap[gName]

		isHiddenInt := 0
		if ch.IsHidden {
			isHiddenInt = 1
		}
		isFavoriteInt := 0
		if ch.IsFavorite {
			isFavoriteInt = 1
		}
		uniqueChID := pID + "-" + ch.ID
		_, err = stmt.Exec(uniqueChID, pID, gID, ch.Name, ch.StreamURL, ch.LogoURL, ch.ChannelNumber, ch.GuideNumber, isHiddenInt, isFavoriteInt)
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
			
			// Check if response is gzipped (by URL extension or Content-Encoding header)
			var reader io.Reader = resp.Body
			if strings.HasSuffix(xmltvURL, ".gz") || resp.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(resp.Body)
				if err != nil {
					return fmt.Errorf("failed to decompress gzip: %w", err)
				}
				defer gz.Close()
				reader = gz
			}
			r = reader
		} else {
			return fmt.Errorf("EPG URL must start with http or https")
		}
	}

	// Fetch channels for matching (exclude virtual channels - they inherit EPG via source_channel_id)
	rows, err := database.DB.Query("SELECT id, name, channel_number, guide_number, logo_url FROM channels WHERE source_channel_id = ''")
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
	categoryUpdateMap := make(map[string]string)

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

			// Try to automatically fix the category using rich EPG display names
			if xmlChan != nil {
				for _, dn := range xmlChan.DisplayName {
					cat := parser.SmartCategorize(dn, "")
					if cat != "Other" {
						categoryUpdateMap[matchedChanID] = cat
						break
					}
				}
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

	for chanID, catName := range categoryUpdateMap {
		var pID string
		err := tx.QueryRow("SELECT playlist_id FROM channels WHERE id = ?", chanID).Scan(&pID)
		if err == nil && pID != "" {
			var gID string
			err = tx.QueryRow("SELECT id FROM channel_groups WHERE playlist_id = ? AND name = ?", pID, catName).Scan(&gID)
			if err != nil {
				gID = uuid.New().String()
				tx.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)", gID, pID, catName)
			}
			tx.Exec("UPDATE channels SET group_id = ? WHERE id = ?", gID, chanID)
		}
	}

	// Copy EPG data from source channels to virtual channels
	virtualRows, err := tx.Query("SELECT id, source_channel_id FROM channels WHERE source_channel_id != ''")
	if err == nil {
		defer virtualRows.Close()
		for virtualRows.Next() {
			var virtualID, sourceID string
			if err := virtualRows.Scan(&virtualID, &sourceID); err == nil {
				// Delete old EPG for this virtual channel
				tx.Exec("DELETE FROM epg_programs WHERE channel_id = ?", virtualID)
				// Copy all EPG programs from source to virtual
				sourceEpgRows, err := tx.Query(`
					SELECT source_id, title, description, start_time, end_time, poster_url
					FROM epg_programs WHERE channel_id = ?
				`, sourceID)
				if err == nil {
					for sourceEpgRows.Next() {
						var srcID, title, description, startTime, endTime string
						var posterURL sql.NullString
						if sourceEpgRows.Scan(&srcID, &title, &description, &startTime, &endTime, &posterURL) == nil {
							newID := uuid.New().String()
							tx.Exec(`
								INSERT INTO epg_programs (id, source_id, channel_id, title, description, start_time, end_time, poster_url)
								VALUES (?, ?, ?, ?, ?, ?, ?, ?)
							`, newID, srcID, virtualID, title, description, startTime, endTime, posterURL.String)
						}
					}
					sourceEpgRows.Close()
				}
			}
		}
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
		writeError(w, http.StatusBadRequest, "Stream URL is empty")
		return
	}

	// For external HLS streams (e.g. Pluto TV), ffmpeg defaults to the first variant (often 360p).
	// We parse the master playlist and extract the highest-bandwidth variant URL to force 1080p.
	if strings.Contains(streamURL, ".m3u8") && !strings.Contains(streamURL, "192.168.") {
		if bestUrl := getBestHlsVariant(streamURL); bestUrl != "" {
			streamURL = bestUrl
		}
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

	// Create unique ID based on URL, bitrate, and transmux to prevent collisions
	hashInput := fmt.Sprintf("%s-%s-%t", streamURL, bitrate, transmux)
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

	if transmux {
		// Transmux mode: copy source codecs into HLS-TS segments (no re-encode).
		args = []string{
			"-user_agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			"-fflags", "+genpts",
			"-err_detect", "ignore_err",
			"-analyzeduration", analyzeDuration,
			"-probesize", probeSize,
			"-i", streamURL,
			"-sn",
			"-c:v", "copy",
			"-c:a", "aac",
			"-b:a", "128k",
			"-ac", "2",
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
			"-user_agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			"-fflags", "+genpts",
			"-err_detect", "ignore_err",
			"-analyzeduration", analyzeDuration,
			"-probesize", probeSize,
			"-i", streamURL,
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

	stderr, err := cmd.StderrPipe()
	if err == nil {
		go func() {
			buf := make([]byte, 2048)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					tag := "[FFMPEG-HLS]"
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
