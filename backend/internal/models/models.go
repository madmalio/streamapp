package models

import "time"

// Playlist represents an IPTV playlist source (M3U or Xtream API connection).
type Playlist struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URLPath   string    `json:"url_path"`
	Type      string    `json:"type"` // "M3U" or "Xtream"
	CreatedAt time.Time `json:"created_at"`
}

// ChannelGroup represents a category grouping of channels.
type ChannelGroup struct {
	ID         string `json:"id"`
	PlaylistID string `json:"playlist_id"`
	Name       string `json:"name"`
}

// Channel represents an individual streamable channel.
type Channel struct {
	ID            string `json:"id"`
	GroupID       string `json:"group_id"`
	Name          string `json:"name"`
	StreamURL     string `json:"stream_url"`
	LogoURL       string `json:"logo_url"`
	ChannelNumber int    `json:"channel_number"`
}

// EPGProgram represents a program guide listing for a channel.
type EPGProgram struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
}
