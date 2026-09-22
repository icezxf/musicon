package db

import (
"database/sql"
"fmt"
"os"
"path/filepath"

_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
return nil, err
}
dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
db, err := sql.Open("sqlite", dsn)
if err != nil {
return nil, err
}
db.SetMaxOpenConns(1)
return db, db.Ping()
}

func Migrate(db *sql.DB) error {
schema := []string{
`CREATE TABLE IF NOT EXISTS songs (
id TEXT PRIMARY KEY,
title TEXT NOT NULL DEFAULT '',
artist TEXT NOT NULL DEFAULT '',
album TEXT NOT NULL DEFAULT '',
album_artist TEXT NOT NULL DEFAULT '',
fmt TEXT NOT NULL DEFAULT '',
dur INTEGER NOT NULL DEFAULT 0,
path TEXT NOT NULL DEFAULT '',
plays INTEGER NOT NULL DEFAULT 0,
provider_id TEXT NOT NULL DEFAULT '',
cover_path TEXT NOT NULL DEFAULT '',
cover_art TEXT NOT NULL DEFAULT '',
genre TEXT NOT NULL DEFAULT '',
lyrics TEXT NOT NULL DEFAULT '',
created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE INDEX IF NOT EXISTS idx_songs_artist ON songs(artist)`,
`CREATE INDEX IF NOT EXISTS idx_songs_album ON songs(album)`,
`CREATE INDEX IF NOT EXISTS idx_songs_path ON songs(path)`,
`CREATE TABLE IF NOT EXISTS playlists (
id TEXT PRIMARY KEY,
name TEXT NOT NULL,
comment TEXT NOT NULL DEFAULT '',
owner TEXT NOT NULL DEFAULT 'admin',
public INTEGER NOT NULL DEFAULT 0,
is_readonly INTEGER NOT NULL DEFAULT 0,
scope TEXT NOT NULL DEFAULT 'private_user',
song_count INTEGER NOT NULL DEFAULT 0,
duration INTEGER NOT NULL DEFAULT 0,
created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS playlist_tracks (
id INTEGER PRIMARY KEY AUTOINCREMENT,
playlist_id TEXT NOT NULL,
song_id TEXT NOT NULL,
sort_order INTEGER NOT NULL DEFAULT 0,
created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE UNIQUE INDEX IF NOT EXISTS idx_pl_track_unique ON playlist_tracks(playlist_id, song_id)`,
`CREATE TABLE IF NOT EXISTS play_history (
id INTEGER PRIMARY KEY AUTOINCREMENT,
song_id TEXT NOT NULL,
source TEXT NOT NULL DEFAULT 'subsonic',
submission INTEGER NOT NULL DEFAULT 0,
client TEXT NOT NULL DEFAULT '',
played_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS scan_tasks (
id TEXT PRIMARY KEY,
path TEXT NOT NULL DEFAULT '',
mode TEXT NOT NULL DEFAULT 'full',
count INTEGER NOT NULL DEFAULT 0,
total INTEGER NOT NULL DEFAULT 0,
processed INTEGER NOT NULL DEFAULT 0,
status TEXT NOT NULL DEFAULT 'pending',
source TEXT NOT NULL DEFAULT '',
created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS subsonic_users (
username TEXT PRIMARY KEY,
password TEXT NOT NULL,
role TEXT NOT NULL DEFAULT 'user',
enabled INTEGER NOT NULL DEFAULT 1,
created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS artist_cache (
name TEXT PRIMARY KEY,
pic TEXT NOT NULL DEFAULT '',
bio TEXT NOT NULL DEFAULT '',
source TEXT NOT NULL DEFAULT '',
updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS app_settings (
key TEXT PRIMARY KEY,
value TEXT NOT NULL DEFAULT '',
updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS web_sessions (
token TEXT PRIMARY KEY,
username TEXT NOT NULL,
expires_at TEXT NOT NULL
)`,
}
for _, s := range schema {
if _, err := db.Exec(s); err != nil {
return fmt.Errorf("exec schema: %w", err)
}
}
return nil
}
