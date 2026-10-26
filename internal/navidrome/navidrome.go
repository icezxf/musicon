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
	return &Handler{DB: database, AList: am, Cfg: c}
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

	// 原生封面代理
	if strings.HasPrefix(p, "cover/") {
		id := strings.TrimPrefix(p, "cover/")
		http.Redirect(w, r, "/rest/getCoverArt?id="+id, http.StatusFound)
		return
	}

	// 【新增】接管 star / unstar
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
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
}

// ==================== 收藏处理 ====================

func (h *Handler) handleStar(w http.ResponseWriter, r *http.Request, user string) {
	var ids []string

	// 尝试解析 POST Body
	if r.Method == http.MethodPost {
		var req struct {
			Ids []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			ids = req.Ids
		}
	}

	// 兼容 Query 参数
	if len(ids) == 0 {
		if id := r.URL.Query().Get("id"); id != "" {
			ids = []string{id}
		}
	}

	isStar := strings.HasSuffix(r.URL.Path, "/star")
	val := 0
	if isStar {
		val = 1
	}

	// 写入数据库（如果你的 songs 表没有 starred 字段，这里会静默失败，但不影响客户端）
	for _, id := range ids {
		// 兼容带前缀的 ID
		cleanID := id
		if strings.HasPrefix(cleanID, "mf-") { cleanID = strings.TrimPrefix(cleanID, "mf-") }
		
		_, err := h.DB.DB.Exec("UPDATE songs SET starred = ? WHERE id = ?", val, cleanID)
		if err != nil {
			log.Printf("[navidrome] star/unstar SQL err: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{}"))
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

// ==================== 歌手与专辑 ====================

func (h *Handler) getArtists(w http.ResponseWriter, r *http.Request, user string) {
	if isStarred(r) {
		w.Header().Set("x-total-count", "0")
		w.Write([]byte("[]"))
		return
	}

	songs := h.visibleSongs(user)
	nameFilter := strings.ToLower(r.URL.Query().Get("name"))

	type artistAgg struct { name string; albumCount int; songCount int; size int64 }
	artistMap := map[string]*artistAgg{}
	artistAlbums := map[string]map[string]bool{}

	for _, s := range songs {
		_, artist, _, _, _ := cleanSongMeta(s)
		for _, name := range splitArtists(artist) {
			if nameFilter != "" && !strings.Contains(strings.ToLower(name), nameFilter) { continue }
			if _, ok := artistMap[name]; !ok {
				artistMap[name] = &artistAgg{name: name}
				artistAlbums[name] = map[string]bool{}
			}
			artistMap[name].songCount++
			artistMap[name].size += s.FileSize
			album, _, _, _, _ := cleanSongMeta(s)
			artistAlbums[name][album] = true
		}
	}

	var out []map[string]any
	for name, agg := range artistMap {
		id := genID(name)
		out = append(out, map[string]any{
			"playCount":             0,
			"playDate":              nil,
			"rating":                0,
			"starred":               false,
			"starredAt":             nil,
			"id":                    id,
			"name":                  name,
			"coverArt":              id,
			"albumCount":            len(artistAlbums[name]),
			"songCount":             agg.songCount,
			"genres":                nil,
			"fullText":              " " + name,
			"orderArtistName":       name,
			"size":                  agg.size,
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

	if artistName == "" {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"playCount":             0,
		"playDate":              nil,
		"rating":                0,
		"starred":               false,
		"starredAt":             nil,
		"id":                    id,
		"name":                  artistName,
		"coverArt":              id,
		"albumCount":            len(albumSet),
		"songCount":             songCount,
		"genres":                nil,
		"fullText":              " " + artistName,
		"orderArtistName":       artistName,
		"size":                  totalSize,
		"externalUrl":           "https://www.last.fm/music/" + artistName,
		"externalInfoUpdatedAt": "0001-01-01T00:00:00Z",
	})
}

func (h *Handler) getAlbums(w http.ResponseWriter, r *http.Request, user string) {
	if isStarred(r) {
		w.Header().Set("x-total-count", "0")
		w.Write([]byte("[]"))
		return
	}

	songs := h.visibleSongs(user)
	artistIDFilter := r.URL.Query().Get("artist_id")
	nameFilter := strings.ToLower(r.URL.Query().Get("name"))

	type albumAgg struct {
		name string; artist string; count int; dur int; size int64
		minYear int; maxYear int; coverPath string; paths string
	}
	albumMap := map[string]*albumAgg{}

	for _, s := range songs {
		album, artist, _, _, _ := cleanSongMeta(s)
		if artistIDFilter != "" && genID(artist) != artistIDFilter { continue }
		if nameFilter != "" && !strings.Contains(strings.ToLower(album), nameFilter) { continue }

		k := album + "|" + artist
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
	var out []map[string]any
	for _, agg := range albumMap {
		id := genID(agg.name + "|" + agg.artist)
		arID := genID(agg.artist)
		minYear := agg.minYear
		if minYear == 9999 { minYear = 0 }
		out = append(out, map[string]any{
			"id": id, "name": agg.name, "embedArtPath": agg.coverPath,
			"coverArt": id,
			"artistId": arID, "artist": agg.artist, "albumArtistId": arID, "albumArtist": agg.artist,
			"allArtistIds": arID, "maxYear": agg.maxYear, "minYear": minYear, "date": strconv.Itoa(minYear),
			"maxOriginalYear": 0, "minOriginalYear": 0, "releases": 1, "compilation": false,
			"songCount": agg.count, "duration": float64(agg.dur), "size": agg.size, "genre": "", "genres": nil,
			"fullText": " " + agg.name + " " + agg.artist, "orderAlbumName": agg.name,
			"orderAlbumArtistName": agg.artist, "paths": agg.paths, "playCount": 0, "playDate": nil,
			"rating": 0, "starred": false, "starredAt": nil, "externalInfoUpdatedAt": nil,
			"createdAt": nowStr, "updatedAt": nowStr,
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
				arID := genID(artist)
				outAlbum = map[string]any{
					"id": id, "name": album, "embedArtPath": s.CoverPath,
					"coverArt": id,
					"artistId": arID, "artist": artist, "albumArtistId": arID, "albumArtist": albumArtist,
					"allArtistIds": arID, "maxYear": s.Year, "minYear": s.Year, "date": strconv.Itoa(s.Year),
					"maxOriginalYear": 0, "minOriginalYear": 0, "releases": 1, "compilation": false,
					"genre": s.Genre, "genres": nil, "fullText": " " + album + " " + artist,
					"orderAlbumName": album, "orderAlbumArtistName": artist, "paths": s.Path,
					"playCount": 0, "playDate": nil, "rating": 0, "starred": false, "starredAt": nil,
					"externalInfoUpdatedAt": nil, "createdAt": time.Now().Format(time.RFC3339), "updatedAt": time.Now().Format(time.RFC3339),
				}
			}
			songCount++
			totalDur += s.Dur
			totalSize += s.FileSize
		}
	}
	if outAlbum == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		return
	}
	outAlbum["songCount"] = songCount
	outAlbum["duration"] = float64(totalDur)
	outAlbum["size"] = totalSize
	json.NewEncoder(w).Encode(outAlbum)
}

// ==================== 歌曲 ====================

func (h *Handler) getSongs(w http.ResponseWriter, r *http.Request, user string) {
	if isStarred(r) {
		w.Header().Set("x-total-count", "0")
		w.Write([]byte("[]"))
		return
	}

	songs := h.visibleSongs(user)
	albumIDFilter := r.URL.Query().Get("album_id")
	artistIDFilter := r.URL.Query().Get("artist_id") 
	titleFilter := strings.ToLower(r.URL.Query().Get("title"))

	var filtered []db.Song
	for _, s := range songs {
		album, artist, _, title, _ := cleanSongMeta(s)
		
		if albumIDFilter != "" && genID(album+"|"+artist) != albumIDFilter { continue }
		
		if artistIDFilter != "" {
			matched := false
			for _, n := range splitArtists(artist) {
				if genID(n) == artistIDFilter {
					matched = true
					break
				}
			}
			if !matched { continue }
		}
		
		if titleFilter != "" && !strings.Contains(strings.ToLower(title), titleFilter) { continue }
		filtered = append(filtered, s)
	}

	nowStr := time.Now().Format(time.RFC3339)
	var out []map[string]any
	for _, s := range filtered {
		album, artist, _, _, _ := cleanSongMeta(s)
		aID := genID(album + "|" + artist)
		arID := genID(artist)
		out = append(out, h.songObj(s, aID, arID, "", nowStr))
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
	if found == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		return
	}
	album, artist, _, _, _ := cleanSongMeta(*found)
	aID := genID(album + "|" + artist)
	arID := genID(artist)
	json.NewEncoder(w).Encode(h.songObj(*found, aID, arID, "", time.Now().Format(time.RFC3339)))
}

func (h *Handler) songObj(s db.Song, aID, arID string, lyricsStr string, nowStr string) map[string]any {
	album, artist, albumArtist, title, suffix := cleanSongMeta(s)
	dur := float64(s.Dur)
	if dur <= 0 { dur = 0.1 }

	return map[string]any{
		"playCount":           0,
		"playDate":            nil,
		"rating":              0,
		"starred":             false,
		"starredAt":           nil,
		"bookmarkPosition":    0,
		"id":                  s.ID,
		"path":                s.Path,
		"title":               title,
		"album":               album,
		"artistId":            arID,
		"artist":              artist,
		"albumArtistId":       arID,
		"albumArtist":         albumArtist,
		"albumId":             aID,
		"coverArt":            s.ID,
		"hasCoverArt":         true,
		"trackNumber":         maxNum(1, s.TrackNumber),
		"discNumber":          maxNum(1, s.DiscNumber),
		"year":                s.Year,
		"date":                strconv.Itoa(s.Year),
		"originalYear":        0,
		"releaseYear":         0,
		"size":                s.FileSize,
		"suffix":              suffix,
		"duration":            dur,
		"bitRate":             s.Bitrate,
		"channels":            s.Channels,
		"genre":               s.Genre,
		"genres":              nil,
		"fullText":            " " + title + " " + album + " " + artist,
		"orderTitle":          title,
		"orderAlbumName":      album,
		"orderArtistName":     artist,
		"orderAlbumArtistName": albumArtist,
		"compilation":         false,
		"lyrics":              "[]",
		"rgAlbumGain":         0.0,
		"rgAlbumPeak":         0.0,
		"rgTrackGain":         0.0,
		"rgTrackPeak":         0.0,
		"createdAt":           nowStr,
		"updatedAt":           nowStr,
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
	var out []map[string]any
	for _, p := range pls {
		if role != "admin" && p.Owner != user && !p.Public { continue }
		songs, _ := h.DB.GetPlaylistSongs(p.ID)
		var size int64
		var dur int
		for _, s := range songs { size += s.FileSize; dur += s.Dur }

		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "comment": "", "duration": float64(dur), "size": size,
			"songCount": len(songs), "ownerName": p.Owner, "ownerId": genID(p.Owner),
			"public": p.Public, "path": "", "sync": false, "createdAt": nowStr, "updatedAt": nowStr,
			"rules": nil, "evaluatedAt": nil,
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
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
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
		"rules": nil, "evaluatedAt": nil,
	})
}

func (h *Handler) getPlaylistTracks(w http.ResponseWriter, r *http.Request, id, user string) {
	songs, _ := h.DB.GetPlaylistSongs(id)
	nowStr := time.Now().Format(time.RFC3339)
	var out []map[string]any
	
	for idx, s := range songs {
		album, artist, _, _, _ := cleanSongMeta(s)
		aID := genID(album + "|" + artist)
		arID := genID(artist)
		
		sObj := h.songObj(s, aID, arID, "", nowStr)
		sObj["id"] = strconv.Itoa(idx + 1)
		sObj["mediaFileId"] = s.ID
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
	start, _ := strconv.Atoi(r.URL.Query().Get("_start"))
	end, _ := strconv.Atoi(r.URL.Query().Get("_end"))
	if start > len(data) { start = len(data) }
	if end <= 0 || end > len(data) { end = len(data) }
	if start > end { start = end }
	return data[start:end]
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