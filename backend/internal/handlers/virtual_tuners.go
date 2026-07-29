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
			INSERT INTO channels (id, playlist_id, group_id, name, stream_url, logo_url, channel_number, source_channel_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, newChannelID, playlistID, groupID, name, streamURL, logoURL, channelNumber, sourceChanID)
		if err != nil {
			http.Error(w, "Failed to insert virtual channel", http.StatusInternalServerError)
			return
		}
		channelNumber++
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
