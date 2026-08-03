package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"streamapp/backend/internal/database"
	"streamapp/backend/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetCameras returns all cameras ordered by sort_order.
func GetCameras(w http.ResponseWriter, r *http.Request) {
	rows, err := database.DB.Query("SELECT id, name, rtsp_url, location, is_enabled, sort_order, created_at FROM cameras ORDER BY sort_order ASC, created_at ASC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	cameras := []models.Camera{}
	for rows.Next() {
		var c models.Camera
		var isEnabledRaw interface{}
		if err := rows.Scan(&c.ID, &c.Name, &c.RTSPUrl, &c.Location, &isEnabledRaw, &c.SortOrder, &c.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		c.IsEnabled = parseSQLiteBool(isEnabledRaw)
		cameras = append(cameras, c)
	}

	writeJSON(w, http.StatusOK, cameras)
}

// AddCamera creates a new camera entry after validating the RTSP URL.
func AddCamera(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		RTSPUrl  string `json:"rtsp_url"`
		Location string `json:"location"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" || req.RTSPUrl == "" {
		writeError(w, http.StatusBadRequest, "Name and RTSP URL are required")
		return
	}

	// Validate RTSP URL format
	if err := validateRTSPUrl(req.RTSPUrl); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid RTSP URL: %v", err))
		return
	}

	// Get max sort_order
	var maxOrder sql.NullInt64
	_ = database.DB.QueryRow("SELECT MAX(sort_order) FROM cameras").Scan(&maxOrder)
	nextOrder := 0
	if maxOrder.Valid {
		nextOrder = int(maxOrder.Int64) + 1
	}

	id := uuid.New().String()
	_, err := database.DB.Exec(
		"INSERT INTO cameras (id, name, rtsp_url, location, is_enabled, sort_order) VALUES (?, ?, ?, ?, 1, ?)",
		id, req.Name, req.RTSPUrl, req.Location, nextOrder,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"message": "Camera added successfully",
		"id":      id,
	})
}

// UpdateCamera modifies an existing camera.
func UpdateCamera(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Camera ID is required")
		return
	}

	var req struct {
		Name      string `json:"name"`
		RTSPUrl   string `json:"rtsp_url"`
		Location  string `json:"location"`
		IsEnabled *bool  `json:"is_enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate RTSP URL if provided
	if req.RTSPUrl != "" {
		if err := validateRTSPUrl(req.RTSPUrl); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid RTSP URL: %v", err))
			return
		}
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}

	if req.Name != "" {
		updates = append(updates, "name = ?")
		args = append(args, req.Name)
	}
	if req.RTSPUrl != "" {
		updates = append(updates, "rtsp_url = ?")
		args = append(args, req.RTSPUrl)
	}
	if req.Location != "" {
		updates = append(updates, "location = ?")
		args = append(args, req.Location)
	}
	if req.IsEnabled != nil {
		updates = append(updates, "is_enabled = ?")
		args = append(args, *req.IsEnabled)
	}

	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE cameras SET %s WHERE id = ?", joinUpdates(updates))

	result, err := database.DB.Exec(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeError(w, http.StatusNotFound, "Camera not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Camera updated successfully",
	})
}

// DeleteCamera removes a camera.
func DeleteCamera(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "Camera ID is required")
		return
	}

	result, err := database.DB.Exec("DELETE FROM cameras WHERE id = ?", id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeError(w, http.StatusNotFound, "Camera not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Camera deleted successfully",
	})
}

// ReorderCameras updates the sort order of cameras.
func ReorderCameras(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CameraIds []string `json:"camera_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.CameraIds) == 0 {
		writeError(w, http.StatusBadRequest, "Camera IDs are required")
		return
	}

	tx, err := database.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	for i, id := range req.CameraIds {
		_, err := tx.Exec("UPDATE cameras SET sort_order = ? WHERE id = ?", i, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Cameras reordered successfully",
	})
}

// validateRTSPUrl checks if the URL is a valid RTSP URL.
func validateRTSPUrl(rawUrl string) error {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return fmt.Errorf("invalid URL format: %v", err)
	}

	if u.Scheme != "rtsp" && u.Scheme != "rtsps" {
		return fmt.Errorf("URL must start with rtsp:// or rtsps://")
	}

	if u.Host == "" {
		return fmt.Errorf("URL must include a host")
	}

	return nil
}

// joinUpdates joins update clauses for SQL queries.
func joinUpdates(updates []string) string {
	result := ""
	for i, u := range updates {
		if i > 0 {
			result += ", "
		}
		result += u
	}
	return result
}
