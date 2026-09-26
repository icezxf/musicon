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
	SourceID    string `json:"source_id"`
}

const songColumns = `id,title,artist,album,album_artist,genre,fmt,dur,path,provider_id,cover_path,cover_art,lyrics,plays,track_number,disc_number,year,composer,bitrate,sample_rate,channels,file_size,isrc,bpm,source_id`

const songColumnsAliased = `s.id,s.title,s.artist,s.album,s.album_artist,s.genre,s.fmt,s.dur,s.path,s.provider_id,s.cover_path,s.cover_art,s.lyrics,s.plays,s.track_number,s.disc_number,s.year,s.composer,s.bitrate,s.sample_rate,s.channels,s.file_size,s.isrc,s.bpm,s.source_id`

func scanSong(rows interface{ Scan(...any) error }) (Song, error) {
	var s Song
	err := rows.Scan(
		&s.ID, &s.Title, &s.Artist, &s.Album, &s.AlbumArtist, &s.Genre, &s.Fmt,
		&s.Dur, &s.Path, &s.ProviderID, &s.CoverPath, &s.CoverArt, &s.Lyrics, &s.Plays,
		&s.TrackNumber, &s.DiscNumber, &s.Year, &s.Composer,
		&s.Bitrate, &s.SampleRate, &s.Channels, &s.FileSize, &s.ISRC, &s.BPM,
		&s.SourceID,
	)
	return s, err
}

func (h *Holder) UpsertSong(s Song) error {
	_, err := h.DB.Exec(`
		INSERT INTO songs (
			id,title,artist,album,album_artist,genre,fmt,dur,path,provider_id,
			cover_path,cover_art,lyrics,track_number,disc_number,year,composer,
			bitrate,sample_rate,channels,file_size,isrc,bpm,source_id
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, artist=excluded.artist, album=excluded.album,
			album_artist=excluded.album_artist, genre=excluded.genre, fmt=excluded.fmt,
			dur=excluded.dur, path=excluded.path, provider_id=excluded.provider_id,
			cover_path=CASE WHEN excluded.cover_path!='' THEN excluded.cover_path ELSE songs.cover_path END,
			cover_art=CASE WHEN excluded.cover_art!='' THEN excluded.cover_art ELSE songs.cover_art END,
			lyrics=CASE WHEN excluded.lyrics!='' THEN excluded.lyrics ELSE songs.lyrics END,
			track_number=excluded.track_number, disc_number=excluded.disc_number,
			year=excluded.year, composer=excluded.composer,
			bitrate=excluded.bitrate, sample_rate=excluded.sample_rate,
			channels=excluded.channels, file_size=excluded.file_size,
			isrc=excluded.isrc, bpm=excluded.bpm,
			source_id=CASE WHEN excluded.source_id!='' THEN excluded.source_id ELSE songs.source_id END,
			updated_at=CURRENT_TIMESTAMP
	`, s.ID, s.Title, s.Artist, s.Album, s.AlbumArtist, s.Genre, s.Fmt, s.Dur,
		s.Path, s.ProviderID, s.CoverPath, s.CoverArt, s.Lyrics,
		s.TrackNumber, s.DiscNumber, s.Year, s.Composer,
		s.Bitrate, s.SampleRate, s.Channels, s.FileSize, s.ISRC, s.BPM,
		s.SourceID)
	return err
}

