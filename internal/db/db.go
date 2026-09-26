package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS songs (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			artist TEXT NOT NULL DEFAULT '',
			album TEXT NOT NULL DEFAULT '',
			album_artist TEXT NOT NULL DEFAULT '',
			genre TEXT NOT NULL DEFAULT '',
			fmt TEXT NOT NULL DEFAULT '',
			dur INTEGER NOT NULL DEFAULT 0,
			path TEXT NOT NULL DEFAULT '',
			provider_id TEXT NOT NULL DEFAULT '',
			cover_path TEXT NOT NULL DEFAULT '',
			cover_art TEXT NOT NULL DEFAULT '',
			lyrics TEXT NOT NULL DEFAULT '',
			plays INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS playlists (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			comment TEXT NOT NULL DEFAULT '',
			owner TEXT NOT NULL DEFAULT '',
			public INTEGER NOT NULL DEFAULT 0,
			is_readonly INTEGER NOT NULL DEFAULT 0,
			song_count INTEGER NOT NULL DEFAULT 0,
			duration INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS playlist_tracks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			playlist_id TEXT NOT NULL,
			song_id TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			UNIQUE(playlist_id, song_id)
		)`,
		`CREATE TABLE IF NOT EXISTS scan_tasks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			count INTEGER NOT NULL DEFAULT 0,
			total INTEGER NOT NULL DEFAULT 0,
			processed INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS subsonic_users (
			username TEXT PRIMARY KEY,
			password TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS web_sessions (
			token TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			expires_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS artist_cache (
			name TEXT PRIMARY KEY,
			pic TEXT NOT NULL DEFAULT '',
			bio TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS play_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			song_id TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			client TEXT NOT NULL DEFAULT '',
			played_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		// 音源表
		`CREATE TABLE IF NOT EXISTS sources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			path TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT (datetime('now', 'localtime'))
		)`,
		// 新增：音源路径表（一个音源可挂多条路径）
		`CREATE TABLE IF NOT EXISTS source_paths (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			path TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT (datetime('now', 'localtime'))
		)`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}

	alters := []string{
		"ALTER TABLE songs ADD COLUMN track_number INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN disc_number INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN year INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN composer TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE songs ADD COLUMN bitrate INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN channels INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN isrc TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE songs ADD COLUMN bpm INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN source_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE scan_tasks ADD COLUMN source_id TEXT NOT NULL DEFAULT ''",
	}
	for _, a := range alters {
		if _, err := db.Exec(a); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("migrate %q: %w", a, err)
			}
		}
	}
	return nil
}