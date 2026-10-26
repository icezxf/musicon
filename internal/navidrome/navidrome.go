package navidrome

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
)

type Handler struct {
	DB    *db.Holder
	AList *alist.Manager
	Cfg   *config.Config
}

func New(database *db.Holder, am *alist.Manager, c *config.Config) *Handler {
	h := &Handler{DB: database, AList: am, Cfg: c}
	h.DB.DB.Exec("ALTER TABLE songs ADD COLUMN starred INTEGER DEFAULT 0")
	
	// 建立独立的收藏表（用于分离歌曲、专辑、歌手的收藏）
	h.DB.DB.Exec(`
		CREATE TABLE IF NOT EXISTS user_favorites (
			username TEXT NOT NULL,
			kind TEXT NOT NULL,      -- 'artist' 或 'album'
			item_id TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (username, kind, item_id)
		)
	`)
	return h
}

var (
	sessionTokens = sync.Map{}
)

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/api/keepalive/keepalive", h.keepalive)
	
	mux.HandleFunc("/api/artist", h.route)
	mux.HandleFunc("/api/artist/", h.route)
	mux.HandleFunc("/api/album", h.route)
	mux.HandleFunc("/api/album/", h.route)
	mux.HandleFunc("/api/song", h.route)
	mux.HandleFunc("/api/song/", h.route)
	mux.HandleFunc("/api/playlist", h.route)
	mux.HandleFunc("/api/playlist/", h.route)
	mux.HandleFunc("/api/star", h.route)
	mux.HandleFunc("/api/unstar", h.route)
	mux.HandleFunc("/api/cover", h.route)
	mux.HandleFunc("/api/cover/", h.route)
	
	mux.HandleFunc("/api/stream", h.route)
	mux.HandleFunc("/api/stream/", h.route)
}

// ==================== 认证 ====================

type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if !auth.VerifySubsonic(h.DB.DB, req.Username, req.Password, "", "") {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	hToken := md5.Sum([]byte(req.Username + req.Password))
	token := hex.EncodeToString(hToken[:])
	clientID := "musicon-" + token[:8]
	sessionTokens.Store(token, req.Username)

	subsonicSalt := fmt.Sprintf("%d", time.Now().UnixNano())
	stMd5 := md5.Sum([]byte(req.Password + subsonicSalt))
	subsonicToken := hex.EncodeToString(stMd5[:])

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":            clientID,
		"isAdmin":       true,
		"name":          req.Username,
		"token":         token,
		"username":      req.Username,
		"subsonicSalt":  subsonicSalt,
		"subsonicToken": subsonicToken,
		"lastFMApiKey":  "",
	})
}

func (h *Handler) keepalive(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.checkAuth(r); !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{}"))
}

func (h *Handler) checkAuth(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("x-nd-authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if user, ok := sessionTokens.Load(token); ok {
			return user.(string), true
		}
	}
	if token := r.URL.Query().Get("token"); token != "" {
		if user, ok := sessionTokens.Load(token); ok {
			return user.(string), true
		}
	}
	return "", false
}

// ==================== 路由分发 ====================

