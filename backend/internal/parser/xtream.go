package parser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"streamapp/backend/internal/models"
)

// XtreamCategory is the category object returned by the player_api.php query.
type XtreamCategory struct {
	CategoryID   string `json:"category_id"`
	CategoryName string `json:"category_name"`
}

// XtreamStream is the live stream object returned by player_api.php.
type XtreamStream struct {
	Num          int    `json:"num"`
	Name         string `json:"name"`
	StreamID     int    `json:"stream_id"`
	StreamIcon   string `json:"stream_icon"`
	EPGChannelID string `json:"epg_channel_id"`
	CategoryID   string `json:"category_id"`
}

// FetchXtream connects to the Xtream Codes server and retrieves all categories and channel streams.
func FetchXtream(baseURL, username, password string) ([]models.ChannelGroup, []models.Channel, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}

	// Normalise path to player_api.php
	u.Path = "/player_api.php"

	// Construct category URL
	catQuery := url.Values{}
	catQuery.Set("username", username)
	catQuery.Set("password", password)
	catQuery.Set("action", "get_live_categories")
	u.RawQuery = catQuery.Encode()

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to fetch categories: status %d", resp.StatusCode)
	}

	var xtreamCats []XtreamCategory
	if err := json.NewDecoder(resp.Body).Decode(&xtreamCats); err != nil {
		return nil, nil, err
	}

	// Create groups list
	var groups []models.ChannelGroup
	groupMap := make(map[string]string) // Maps CategoryID to GroupID UUID
	for _, xc := range xtreamCats {
		gID := uuid.New().String()
		groups = append(groups, models.ChannelGroup{
			ID:   gID,
			Name: xc.CategoryName,
		})
		groupMap[xc.CategoryID] = gID
	}

	// Construct live streams list URL
	streamQuery := url.Values{}
	streamQuery.Set("username", username)
	streamQuery.Set("password", password)
	streamQuery.Set("action", "get_live_streams")
	u.RawQuery = streamQuery.Encode()

	respStreams, err := client.Get(u.String())
	if err != nil {
		return nil, nil, err
	}
	defer respStreams.Body.Close()

	if respStreams.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to fetch streams: status %d", respStreams.StatusCode)
	}

	var xtreamStreams []XtreamStream
	if err := json.NewDecoder(respStreams.Body).Decode(&xtreamStreams); err != nil {
		return nil, nil, err
	}

	// Format base for stream URLs: http://domain:port/live/username/password/{stream_id}.ts
	streamBase := fmt.Sprintf("%s://%s/live/%s/%s", u.Scheme, u.Host, username, password)

	var channels []models.Channel
	for _, xs := range xtreamStreams {
		streamURL := fmt.Sprintf("%s/%d.ts", streamBase, xs.StreamID)
		gID := groupMap[xs.CategoryID]

		channels = append(channels, models.Channel{
			ID:            uuid.New().String(),
			GroupID:       gID,
			Name:          xs.Name,
			StreamURL:     streamURL,
			LogoURL:       xs.StreamIcon,
			ChannelNumber: xs.Num,
		})
	}

	return groups, channels, nil
}
