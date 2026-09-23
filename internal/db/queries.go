package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Holder struct {
	DB *sql.DB
}

type Song struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"album_artist"`
	Genre       string `json:"genre"`
	Fmt         string `json:"fmt"`
	Dur         int    `json:"dur"`
	Path        string `json:"path"`
	ProviderID  string `json:"provider_id"`
	CoverPath   string `json:"cover_path"`
	CoverArt    string `json:"cover_art"`
	Lyrics      string `json:"lyrics"`
	Plays       int    `json:"plays"`
	TrackNumber int    `json:"track_number"`
	DiscNumber  int    `json:"disc_number"`
	Year        int    `json:"year"`
	Composer    string `json:"composer"`
	Bitrate     int    `json:"bitrate"`
	SampleRate  int    `json:"sample_rate"`
	Channels    int    `json:"channels"`
	FileSize    int64  `json:"file_size"`
	ISRC        string `json:"isrc"`
	BPM         int    `json:"bpm"`
}

const songColumns = `id,title,artist,album,album_artist,genre,fmt,dur,path,provider_id,cover_path,cover_art,lyrics,plays,track_number,disc_number,year,composer,bitrate,sample_rate,channels,file_size,isrc,bpm`

func scanSong(rows interface{ Scan(...any) error }) (Song, error) {
	var s Song
	err := rows.Scan(
		&s.ID, &s.Title, &s.Artist, &s.Album, &s.AlbumArtist, &s.Genre, &s.Fmt,
		&s.Dur, &s.Path, &s.ProviderID, &s.CoverPath, &s.CoverArt, &s.Lyrics, &s.Plays,
		&s.TrackNumber, &s.DiscNumber, &s.Year, &s.Composer,
		&s.Bitrate, &s.SampleRate, &s.Channels, &s.FileSize, &s.ISRC, &s.BPM,
	)
	return s, err
}

func (h *Holder) UpsertSong(s Song) error {
	_, err := h.DB.Exec(`
		INSERT INTO songs (
			id,title,artist,album,album_artist,genre,fmt,dur,path,provider_id,
			cover_path,cover_art,lyrics,track_number,disc_number,year,composer,
			bitrate,sample_rate,channels,file_size,isrc,bpm
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, artist=excluded.artist, album=excluded.album,
			album_artist=excluded.album_artist, genre=excluded.genre, fmt=excluded.fmt,
			dur=excluded.dur, path=excluded.path, provider_id=excluded.provider_id,
			cover_path=excluded.cover_path, cover_art=excluded.cover_art,
			lyrics=CASE WHEN excluded.lyrics!='' THEN excluded.lyrics ELSE songs.lyrics END,
			track_number=excluded.track_number, disc_number=excluded.disc_number,
			year=excluded.year, composer=excluded.composer,
			bitrate=excluded.bitrate, sample_rate=excluded.sample_rate,
			channels=excluded.channels, file_size=excluded.file_size,
			isrc=excluded.isrc, bpm=excluded.bpm,
			updated_at=CURRENT_TIMESTAMP
	`, s.ID, s.Title, s.Artist, s.Album, s.AlbumArtist, s.Genre, s.Fmt, s.Dur,
		s.Path, s.ProviderID, s.CoverPath, s.CoverArt, s.Lyrics,
		s.TrackNumber, s.DiscNumber, s.Year, s.Composer,
		s.Bitrate, s.SampleRate, s.Channels, s.FileSize, s.ISRC, s.BPM)
	return err
}

func (h *Holder) ListSongs() ([]Song, error) {
	rows, err := h.DB.Query(`SELECT ` + songColumns + ` FROM songs ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Song
	for rows.Next() {
		s, err := scanSong(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (h *Holder) GetSong(id string) (*Song, error) {
	row := h.DB.QueryRow(`SELECT `+songColumns+` FROM songs WHERE id=?`, id)
	s, err := scanSong(row)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (h *Holder) CountSongs() int {
	var n int
	h.DB.QueryRow(`SELECT COUNT(*) FROM songs`).Scan(&n)
	return n
}

type Playlist struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Comment  string `json:"comment"`
	Owner    string `json:"owner"`
	Public   bool   `json:"public"`
	Readonly bool   `json:"is_readonly"`
	Count    int    `json:"song_count"`
	Duration int    `json:"duration"`
}

func (h *Holder) ListPlaylists() ([]Playlist, error) {
	rows, err := h.DB.Query(`SELECT id,name,comment,owner,public,is_readonly,song_count,duration FROM playlists ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		var p Playlist
		rows.Scan(&p.ID, &p.Name, &p.Comment, &p.Owner, &p.Public, &p.Readonly, &p.Count, &p.Duration)
		out = append(out, p)
	}
	return out, nil
}

func (h *Holder) GetPlaylist(id string) (*Playlist, error) {
	row := h.DB.QueryRow(`SELECT id,name,comment,owner,public,is_readonly,song_count,duration FROM playlists WHERE id=?`, id)
	var p Playlist
	if err := row.Scan(&p.ID, &p.Name, &p.Comment, &p.Owner, &p.Public, &p.Readonly, &p.Count, &p.Duration); err != nil {
		return nil, err
	}
	return &p, nil
}

func (h *Holder) GetPlaylistSongs(id string) ([]Song, error) {
	rows, err := h.DB.Query(`
		SELECT `+songColumns+`
		FROM playlist_tracks pt JOIN songs s ON s.id = pt.song_id
		WHERE pt.playlist_id=? ORDER BY pt.sort_order, pt.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Song
	for rows.Next() {
		s, err := scanSong(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (h *Holder) CreatePlaylist(name, comment, owner string) (string, error) {
	id := newShortID("pl")
	_, err := h.DB.Exec(`INSERT INTO playlists(id,name,comment,owner) VALUES(?,?,?,?)`, id, name, comment, owner)
	return id, err
}

func (h *Holder) DeletePlaylist(id string) error {
	h.DB.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=?`, id)
	_, err := h.DB.Exec(`DELETE FROM playlists WHERE id=?`, id)
	return err
}

func (h *Holder) AddSongsToPlaylist(plID string, songIDs []string) error {
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	tx.QueryRow(`SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id=?`, plID).Scan(&n)
	for i, sid := range songIDs {
		tx.Exec(`INSERT OR IGNORE INTO playlist_tracks(playlist_id,song_id,sort_order) VALUES(?,?,?)`, plID, sid, n+i)
	}
	tx.Exec(`UPDATE playlists SET song_count=(SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id=?), updated_at=CURRENT_TIMESTAMP WHERE id=?`, plID, plID)
	return tx.Commit()
}

func (h *Holder) RemoveSongsFromPlaylist(plID string, songIDs []string) error {
	if len(songIDs) == 0 {
		return nil
	}
	ph := strings.Repeat("?,", len(songIDs))
	ph = ph[:len(ph)-1]
	args := []any{plID}
	for _, s := range songIDs {
		args = append(args, s)
	}
	_, err := h.DB.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=? AND song_id IN (`+ph+`)`, args...)
	if err != nil {
		return err
	}
	h.DB.Exec(`UPDATE playlists SET song_count=(SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id=?) WHERE id=?`, plID, plID)
	return nil
}

func (h *Holder) RecordPlay(songID, source, client string) {
	h.DB.Exec(`INSERT INTO play_history(song_id,source,client) VALUES(?,?,?)`, songID, source, client)
	h.DB.Exec(`UPDATE songs SET plays=plays+1 WHERE id=?`, songID)
}

func (h *Holder) RecentPlays(limit int) ([]map[string]any, error) {
	rows, err := h.DB.Query(`
		SELECT ph.id, ph.song_id, ph.played_at, s.title, s.artist
		FROM play_history ph JOIN songs s ON s.id=ph.song_id
		ORDER BY ph.played_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int
		var sid, playedAt, title, artist string
		rows.Scan(&id, &sid, &playedAt, &title, &artist)
		out = append(out, map[string]any{"id": id, "song_id": sid, "played_at": playedAt, "title": title, "artist": artist})
	}
	return out, nil
}

func (h *Holder) CreateScanTask(path, mode, source string) (string, error) {
	id := newShortID("scan")
	_, err := h.DB.Exec(`INSERT INTO scan_tasks(id,path,mode,source,status) VALUES(?,?,?,?, 'running')`, id, path, mode, source)
	return id, err
}

func (h *Holder) SetTaskProgress(id string, total, processed int) {
	if id == "" {
		return
	}
	h.DB.Exec(`UPDATE scan_tasks SET total=?, processed=? WHERE id=?`, total, processed, id)
}

func (h *Holder) SetTaskStatus(id, status string) {
	h.DB.Exec(`UPDATE scan_tasks SET status=? WHERE id=?`, status, id)
}

func (h *Holder) ListScanTasks(limit int) ([]map[string]any, error) {
	rows, err := h.DB.Query(`SELECT id,path,mode,count,total,processed,status,source,created_at FROM scan_tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, path, mode, status, source, created string
		var count, total, processed int
		rows.Scan(&id, &path, &mode, &count, &total, &processed, &status, &source, &created)
		out = append(out, map[string]any{
			"id": id, "path": path, "mode": mode, "count": count,
			"total": total, "processed": processed, "status": status,
			"source": source, "created_at": created,
		})
	}
	return out, nil
}

func newShortID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
