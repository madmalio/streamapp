package models

import "time"

// Playlist represents an IPTV playlist source (M3U or Xtream API connection).
type Playlist struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	URLPath              string    `json:"url_path"`
	Type                 string    `json:"type"` // "M3U" or "Xtream"
	CreatedAt            time.Time `json:"created_at"`
	CreatedFromFavorites bool      `json:"created_from_favorites"`
}

type EpgSource struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// ChannelGroup represents a category grouping of channels.
type ChannelGroup struct {
	ID         string `json:"id"`
	PlaylistID string `json:"playlist_id"`
	Name       string `json:"name"`
}

type Channel struct {
	ID              string `json:"id"`
	PlaylistID      string `json:"playlist_id"`
	GroupID         string `json:"group_id"`
	Name            string `json:"name"`
	StreamURL       string `json:"stream_url"`
	LogoURL         string `json:"logo_url"`
	ChannelNumber   int    `json:"channel_number"`
	GuideNumber     string `json:"guide_number"`
	IsHidden        bool   `json:"is_hidden"`
	IsFavorite      bool   `json:"is_favorite"`
	SourceChannelID string `json:"source_channel_id"`
}

// EPGProgram represents a program guide listing for a channel.
type EPGProgram struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	PosterURL   string    `json:"poster_url"`
}
