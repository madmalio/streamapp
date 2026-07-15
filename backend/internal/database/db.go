package database

import (
	"database/sql"
	"log"

	_ "modernc.org/sqlite"
)

// DB is the global SQLite connection handle
var DB *sql.DB

// InitDB opens the database connection, configures it, and creates the required tables.
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Enable WAL journal mode for concurrent read/write and enforce foreign keys
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, err
	}

	// Define table creation queries
	queries := []string{
		`CREATE TABLE IF NOT EXISTS playlists (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			url_path TEXT NOT NULL,
			type TEXT NOT NULL, -- M3U, Xtream, HDHomeRun
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS channel_groups (
			id TEXT PRIMARY KEY,
			playlist_id TEXT NOT NULL,
			name TEXT NOT NULL,
			FOREIGN KEY (playlist_id) REFERENCES playlists(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS channels (
			id TEXT PRIMARY KEY,
			playlist_id TEXT NOT NULL,
			group_id TEXT,
			name TEXT NOT NULL,
			stream_url TEXT NOT NULL,
			logo_url TEXT,
			channel_number INTEGER DEFAULT 0,
			FOREIGN KEY (playlist_id) REFERENCES playlists(id) ON DELETE CASCADE,
			FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS epg_programs (
			id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			start_time DATETIME NOT NULL,
			end_time DATETIME NOT NULL,
			FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE
		);`,
		// Indexes for optimizing query operations
		`CREATE INDEX IF NOT EXISTS idx_channels_playlist ON channels(playlist_id);`,
		`CREATE INDEX IF NOT EXISTS idx_channels_group ON channels(group_id);`,
		`CREATE INDEX IF NOT EXISTS idx_epg_channel_time ON epg_programs(channel_id, start_time, end_time);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}

	DB = db
	log.Println("SQLite database initialized successfully at", dbPath)
	return db, nil
}
