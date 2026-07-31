package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"streamapp/backend/internal/database"

	"github.com/google/uuid"
)

type VirtualTunerRequest struct {
	Name             string   `json:"name"`
	IncludeLocals    bool     `json:"include_locals"`
	FavoriteGenres   []string `json:"favorite_genres"`
	SelectedChannels []string `json:"selected_channels"` // Manual channel IDs to include
}

// GenerateVirtualTuner handles the creation of a DIY Cable Company
func GenerateVirtualTuner(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req VirtualTunerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = "My Custom Tuner"
	}

	db := database.DB
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// 1. Create a new virtual playlist
	playlistID := uuid.New().String()
	_, err = tx.Exec("INSERT INTO playlists (id, name, url_path, type) VALUES (?, ?, ?, ?)",
		playlistID, req.Name, "virtual://"+playlistID, "VIRTUAL")
	if err != nil {
		http.Error(w, "Failed to create virtual tuner", http.StatusInternalServerError)
		return
	}

	// Map to keep track of created channel groups: name -> id
	groupCache := make(map[string]string)

	defaultGroupID := uuid.New().String()
	_, err = tx.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)",
		defaultGroupID, playlistID, "Uncategorized")
	if err != nil {
		http.Error(w, "Failed to create default channel group", http.StatusInternalServerError)
		return
	}
	groupCache["Uncategorized"] = defaultGroupID

	// 2. Resolve channels to copy (algorithmic + manual)
	var channelsToCopy []string
	
	// Add algorithmic channels...
	// For simplicity, if they selected categories, we query channels matching those keywords in their group names
	// This would require a complex query or pulling them in memory.
	// For MVP, we will rely entirely on the SelectedChannels provided by the frontend UI wizard.
	if len(req.SelectedChannels) > 0 {
		channelsToCopy = append(channelsToCopy, req.SelectedChannels...)
	}

	// 3. Copy channels into the virtual tuner
	channelNumber := 1
	sourceToVirtualMap := make(map[string]string) // Track source channel ID -> virtual channel ID
	
	for _, sourceChanID := range channelsToCopy {
		// Fetch original channel and its group name
		row := tx.QueryRow(`
			SELECT c.name, c.stream_url, c.logo_url, cg.name 
			FROM channels c 
			LEFT JOIN channel_groups cg ON c.group_id = cg.id 
			WHERE c.id = ?`, sourceChanID)
			
		var name, streamURL, logoURL, groupName sql.NullString
		if err := row.Scan(&name, &streamURL, &logoURL, &groupName); err != nil {
			if err == sql.ErrNoRows {
				continue // Skip if not found
			}
			http.Error(w, "Database error finding source channel", http.StatusInternalServerError)
			return
		}

		gn := groupName.String
		if gn == "" {
			gn = "Uncategorized"
		}

		groupID, exists := groupCache[gn]
		if !exists {
			groupID = uuid.New().String()
			_, err = tx.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)", groupID, playlistID, gn)
			if err != nil {
				http.Error(w, "Failed to create channel group", http.StatusInternalServerError)
				return
			}
			groupCache[gn] = groupID
		}

		newChannelID := uuid.New().String()
		_, err = tx.Exec(`
			INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, source_channel_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, newChannelID, playlistID, groupID, name, streamURL, logoURL, channelNumber, "", sourceChanID)
		if err != nil {
			http.Error(w, "Failed to insert virtual channel", http.StatusInternalServerError)
			return
		}
		sourceToVirtualMap[sourceChanID] = newChannelID
		channelNumber++
	}

	// 4. Copy EPG programs from source channels to virtual channels
	epgInsertStmt, err := tx.Prepare(`
		INSERT INTO epg_programs (id, source_id, channel_id, title, description, start_time, end_time, poster_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		http.Error(w, "Failed to prepare EPG insert", http.StatusInternalServerError)
		return
	}
	defer epgInsertStmt.Close()

	for sourceChanID, virtualChanID := range sourceToVirtualMap {
		rows, err := tx.Query(`
			SELECT source_id, title, description, start_time, end_time, poster_url
			FROM epg_programs
			WHERE channel_id = ?
		`, sourceChanID)
		if err != nil {
			continue
		}

		for rows.Next() {
			var sourceID, title, description, startTime, endTime, posterURL sql.NullString
			if err := rows.Scan(&sourceID, &title, &description, &startTime, &endTime, &posterURL); err != nil {
				rows.Close()
				break
			}
			_, _ = epgInsertStmt.Exec(
				uuid.New().String(),
				sourceID.String,
				virtualChanID,
				title.String,
				description.String,
				startTime.String,
				endTime.String,
				posterURL.String,
			)
		}
		rows.Close()
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"message":     "Virtual tuner created successfully",
		"playlist_id": playlistID,
	})
}

type FavoritesTunerRequest struct {
	Name string `json:"name"`
}