func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	username, ok := h.checkAuth(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	p := strings.TrimPrefix(r.URL.Path, "/api/")
	p = strings.TrimSuffix(p, "/")
	log.Printf("[navidrome] %s %s user=%s", r.Method, p, username)

	if p == "stream" || strings.HasPrefix(p, "stream/") {
		h.handleStream(w, r)
		return
	}

	if strings.HasPrefix(p, "cover/") {
		id := strings.TrimPrefix(p, "cover/")
		http.Redirect(w, r, "/rest/getCoverArt?id="+id, http.StatusFound)
		return
	}

	if p == "star" || p == "unstar" {
		h.handleStar(w, r, username)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch {
	case p == "artist":
		h.getArtists(w, r, username)
	case strings.HasPrefix(p, "artist/"):
		h.getArtistDetail(w, r, strings.TrimPrefix(p, "artist/"), username)
	case p == "album":
		h.getAlbums(w, r, username)
	case strings.HasPrefix(p, "album/"):
		h.getAlbumDetail(w, r, strings.TrimPrefix(p, "album/"), username)
	case p == "song":
		h.getSongs(w, r, username)
	case strings.HasPrefix(p, "song/"):
		h.getSongDetail(w, r, strings.TrimPrefix(p, "song/"), username)
	case p == "playlist":
		h.handlePlaylistRoot(w, r, username)
	case strings.HasPrefix(p, "playlist/"):
		h.handlePlaylistSub(w, r, p, username)
	default:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}
}

// ==================== 收藏处理（独立隔离） ====================

func (h *Handler) handleStar(w http.ResponseWriter, r *http.Request, user string) {
	var ids, albumIds, artistIds []string

	if r.Method == http.MethodPost {
		var req struct {
			Ids       []string `json:"ids"`
			AlbumIds  []string `json:"albumIds"`
			ArtistIds []string `json:"artistIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			ids = req.Ids
			albumIds = req.AlbumIds
			artistIds = req.ArtistIds
		}
	}
	if id := r.URL.Query().Get("id"); id != "" { ids = append(ids, id) }
	if aid := r.URL.Query().Get("albumId"); aid != "" { albumIds = append(albumIds, aid) }
	if arid := r.URL.Query().Get("artistId"); arid != "" { artistIds = append(artistIds, arid) }

	isStar := strings.HasSuffix(r.URL.Path, "/star")
	val := 0
	if isStar { val = 1 }

	// 歌曲
	for _, id := range ids {
		cleanID := strings.TrimPrefix(id, "mf-")
		h.DB.DB.Exec("UPDATE songs SET starred = ? WHERE id = ?", val, cleanID)
	}

	// 专辑
	for _, aid := range albumIds {
		cleanID := strings.TrimPrefix(aid, "al-")
		if isStar {
			h.DB.DB.Exec("INSERT OR IGNORE INTO user_favorites (username, kind, item_id) VALUES (?, 'album', ?)", user, cleanID)
		} else {
			h.DB.DB.Exec("DELETE FROM user_favorites WHERE username = ? AND kind = 'album' AND item_id = ?", user, cleanID)
		}
	}

	// 歌手
	for _, arid := range artistIds {
		cleanID := strings.TrimPrefix(arid, "ar-")
		if isStar {
			h.DB.DB.Exec("INSERT OR IGNORE INTO user_favorites (username, kind, item_id) VALUES (?, 'artist', ?)", user, cleanID)
		} else {
			h.DB.DB.Exec("DELETE FROM user_favorites WHERE username = ? AND kind = 'artist' AND item_id = ?", user, cleanID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{}"))
}

func (h *Handler) getStarredMap() map[string]bool {
	m := map[string]bool{}
	rows, err := h.DB.DB.Query("SELECT id FROM songs WHERE starred = 1")
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil { m[id] = true }
		}
		rows.Close()
	}
	return m
}

func (h *Handler) getFavoriteAlbums(user string) map[string]bool {
	m := map[string]bool{}
	rows, err := h.DB.DB.Query("SELECT item_id FROM user_favorites WHERE username = ? AND kind = 'album'", user)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil { m[id] = true }
		}
		rows.Close()
	}
	return m
}

func (h *Handler) getFavoriteArtists(user string) map[string]bool {
	m := map[string]bool{}
	rows, err := h.DB.DB.Query("SELECT item_id FROM user_favorites WHERE username = ? AND kind = 'artist'", user)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil { m[id] = true }
		}
		rows.Close()
	}
	return m
}

// ==================== 核心：音频流 302 ====================

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	songID := r.URL.Query().Get("id")
	if songID == "" {
		songID = strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/stream/")
	}
	if songID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	song, err := h.DB.GetSong(songID)
	if err != nil {
		http.Error(w, "song not found", http.StatusNotFound)
		return
	}

	if strings.HasPrefix(song.ID, "lxv-") || song.Fmt == "VIRTUAL" {
		http.Error(w, "virtual songs not supported in native API", http.StatusNotImplemented)
		return
	}

	providerID := song.ProviderID
	if providerID == "" {
		providerID = "alist-1"
	}

	rawURL, err := h.AList.Get(providerID).GetRawURL(song.Path)
	if err != nil {
		log.Printf("[navidrome] alist stream error: %v", err)
		http.Error(w, "stream error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Accept-Ranges", "bytes")
	http.Redirect(w, r, rawURL, http.StatusFound)
}

// ==================== 基础字段清洗 ====================

func cleanSongMeta(s db.Song) (album, artist, albumArtist, title, suffix string) {
	album = strings.TrimSpace(s.Album)
	if album == "" { album = "未知专辑" }
	artist = strings.TrimSpace(s.Artist)
	if artist == "" { artist = "未知歌手" }
	albumArtist = strings.TrimSpace(s.AlbumArtist)
	if albumArtist == "" { albumArtist = artist }
	title = strings.TrimSpace(s.Title)
	if title == "" { title = "未知歌曲" }
	suffix = strings.ToLower(s.Fmt)
	if suffix == "" {
		ext := path.Ext(s.Path)
		if ext != "" { suffix = strings.TrimPrefix(ext, ".") } else { suffix = "mp3" }
	}
	return
}

func maxNum(a, b int) int {
	if a > b { return a }
	return b
}

func isStarred(r *http.Request) bool {
	v := r.URL.Query().Get("starred")
	return v == "true" || v == "1"
}

// 获取主歌手名，统一各处的 ID 映射
func primaryArtist(artist string) string {
	parts := splitArtists(artist)
	if len(parts) > 0 { return parts[0] }
	return artist
}

// 文档要求的日期格式是年份字符串（如 "2013"），如果是 0 则给空
func safeYear(year int) string {
	if year <= 0 { return "" }
	return strconv.Itoa(year)
}

// ==================== 歌手与专辑 ====================

func (h *Handler) getArtists(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	nameFilter := strings.ToLower(r.URL.Query().Get("name"))
	starFilter := isStarred(r)
	favArtists := h.getFavoriteArtists(user)

	type artistAgg struct { name string; albumCount int; songCount int; size int64 }
	artistMap := map[string]*artistAgg{}
	artistAlbums := map[string]map[string]bool{}

	for _, s := range songs {
		_, artist, _, _, _ := cleanSongMeta(s)
		for _, name := range splitArtists(artist) {
			if nameFilter != "" && !strings.Contains(strings.ToLower(name), nameFilter) { continue }
			id := genID(name)
			if starFilter && !favArtists[id] { continue }

			if _, ok := artistMap[name]; !ok {
				artistMap[name] = &artistAgg{name: name}
				artistAlbums[name] = map[string]bool{}
			}
			artistMap[name].songCount++
			artistMap[name].size += s.FileSize
			album, _, _, _, _ := cleanMovie(s)
			artistAlbums[name][album] = true
		}
	}

	nowStr := time.Now().Format(time.RFC3339)
	out := make([]map[string]any, 0)
	for name, agg := range artistMap {
		id := genID(name)
		isFav := favArtists[id]
		var starredAt any = nil
		if isFav { starredAt = nowStr }

		out = append(out, map[string]any{
			"playCount":             0,
			"playDate":              nil,
			"rating":                0,
			"starred":               isFav,
			"starredAt":             starredAt,
			"id":                    id,
			"name":                  name,
			"albumCount":            len(artistAlbums[name]),
			"songCount":             agg.songCount,
			"genres":                nil, // 文档允许 null
			"fullText":              " " + name,
			"orderArtistName":       name,
			"size":                  agg.size,
			"mbzArtistId":           "",
			"smallImageUrl":         "",
			"mediumImageUrl":        "",
			"largeImageUrl":         "",
			"externalUrl":           "https://www.last.fm/music/" + name,
			"externalInfoUpdatedAt": "0001-01-01T00:00:00Z",
		})
	}
	
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	total := len(out)
	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(total))
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) getArtistDetail(w http.ResponseWriter, r *http.Request, id, user string) {
	songs := h.visibleSongs(user)
	var artistName string
	albumSet := map[string]bool{}
	var songCount int
	var totalSize int64

	for _, s := range songs {
		_, artist, _, _, _ := cleanSongMeta(s)
		for _, name := range splitArtists(artist) {
			if genID(name) == id {
				artistName = name
				songCount++
				totalSize += s.FileSize
				album, _, _, _, _ := cleanSongMeta(s)
				albumSet[album] = true
			}
		}
	}

	// 【防崩溃】找不到返回兜底空对象，不返回 404
	if artistName == "" { artistName = "未知歌手" }

	favArtists := h.getFavoriteArtists(user)
	isFav := favArtists[id]
	var starredAt any = nil
	if isFav { starredAt = time.Now().Format(time.RFC3339) }

	json.NewEncoder(w).Encode(map[string]any{
		"playCount":             0,
		"playDate":              nil,
		"rating":                0,
		"starred":               isFav,
		"starredAt":             starredAt,
		"id":                    id,
		"name":                  artistName,
		"albumCount":            len(albumSet),
		"songCount":             songCount,
		"genres":                nil,
		"fullText":              " " + artistName,
		"orderArtistName":       artistName,
		"size":                  totalSize,
		"mbzArtistId":           "",
		"smallImageUrl":         "",
		"mediumImageUrl":        "",
		"largeImageUrl":         "",
		"externalUrl":           "https://www.last.fm/music/" + artistName,
		"externalInfoUpdatedAt": "0001-01-01T00:00:00Z",
	})
}

func (h *Handler) getAlbums(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	artistIDFilter := r.URL.Query().Get("artist_id")
	nameFilter := strings.ToLower(r.URL.Query().Get("name"))
	starFilter := isStarred(r)
	favAlbums := h.getFavoriteAlbums(user)

	type albumAgg struct {
		name string; artist string; count int; dur int; size int64
		minYear int; maxYear int; coverPath string; paths string
	}
	albumMap := map[string]*albumAgg{}

	for _, s := range songs {
		album, artist, _, _, _ := cleanSongMeta(s)
		
		if artistIDFilter != "" {
			matched := false
			for _, n := range splitArtists(artist) {
				if genID(n) == artistIDFilter { matched = true; break }
			}
			if !matched { continue }
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(album), nameFilter) { continue }

		k := album + "|" + artist
		id := genID(k)
		if starFilter && !favAlbums[id] { continue }

		if _, ok := albumMap[k]; !ok {
			albumMap[k] = &albumAgg{name: album, artist: artist, minYear: 9999, coverPath: s.CoverPath, paths: s.Path}
		}
		agg := albumMap[k]
		agg.count++
		agg.dur += s.Dur
		agg.size += s.FileSize
		if s.Year > 0 {
			if s.Year < agg.minYear { agg.minYear = s.Year }
			if s.Year > agg.maxYear { agg.maxYear = s.Year }
		}
	}

	nowStr := time.Now().Format(time.RFC3339)
	out := make([]map[string]any, 0)
	for _, agg := range albumMap {
		id := genID(agg.name + "|" + agg.artist)
		arID := genID(primaryArtist(agg.artist))
		minYear := agg.minYear
		if minYear == 9999 { minYear = 0 }
		
		isFav := favAlbums[id]
		var starredAt any = nil
		if isFav { starredAt = nowStr }

		out = append(out, map[string]any{
			"id": id, "name": agg.name, "embedArtPath": agg.coverPath,
			"coverArt": id,
			"artistId": arID, "artist": agg.artist, "albumArtistId": arID, "albumArtist": agg.artist,
			"allArtistIds": arID, "maxYear": agg.maxYear, "minYear": minYear, 
			"date": safeYear(minYear), // 文档要求年份字符串
			"maxOriginalYear": 0, "minOriginalYear": 0, 
			"releases": 1, // 文档是整数 1
			"compilation": false,
			"songCount": agg.count, 
			"duration": float64(agg.dur), // 文档是浮点数
			"size": agg.size, "genre": "", 
			"genres": nil, // 文档允许 null
			"fullText": " " + agg.name + " " + agg.artist, "orderAlbumName": agg.name,
			"orderAlbumArtistName": agg.artist, "paths": agg.paths, "playCount": 0, 
			"playDate": nil, "rating": 0, "starred": isFav, "starredAt": starredAt, 
			"externalInfoUpdatedAt": nil, "createdAt": nowStr, "updatedAt": nowStr,
			"mbzAlbumId": "", "mbzAlbumArtistId": "", "mbzReleaseGroupId": "",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	total := len(out)
	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(total))
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) getAlbumDetail(w http.ResponseWriter, r *http.Request, id, user string) {
	songs := h.visibleSongs(user)
	var outAlbum map[string]any
	var songCount, totalDur int
	var totalSize int64

	for _, s := range songs {
		album, artist, albumArtist, _, _ := cleanSongMeta(s)
		if genID(album+"|"+artist) == id {
			if outAlbum == nil {
				arID := genID(primaryArtist(artist))
				outAlbum = map[string]any{
					"id": id, "name": album, "embedArtPath": s.CoverPath,
					"coverArt": id,
					"artistId": arID, "artist": artist, "albumArtistId": arID, "albumArtist": albumArtist,
					"allArtistIds": arID, "maxYear": s.Year, "minYear": s.Year, "date": safeYear(s.Year),
					"maxOriginalYear": 0, "minOriginalYear": 0, 
					"releases": 1, // 文档是整数 1
					"compilation": false,
					"genre": s.Genre, "genres": nil, // 文档允许 null
					"fullText": " " + album + " " + artist,
					"orderAlbumName": album, "orderAlbumArtistName": artist, "paths": s.Path,
					"playCount": 0, "playDate": nil, "rating": 0, "starred": false, "starredAt": nil,
					"externalInfoUpdatedAt": nil, "createdAt": time.Now().Format(time.RFC3339), "updatedAt": time.Now().Format(time.RFC3339),
					"mbzAlbumId": "", "mbzAlbumArtistId": "", "mbzReleaseGroupId": "",
				}
			}
			songCount++
			totalDur += s.Dur
			totalSize += s.FileSize
		}
	}
	
	// 【防崩溃】找不到返回兜底空对象，不返回 404
	if outAlbum == nil {
		outAlbum = map[string]any{
			"id": id, "name": "未知专辑", "embedArtPath": "", "coverArt": id,
			"artistId": "", "artist": "未知歌手", "albumArtistId": "", "albumArtist": "未知歌手",
			"allArtistIds": "", "maxYear": 0, "minYear": 0, "date": "",
			"maxOriginalYear": 0, "minOriginalYear": 0, "releases": 1, "compilation": false,
			"genre": "", "genres": nil, "fullText": " ", "orderAlbumName": "未知专辑",
			"orderAlbumArtistName": "未知歌手", "paths": "", "playCount": 0, "playDate": nil, 
			"rating": 0, "starred": false, "starredAt": nil, "externalInfoUpdatedAt": nil, 
			"createdAt": time.Now().Format(time.RFC3339), "updatedAt": time.Now().Format(time.RFC3339),
			"mbzAlbumId": "", "mbzAlbumArtistId": "", "mbzReleaseGroupId": "",
		}
	}
	
	favAlbums := h.getFavoriteAlbums(user)
	isFav := favAlbums[id]
	var starredAt any = nil
	if isFav { starredAt = time.Now().Format(time.RFC3339) }
	
	outAlbum["starred"] = isFav
	outAlbum["starredAt"] = starredAt
	outAlbum["songCount"] = songCount
	outAlbum["duration"] = float64(totalDur) // 文档是浮点数
	outAlbum["size"] = totalSize
	json.NewEncoder(w).Encode(outAlbum)
}

// ==================== 歌曲 ====================

func (h *Handler) getSongs(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	albumIDFilter := r.URL.Query().Get("album_id")
	artistIDFilter := r.URL.Query().Get("artist_id") 
	titleFilter := strings.ToLower(r.URL.Query().Get("title"))
	starFilter := isStarred(r)
	starredMap := h.getStarredMap()

	filtered := make([]db.Song, 0)
	for _, s := range songs {
		if starFilter && !starredMap[s.ID] { continue }
		album, artist, _, title, _ := cleanSongMeta(s)
		
		if albumIDFilter != "" && genID(album+"|"+artist) != albumIDFilter { continue }
		
		if artistIDFilter != "" {
			matched := false
			for _, n := range splitArtists(artist) {
				if genID(n) == artistIDFilter { matched = true; break }
			}
			if !matched { continue }
		}
		
		if titleFilter != "" && !strings.Contains(strings.ToLower(title), titleFilter) { continue }
		filtered = append(filtered, s)
	}

	nowStr := time.Now().Format(time.RFC3339)
	out := make([]map[string]any, 0)
	for _, s := range filtered {
		album, artist, _, _, _ := cleanSongMeta(s)
		aID := genID(album + "|" + artist)
		arID := genID(primaryArtist(artist))
		out = append(out, h.songObj(s, aID, arID, starredMap[s.ID], nowStr))
	}

	total := len(out)
	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(total))
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) getSongDetail(w http.ResponseWriter, r *http.Request, id, user string) {
	songs := h.visibleSongs(user)
	var found *db.Song
	for _, s := range songs {
		if s.ID == id { found = &s; break }
	}
	
	// 【防崩溃】找不到返回兜底，不返回 404
	if found == nil {
		json.NewEncoder(w).Encode(h.songObj(db.Song{ID: id, Title: "未知歌曲"}, "", "", false, time.Now().Format(time.RFC3339)))
		return
	}
	
	album, artist, _, _, _ := cleanSongMeta(*found)
	aID := genID(album + "|" + artist)
	arID := genID(primaryArtist(artist))
	starredMap := h.getStarredMap()
	json.NewEncoder(w).Encode(h.songObj(*found, aID, arID, starredMap[found.ID], time.Now().Format(time.RFC3339)))
}

func (h *Handler) songObj(s db.Song, aID, arID string, isStarred bool, nowStr string) map[string]any {
	album, artist, albumArtist, title, suffix := cleanSongMeta(s)
	dur := s.Dur
	if dur <= 0 { dur = 1 }

	var starredAt any = nil
	if isStarred { starredAt = nowStr }

	return map[string]any{
		"playCount":           0,
		"playDate":            nil, // 未播放给 null 合法
		"rating":              0,
		"starred":             isStarred,
		"starredAt":           starredAt,
		"bookmarkPosition":    0,
		"id":                  s.ID,
		"isDir":               false,
		"type":                "music",
		"mediaType":           "song",
		"path":                s.Path,
		"title":               title,
		"album":               album,
		"artistId":            arID,
		"artist":              artist,
		"albumArtistId":       genID(primaryArtist(albumArtist)),
		"albumArtist":         albumArtist,
		"albumId":             aID,
		"coverArt":            s.ID,
		"hasCoverArt":         true,
		"trackNumber":         maxNum(1, s.TrackNumber),
		"discNumber":          maxNum(1, s.DiscNumber),
		"year":                s.Year,
		"date":                safeYear(s.Year), // 文档是年份字符串
		"originalYear":        0,
		"releaseYear":         0,
		"size":                s.FileSize,
		"suffix":              suffix,
		"duration":            float64(dur), // 文档是浮点数
		"bitRate":             s.Bitrate,
		"channels":            s.Channels,
		"genre":               s.Genre,
		"genres":              nil, // 文档允许 null
		"fullText":            " " + title + " " + album + " " + artist,
		"orderTitle":          title,
		"orderAlbumName":      album,
		"orderArtistName":     artist,
		"orderAlbumArtistName": albumArtist,
		"compilation":         false,
		"lyrics":              "[]", // 防解析崩溃
		"rgAlbumGain":         0.0,
		"rgAlbumPeak":         0.0,
		"rgTrackGain":         0.0,
		"rgTrackPeak":         0.0,
		"createdAt":           nowStr,
		"updatedAt":           nowStr,
		"bpm":                 0,
		"comment":             "",
		"moods":               []string{},
		"sortName":            title,
		"discSubtitle":        "",
		"mbzTrackId":          "",
		"mbzAlbumId":          "",
		"mbzArtistId":         "",
	}
}

// ==================== 歌单操作 ====================

func (h *Handler) handlePlaylistRoot(w http.ResponseWriter, r *http.Request, user string) {
	switch r.Method {
	case http.MethodGet:
		h.getPlaylists(w, r, user)
	case http.MethodPost:
		var req struct {
			Name    string `json:"name"`
			Comment string `json:"comment"`
			Public  bool   `json:"public"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"bad request"}`))
			return
		}
		id, err := h.DB.CreatePlaylist(req.Name, req.Comment, user, req.Public)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"`+err.Error()+`"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": id})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handlePlaylistSub(w http.ResponseWriter, r *http.Request, p string, user string) {
	parts := strings.Split(p, "/")
	id := parts[1]
	
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			h.getPlaylistDetail(w, r, id, user)
		case http.MethodDelete:
			p, err := h.DB.GetPlaylist(id)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"not found"}`))
				return
			}
			role := h.DB.GetUserRole(user)
			if role != "admin" && p.Owner != user {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"forbidden"}`))
				return
			}
			h.DB.DeletePlaylist(id)
			w.Write([]byte("{}"))
		case http.MethodPut:
			var req struct {
				Name    string `json:"name"`
				Comment string `json:"comment"`
				Public  bool   `json:"public"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"bad request"}`))
				return
			}
			
			if req.Name != "" { h.DB.DB.Exec(`UPDATE playlists SET name=? WHERE id=?`, req.Name, id) }
			h.DB.DB.Exec(`UPDATE playlists SET comment=? WHERE id=?`, req.Comment, id)
			b := 0; if req.Public { b = 1 }
			h.DB.DB.Exec(`UPDATE playlists SET public=? WHERE id=?`, b, id)
			
			w.Write([]byte("{}"))
		}
	} else if len(parts) == 3 && parts[2] == "tracks" {
		switch r.Method {
		case http.MethodGet:
			h.getPlaylistTracks(w, r, id, user)
		case http.MethodPost:
			var songIDs []string
			var req struct {
				Ids interface{} `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				log.Printf("[navidrome] playlist add tracks decode err: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"bad request"}`))
				return
			}
			
			if req.Ids != nil {
				if arr, ok := req.Ids.([]interface{}); ok {
					for _, item := range arr {
						if s, ok := item.(string); ok {
							songIDs = append(songIDs, s)
						}
					}
				} else if str, ok := req.Ids.(string); ok {
					songIDs = strings.Split(str, ",")
				}
			}
			
			if len(songIDs) > 0 {
				log.Printf("[navidrome] adding %d songs to playlist %s", len(songIDs), id)
				if err := h.DB.AddSongsToPlaylist(id, songIDs); err != nil {
					log.Printf("[navidrome] AddSongsToPlaylist err: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":"add failed"}`))
					return
				}
			}
			w.Write([]byte("{}"))
		case http.MethodDelete:
			songs, _ := h.DB.GetPlaylistSongs(id)
			q := r.URL.Query()
			rowIDs := q["id"]
			var toRemove []string
			for _, rowStr := range rowIDs {
				if idx, err := strconv.Atoi(rowStr); err == nil && idx > 0 && idx <= len(songs) {
					toRemove = append(toRemove, songs[idx-1].ID)
				}
			}
			if len(toRemove) > 0 {
				if err := h.DB.RemoveSongsFromPlaylist(id, toRemove); err != nil {
					log.Printf("[navidrome] RemoveSongsFromPlaylist err: %v", err)
				}
			}
			w.Write([]byte("{}"))
		}
	}
}

func (h *Handler) getPlaylists(w http.ResponseWriter, r *http.Request, user string) {
	pls, _ := h.DB.ListPlaylists()
	role := h.DB.GetUserRole(user)
	nowStr := time.Now().Format(time.RFC3339)
	out := make([]map[string]any, 0)
	for _, p := range pls {
		if role != "admin" && p.Owner != user && !p.Public { continue }
		songs, _ := h.DB.GetPlaylistSongs(p.ID)
		var size int64
		var dur int
		for _, s := range songs { size += s.FileSize; dur += s.Dur }

		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "comment": "", "duration": float64(dur),
			"size": size, "songCount": len(songs), "ownerName": p.Owner, "ownerId": genID(p.Owner),
			"public": p.Public, "path": "", "sync": false, "createdAt": nowStr, "updatedAt": nowStr,
			"rules": []any{}, "evaluatedAt": nil,
		})
	}
	total := len(out)
	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(total))
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) getPlaylistDetail(w http.ResponseWriter, r *http.Request, id, user string) {
	p, err := h.DB.GetPlaylist(id)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{})
		return
	}
	songs, _ := h.DB.GetPlaylistSongs(id)
	var size int64
	var dur int
	for _, s := range songs { size += s.FileSize; dur += s.Dur }

	nowStr := time.Now().Format(time.RFC3339)
	json.NewEncoder(w).Encode(map[string]any{
		"id": p.ID, "name": p.Name, "comment": "", "duration": float64(dur), "size": size,
		"songCount": len(songs), "ownerName": p.Owner, "ownerId": genID(p.Owner),
		"public": p.Public, "path": "", "sync": false, "createdAt": nowStr, "updatedAt": nowStr,
		"rules": []any{}, "evaluatedAt": nil,
	})
}

func (h *Handler) getPlaylistTracks(w http.ResponseWriter, r *http.Request, id, user string) {
	songs, _ := h.DB.GetPlaylistSongs(id)
	nowStr := time.Now().Format(time.RFC3339)
	out := make([]map[string]any, 0)
	starredMap := h.getStarredMap()
	
	for idx, s := range songs {
		album, artist, _, _, _ := cleanSongMeta(s)
		aID := genID(album + "|" + artist)
		arID := genID(primaryArtist(artist))
		
		sObj := h.songObj(s, aID, arID, starredMap[s.ID], nowStr)
		sObj["id"] = s.ID
		sObj["mediaFileId"] = s.ID
		sObj["playlistEntryId"] = strconv.Itoa(idx + 1)
		sObj["playlistId"] = id
		out = append(out, sObj)
	}
	total := len(out)
	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(total))
	json.NewEncoder(w).Encode(out)
}

// ==================== 辅助函数 ====================

func (h *Handler) visibleSongs(username string) []db.Song {
	songs, _ := h.DB.ListSongs()
	role := h.DB.GetUserRole(username)
	if role == "admin" { return songs }
	allowed := h.DB.GetUserSources(username)
	set := map[string]bool{}
	for _, id := range allowed { set[id] = true }
	out := make([]db.Song, 0, len(songs))
	for _, s := range songs {
		if s.SourceID == "" || set[s.SourceID] { out = append(out, s) }
	}
	return out
}

func paginate(data []map[string]any, r *http.Request) []map[string]any {
	if data == nil { return []map[string]any{} }
	start, _ := strconv.Atoi(r.URL.Query().Get("_start"))
	end, _ := strconv.Atoi(r.URL.Query().Get("_end"))
	if start > len(data) { start = len(data) }
	if end <= 0 || end > len(data) { end = len(data) }
	if start > end { start = end }
	
	res := data[start:end]
	if res == nil { return []map[string]any{} }
	return res
}

func genID(name string) string {
	h := md5.Sum([]byte(name))
	return hex.EncodeToString(h[:])
}

var splitArtistRe = regexp.MustCompile(`[、,，&＆;；]|\s+feat\.?\s+|\s+ft\.?\s+|\s+vs\.?\s+|\s*/\s*`)

func splitArtists(s string) []string {
	if s == "" { return nil }
	parts := splitArtistRe.Split(s, -1)
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] { continue }
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 { return []string{s} }
	return out
}