func (h *Holder) ListSongs() ([]Song, error) {
	rows, err := h.DB.Query(`SELECT ` + songColumns + ` FROM songs ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Song{}
	for rows.Next() {
		s, err := scanSong(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		rows.Scan(&p.ID, &p.Name, &p.Comment, &p.Owner, &p.Public, &p.Readonly, &p.Count, &p.Duration)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
		SELECT `+songColumnsAliased+`
		FROM playlist_tracks pt JOIN songs s ON s.id = pt.song_id
		WHERE pt.playlist_id=? ORDER BY pt.sort_order, pt.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Song{}
	for rows.Next() {
		s, err := scanSong(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Holder) CreatePlaylist(name, comment, owner string, public bool) (string, error) {
	id := newShortID("pl")
	p := 0
	if public {
		p = 1
	}
	_, err := h.DB.Exec(`INSERT INTO playlists(id,name,comment,owner,public) VALUES(?,?,?,?,?)`,
		id, name, comment, owner, p)
	return id, err
}

func (h *Holder) DeletePlaylist(id string) error {
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM playlists WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Holder) AddSongsToPlaylist(plID string, songIDs []string) error {
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exist int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM playlists WHERE id=?`, plID).Scan(&exist); err != nil {
		return err
	}
	if exist == 0 {
		return fmt.Errorf("playlist not found")
	}

	var next int
	tx.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM playlist_tracks WHERE playlist_id=?`, plID).Scan(&next)
	for i, sid := range songIDs {
		tx.Exec(`INSERT OR IGNORE INTO playlist_tracks(playlist_id,song_id,sort_order) VALUES(?,?,?)`,
			plID, sid, next+i)
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
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM playlist_tracks WHERE playlist_id=? AND song_id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE playlists SET song_count=(SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id=?), updated_at=CURRENT_TIMESTAMP WHERE id=?`, plID, plID); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Holder) RecordPlay(songID, source, client string) {
	tx, err := h.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO play_history(song_id,source,client) VALUES(?,?,?)`, songID, source, client); err != nil {
		return
	}
	if _, err := tx.Exec(`UPDATE songs SET plays=plays+1 WHERE id=?`, songID); err != nil {
		return
	}
	tx.Commit()
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
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var sid, playedAt, title, artist string
		rows.Scan(&id, &sid, &playedAt, &title, &artist)
		out = append(out, map[string]any{"id": id, "song_id": sid, "played_at": playedAt, "title": title, "artist": artist})
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	out := []map[string]any{}
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func newShortID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), time.Now().UnixNano()&0xffff)
}

func (h *Holder) UpdateSong(id string, fields map[string]string) error {
	allowed := map[string]bool{
		"title": true, "artist": true, "album": true, "album_artist": true,
		"genre": true, "lyrics": true, "cover_art": true, "composer": true,
	}
	var sets []string
	args := []any{}
	for k, v := range fields {
		if !allowed[k] {
			continue
		}
		sets = append(sets, k+"=?")
		args = append(args, v)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := h.DB.Exec("UPDATE songs SET "+strings.Join(sets, ", ")+", updated_at=CURRENT_TIMESTAMP WHERE id=?", args...)
	return err
}

func (h *Holder) DeleteSongs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ph := strings.Repeat("?,", len(ids))
	ph = ph[:len(ph)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var affected []string
	if rows, err := tx.Query(`SELECT DISTINCT playlist_id FROM playlist_tracks WHERE song_id IN (`+ph+`)`, args...); err == nil {
		for rows.Next() {
			var pid string
			rows.Scan(&pid)
			affected = append(affected, pid)
		}
		rows.Close()
	}
	if _, err := tx.Exec(`DELETE FROM playlist_tracks WHERE song_id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM songs WHERE id IN (`+ph+`)`, args...); err != nil {
		return err
	}
	for _, pid := range affected {
		tx.Exec(`UPDATE playlists SET song_count=(SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id=?), updated_at=CURRENT_TIMESTAMP WHERE id=?`, pid, pid)
	}
	return tx.Commit()
}

// ============ 歌手缓存 ============

func (h *Holder) LoadArtist(name string) (string, string, string, time.Time, bool) {
	row := h.DB.QueryRow(`SELECT pic, bio, source, updated_at FROM artist_cache WHERE name=?`, name)
	var pic, bio, source, updated string
	if err := row.Scan(&pic, &bio, &source, &updated); err != nil {
		return "", "", "", time.Time{}, false
	}
	t, err := time.Parse("2006-01-02 15:04:05", updated)
	if err != nil {
		t, err = time.Parse(time.RFC3339, updated)
		if err != nil {
			return "", "", "", time.Time{}, false
		}
	}
	return pic, bio, source, t, true
}

func (h *Holder) SaveArtist(name, pic, bio, source string) error {
	if name == "" {
		return nil
	}
	_, err := h.DB.Exec(`
		INSERT INTO artist_cache(name, pic, bio, source) VALUES(?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			pic=excluded.pic, bio=excluded.bio, source=excluded.source,
			updated_at=CURRENT_TIMESTAMP
	`, name, pic, bio, source)
	return err
}

func (h *Holder) ClearArtistCache(name string) error {
	_, err := h.DB.Exec(`DELETE FROM artist_cache WHERE name=?`, name)
	return err
}

// ============ 音源 ============

type SourcePath struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	ProviderID string `json:"provider_id"`
	Path       string `json:"path"`
	SortOrder  int    `json:"sort_order"`
}

type Source struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	ProviderID string      `json:"provider_id,omitempty"`
	Path      string       `json:"path,omitempty"`
	Paths     []SourcePath `json:"paths"`
	CreatedAt string       `json:"created_at"`
}

func (h *Holder) loadSourcePaths(sourceID string) ([]SourcePath, error) {
	rows, err := h.DB.Query(`SELECT id, source_id, provider_id, path, COALESCE(sort_order,0) FROM source_paths WHERE source_id=? ORDER BY sort_order, id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourcePath{}
	for rows.Next() {
		var p SourcePath
		if err := rows.Scan(&p.ID, &p.SourceID, &p.ProviderID, &p.Path, &p.SortOrder); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (h *Holder) ListSources() ([]Source, error) {
	rows, err := h.DB.Query(`SELECT id, name, provider_id, path, COALESCE(created_at,'') FROM sources ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.Name, &s.ProviderID, &s.Path, &s.CreatedAt); err != nil {
			continue
		}
		s.Paths, _ = h.loadSourcePaths(s.ID)
		if s.Paths == nil {
			s.Paths = []SourcePath{}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Holder) GetSource(id string) (*Source, error) {
	row := h.DB.QueryRow(`SELECT id, name, provider_id, path, COALESCE(created_at,'') FROM sources WHERE id=?`, id)
	var s Source
	if err := row.Scan(&s.ID, &s.Name, &s.ProviderID, &s.Path, &s.CreatedAt); err != nil {
		return nil, err
	}
	s.Paths, _ = h.loadSourcePaths(s.ID)
	if s.Paths == nil {
		s.Paths = []SourcePath{}
	}
	return &s, nil
}

func (h *Holder) CreateSource(name string, paths []SourcePath) (string, error) {
	id := newShortID("src")
	tx, err := h.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	firstProvider, firstPath := "", ""
	if len(paths) > 0 {
		firstProvider = paths[0].ProviderID
		firstPath = paths[0].Path
	}
	if _, err := tx.Exec(`INSERT INTO sources(id,name,provider_id,path) VALUES(?,?,?,?)`,
		id, name, firstProvider, firstPath); err != nil {
		return "", err
	}

	for i, p := range paths {
		pid := newShortID("sp")
		if _, err := tx.Exec(`INSERT INTO source_paths(id,source_id,provider_id,path,sort_order) VALUES(?,?,?,?,?)`,
			pid, id, p.ProviderID, p.Path, i); err != nil {
			return "", err
		}
	}
	return id, tx.Commit()
}

func (h *Holder) UpdateSource(id, name string, paths []SourcePath) error {
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	firstProvider, firstPath := "", ""
	if len(paths) > 0 {
		firstProvider = paths[0].ProviderID
		firstPath = paths[0].Path
	}
	if _, err := tx.Exec(`UPDATE sources SET name=?, provider_id=?, path=? WHERE id=?`,
		name, firstProvider, firstPath, id); err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM source_paths WHERE source_id=?`, id); err != nil {
		return err
	}
	for i, p := range paths {
		pid := newShortID("sp")
		if _, err := tx.Exec(`INSERT INTO source_paths(id,source_id,provider_id,path,sort_order) VALUES(?,?,?,?,?)`,
			pid, id, p.ProviderID, p.Path, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (h *Holder) DeleteSource(id string) error {
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM source_paths WHERE source_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sources WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM songs WHERE source_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ============ 用户 / 音源授权 ============

type UserWithSources struct {
	Username  string   `json:"username"`
	Role      string   `json:"role"`
	Enabled   bool     `json:"enabled"`
	Sources   []string `json:"sources"` // 授权的 source_id 列表
	CreatedAt string   `json:"created_at"`
}

// GetUserRole 返回用户的角色（admin/user），用户不存在或禁用返回空串
func (h *Holder) GetUserRole(username string) string {
	if username == "" {
		return ""
	}
	var role string
	err := h.DB.QueryRow(`SELECT role FROM subsonic_users WHERE username=? AND enabled=1`, username).Scan(&role)
	if err != nil {
		return ""
	}
	return role
}

// ListUsersWithSources 列出所有用户及其授权音源
func (h *Holder) ListUsersWithSources() ([]UserWithSources, error) {
	rows, err := h.DB.Query(`SELECT username, role, enabled, created_at FROM subsonic_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserWithSources{}
	for rows.Next() {
		var u UserWithSources
		var en int
		rows.Scan(&u.Username, &u.Role, &en, &u.CreatedAt)
		u.Enabled = en == 1
		u.Sources = h.GetUserSources(u.Username)
		if u.Sources == nil {
			u.Sources = []string{}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserSources 返回用户被授权的音源 ID 列表
func (h *Holder) GetUserSources(username string) []string {
	if username == "" {
		return nil
	}
	rows, err := h.DB.Query(`SELECT source_id FROM user_sources WHERE username=?`, username)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var sid string
		rows.Scan(&sid)
		out = append(out, sid)
	}
	return out
}

// SetUserSources 重设用户的音源授权（先删后插）
func (h *Holder) SetUserSources(username string, sourceIDs []string) error {
	tx, err := h.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_sources WHERE username=?`, username); err != nil {
		return err
	}
	for _, sid := range sourceIDs {
		if sid == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_sources(username, source_id) VALUES(?,?)`,
			username, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserCanSeeSource 检查用户是否有权限看某个音源。admin 永远返回 true
func (h *Holder) UserCanSeeSource(username, sourceID string) bool {
	if sourceID == "" {
		return false
	}
	role := h.GetUserRole(username)
	if role == "admin" {
		return true
	}
	var n int
	h.DB.QueryRow(`SELECT COUNT(*) FROM user_sources WHERE username=? AND source_id=?`,
		username, sourceID).Scan(&n)
	return n > 0
}

// ListSourcesByUser 返回用户能看的音源列表
// admin 返回全部；普通用户返回被授权的
func (h *Holder) ListSourcesByUser(username string) ([]Source, error) {
	role := h.GetUserRole(username)
	if role == "admin" {
		return h.ListSources()
	}
	ids := h.GetUserSources(username)
	if len(ids) == 0 {
		return []Source{}, nil
	}
	ph := strings.Repeat("?,", len(ids))
	ph = ph[:len(ph)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := h.DB.Query(
		`SELECT id, name, provider_id, path, COALESCE(created_at,'') FROM sources WHERE id IN (`+ph+`) ORDER BY created_at DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.Name, &s.ProviderID, &s.Path, &s.CreatedAt); err != nil {
			continue
		}
		s.Paths, _ = h.loadSourcePaths(s.ID)
		if s.Paths == nil {
			s.Paths = []SourcePath{}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UserCanSeeSong 检查用户是否有权限看某首歌（按 source_id 授权）
func (h *Holder) UserCanSeeSong(username, songID string) bool {
	role := h.GetUserRole(username)
	if role == "admin" {
		return true
	}
	// 查歌曲的 source_id
	var sid string
	err := h.DB.QueryRow(`SELECT source_id FROM songs WHERE id=?`, songID).Scan(&sid)
	if err != nil {
		return false
	}
	// 在线歌（source_id 为空）所有登录用户都能看
	if sid == "" {
		return true
	}
	return h.UserCanSeeSource(username, sid)
}

// GetUserPassword 返回用户的密码和启用状态（web 登录用）
func (h *Holder) GetUserPassword(username string) (password, role string, enabled bool, ok bool) {
	var pw, rl string
	var en int
	err := h.DB.QueryRow(`SELECT password, role, enabled FROM subsonic_users WHERE username=?`, username).
		Scan(&pw, &rl, &en)
	if err != nil {
		return "", "", false, false
	}
	return pw, rl, en == 1, true
}