// CreateFromFavorites creates or updates a virtual tuner from favorited channels
func CreateFromFavorites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req FavoritesTunerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = "My Favorites"
	}

	db := database.DB

	// Check if a favorites-based virtual tuner already exists
	var existingPlaylistID string
	err := db.QueryRow("SELECT id FROM playlists WHERE created_from_favorites = 1 LIMIT 1").Scan(&existingPlaylistID)
	if err != nil && err != sql.ErrNoRows {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var playlistID string

	if existingPlaylistID != "" {
		// Update existing tuner: delete old channels and groups
		playlistID = existingPlaylistID
		_, err = tx.Exec("DELETE FROM channels WHERE playlist_id = ?", playlistID)
		if err != nil {
			http.Error(w, "Failed to delete old channels", http.StatusInternalServerError)
			return
		}
		_, err = tx.Exec("DELETE FROM channel_groups WHERE playlist_id = ?", playlistID)
		if err != nil {
			http.Error(w, "Failed to delete old channel groups", http.StatusInternalServerError)
			return
		}
	} else {
		// Create new tuner
		playlistID = uuid.New().String()
		_, err = tx.Exec("INSERT INTO playlists (id, name, url_path, type, created_from_favorites) VALUES (?, ?, ?, ?, ?)",
			playlistID, req.Name, "virtual://"+playlistID, "VIRTUAL", 1)
		if err != nil {
			http.Error(w, "Failed to create virtual tuner", http.StatusInternalServerError)
			return
		}
	}

	// Query all favorited channels
	rows, err := tx.Query(`
		SELECT c.id, c.name, c.stream_url, c.logo_url, c.guide_number, cg.name
		FROM channels c
		LEFT JOIN channel_groups cg ON c.group_id = cg.id
		WHERE c.is_favorite = 1
		ORDER BY c.channel_number ASC, c.name ASC
	`)
	if err != nil {
		http.Error(w, "Failed to query favorites", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type FavoriteChannel struct {
		ID        string
		Name      string
		StreamURL string
		LogoURL   string
		GuideNum  string
		GroupName string
	}

	var favorites []FavoriteChannel
	for rows.Next() {
		var fc FavoriteChannel
		var groupName sql.NullString
		if err := rows.Scan(&fc.ID, &fc.Name, &fc.StreamURL, &fc.LogoURL, &fc.GuideNum, &groupName); err != nil {
			http.Error(w, "Failed to scan favorite", http.StatusInternalServerError)
			return
		}
		fc.GroupName = groupName.String
		if fc.GroupName == "" {
			fc.GroupName = "Uncategorized"
		}
		favorites = append(favorites, fc)
	}

	if len(favorites) == 0 {
		http.Error(w, "No favorited channels found", http.StatusBadRequest)
		return
	}

	// Map to keep track of created channel groups: name -> id
	groupCache := make(map[string]string)

	// Create channel groups
	for _, fc := range favorites {
		if _, exists := groupCache[fc.GroupName]; !exists {
			groupID := uuid.New().String()
			_, err = tx.Exec("INSERT INTO channel_groups (id, playlist_id, name) VALUES (?, ?, ?)", groupID, playlistID, fc.GroupName)
			if err != nil {
				http.Error(w, "Failed to create channel group", http.StatusInternalServerError)
				return
			}
			groupCache[fc.GroupName] = groupID
		}
	}

	// Copy channels into the virtual tuner
	channelNumber := 1
	sourceToVirtualMap := make(map[string]string)

	for _, fc := range favorites {
		groupID := groupCache[fc.GroupName]
		newChannelID := uuid.New().String()
		_, err = tx.Exec(`
			INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, guide_number, source_channel_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, newChannelID, playlistID, groupID, fc.Name, fc.StreamURL, fc.LogoURL, channelNumber, fc.GuideNum, fc.ID)
		if err != nil {
			http.Error(w, "Failed to insert virtual channel", http.StatusInternalServerError)
			return
		}
		sourceToVirtualMap[fc.ID] = newChannelID
		channelNumber++
	}

	// Copy EPG programs from source channels to virtual channels
	epgInsertStmt, err := tx.Prepare(`
		INSERT INTO epg_programs (id, source_id, channel_id, title, description, start_time, end_time, poster_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		http.Error(w, "Failed to prepare EPG insert", http.StatusInternalServerError)
		return
	}
	defer epgInsertStmt.Close()

	for sourceChanID, virtualChanID := range sourceToVirtualMap {
		epgRows, err := tx.Query(`
			SELECT source_id, title, description, start_time, end_time, poster_url
			FROM epg_programs
			WHERE channel_id = ?
		`, sourceChanID)
		if err != nil {
			continue
		}

		for epgRows.Next() {
			var sourceID, title, description, startTime, endTime, posterURL sql.NullString
			if err := epgRows.Scan(&sourceID, &title, &description, &startTime, &endTime, &posterURL); err != nil {
				break
			}
			_, _ = epgInsertStmt.Exec(
				uuid.New().String(),
				sourceID.String,
				virtualChanID,
				title.String,
				description.String,
				startTime.String,
				endTime.String,
				posterURL.String,
			)
		}
		epgRows.Close()
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	action := "created"
	if existingPlaylistID != "" {
		action = "updated"
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Virtual tuner " + action + " successfully",
		"playlist_id": playlistID,
		"channel_count": len(favorites),
	})
}
