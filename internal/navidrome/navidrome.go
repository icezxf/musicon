package navidrome

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/meta"
)

type Handler struct {
	DB  *db.Holder
	Cfg *config.Config
}

func New(database *db.Holder, c *config.Config) *Handler {
	return &Handler{DB: database, Cfg: c}
}

var (
	sessionTokens = sync.Map{}
)

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/api/keepalive/keepalive", h.keepalive)
	
	// 精确注册 Navidrome 特有的子路径，避免与 api.go 的 /api/ 发生冲突崩溃
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
	w.Header().Set("Content-Type", "application/json")

	// 为了兼容 Navidrome 客户端的 star/unstar 请求，直接返回成功即可
	if p == "star" || p == "unstar" {
		w.Write([]byte("{}"))
		return
	}

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
	case p == "playlist":
		h.handlePlaylistRoot(w, r, username)
	case strings.HasPrefix(p, "playlist/"):
		h.handlePlaylistSub(w, r, p, username)
	default:
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
}

// ==================== 歌手与专辑 (部分省略，保持原有逻辑) ====================

func (h *Handler) getArtists(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	nameFilter := strings.ToLower(r.URL.Query().Get("name"))

	type artistAgg struct {
		name       string
		albumCount int
		songCount  int
		size       int64
	}
	artistMap := map[string]*artistAgg{}
	artistAlbums := map[string]map[string]bool{}

	for _, s := range songs {
		for _, name := range splitArtists(s.Artist) {
			if nameFilter != "" && !strings.Contains(strings.ToLower(name), nameFilter) {
				continue
			}
			if _, ok := artistMap[name]; !ok {
				artistMap[name] = &artistAgg{name: name}
				artistAlbums[name] = map[string]bool{}
			}
			artistMap[name].songCount++
			artistMap[name].size += s.FileSize
			if s.Album != "" {
				artistAlbums[name][s.Album] = true
			}
		}
	}

	nowStr := time.Now().Format(time.RFC3339)
	var out []map[string]any
	for name, agg := range artistMap {
		out = append(out, map[string]any{
			"id": genID(name), "name": name, "albumCount": len(artistAlbums[name]),
			"songCount": agg.songCount, "size": agg.size, "playCount": 0, "playDate": nil,
			"rating": 0, "starred": false, "starredAt": nil, "genres": nil,
			"fullText": " " + name, "orderArtistName": name, "externalUrl": "",
			"externalInfoUpdatedAt": nil, "createdAt": nowStr, "updatedAt": nowStr,
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
		for _, name := range splitArtists(s.Artist) {
			if genID(name) == id {
				artistName = name
				songCount++
				totalSize += s.FileSize
				if s.Album != "" {
					albumSet[s.Album] = true
				}
			}
		}
	}

	if artistName == "" {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		return
	}

	nowStr := time.Now().Format(time.RFC3339)
	json.NewEncoder(w).Encode(map[string]any{
		"id": id, "name": artistName, "albumCount": len(albumSet), "songCount": songCount,
		"size": totalSize, "playCount": 0, "playDate": nil, "rating": 0, "starred": false,
		"starredAt": nil, "genres": nil, "fullText": " " + artistName, "orderArtistName": artistName,
		"externalUrl": "", "externalInfoUpdatedAt": nil, "createdAt": nowStr, "updatedAt": nowStr,
	})
}

func (h *Handler) getAlbums(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	artistIDFilter := r.URL.Query().Get("artist_id")
	nameFilter := strings.ToLower(r.URL.Query().Get("name"))

	type albumAgg struct {
		name      string
		artist    string
		count     int
		dur       int
		size      int64
		minYear   int
		maxYear   int
		coverPath string
		paths     string
	}
	albumMap := map[string]*albumAgg{}

	for _, s := range songs {
		if s.Album == "" {
			continue
		}
		if artistIDFilter != "" && genID(s.Artist) != artistIDFilter {
			continue
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(s.Album), nameFilter) {
			continue
		}

		k := s.Album + "|" + s.Artist
		if _, ok := albumMap[k]; !ok {
			albumMap[k] = &albumAgg{name: s.Album, artist: s.Artist, minYear: 9999, coverPath: s.CoverPath, paths: s.Path}
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
			"artistId": arID, "artist": agg.artist, "albumArtistId": arID, "albumArtist": agg.artist,
			"allArtistIds": arID, "maxYear": agg.maxYear, "minYear": minYear, "date": strconv.Itoa(minYear),
			"maxOriginalYear": 0, "minOriginalYear": 0, "releases": 1, "compilation": false,
			"songCount": agg.count, "duration": agg.dur, "size": agg.size, "genre": "", "genres": nil,
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
		if genID(s.Album+"|"+s.Artist) == id {
			if outAlbum == nil {
				arID := genID(s.Artist)
				outAlbum = map[string]any{
					"id": id, "name": s.Album, "embedArtPath": s.CoverPath, "artistId": arID,
					"artist": s.Artist, "albumArtistId": arID, "albumArtist": s.AlbumArtist,
					"allArtistIds": arID, "maxYear": s.Year, "minYear": s.Year, "date": strconv.Itoa(s.Year),
					"maxOriginalYear": 0, "minOriginalYear": 0, "releases": 1, "compilation": false,
					"genre": s.Genre, "genres": nil, "fullText": " " + s.Album + " " + s.Artist,
					"orderAlbumName": s.Album, "orderAlbumArtistName": s.Artist, "paths": s.Path,
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
	outAlbum["duration"] = totalDur
	outAlbum["size"] = totalSize
	json.NewEncoder(w).Encode(outAlbum)
}

// ==================== 歌曲列表 ====================

func (h *Handler) getSongs(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	albumIDFilter := r.URL.Query().Get("album_id")
	titleFilter := strings.ToLower(r.URL.Query().Get("title"))

	var filtered []db.Song
	for _, s := range songs {
		if albumIDFilter != "" && genID(s.Album+"|"+s.Artist) != albumIDFilter {
			continue
		}
		if titleFilter != "" && !strings.Contains(strings.ToLower(s.Title), titleFilter) {
			continue
		}
		filtered = append(filtered, s)
	}

	// 简易排序
	sortParam := r.URL.Query().Get("_sort")
	orderParam := strings.ToUpper(r.URL.Query().Get("_order"))
	sort.Slice(filtered, func(i, j int) bool {
		var less bool
		switch sortParam {
		case "max_year": less = filtered[i].Year < filtered[j].Year
		case "album": less = filtered[i].Album < filtered[j].Album
		case "title": fallthrough
		default: less = filtered[i].Title < filtered[j].Title
		}
		if orderParam == "DESC" { return !less }
		return less
	})

	nowStr := time.Now().Format(time.RFC3339)
	var out []map[string]any
	for _, s := range filtered {
		aID := genID(s.Album + "|" + s.Artist)
		arID := genID(s.Artist)

		lyricsStr := ""
		if s.Lyrics != "" {
			lines := meta.ParseLRC(s.Lyrics)
			var lrcLines []map[string]any
			hasTs := false
			for _, ln := range lines {
				m := map[string]any{"value": ln.Value}
				if ln.Start >= 0 { m["start"] = ln.Start; hasTs = true }
				lrcLines = append(lrcLines, m)
			}
			b, _ := json.Marshal([]map[string]any{{"lang": "xxx", "line": lrcLines, "synced": hasTs}})
			lyricsStr = string(b)
		}

		out = append(out, h.songObj(s, aID, arID, lyricsStr, nowStr))
	}
	total := len(out)
	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(total))
	json.NewEncoder(w).Encode(out)
}

// ==================== 歌单操作 ====================

func (h *Handler) handlePlaylistRoot(w http.ResponseWriter, r *http.Request, user string) {
	switch r.Method {
	case http.MethodGet:
		h.getPlaylists(w, r, user)
	case http.MethodPost:
		// 创建歌单
		var req struct {
			Name    string `json:"name"`
			Comment string `json:"comment"`
			Public  bool   `json:"public"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		id, err := h.DB.CreatePlaylist(req.Name, req.Comment, user, req.Public)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
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
		// /api/playlist/[id]
		switch r.Method {
		case http.MethodGet:
			h.getPlaylistDetail(w, r, id, user)
		case http.MethodDelete:
			h.DB.DeletePlaylist(id)
			w.Write([]byte("{}"))
		case http.MethodPut:
			var req struct {
				Name    string `json:"name"`
				Comment string `json:"comment"`
				Public  bool   `json:"public"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Name != "" { h.DB.DB.Exec(`UPDATE playlists SET name=? WHERE id=?`, req.Name, id) }
			if req.Comment != "" { h.DB.DB.Exec(`UPDATE playlists SET comment=? WHERE id=?`, req.Comment, id) }
			h.DB.DB.Exec(`UPDATE playlists SET public=? WHERE id=?`, req.Public, id)
			w.Write([]byte("{}"))
		}
	} else if len(parts) == 3 && parts[2] == "tracks" {
		// /api/playlist/[id]/tracks
		switch r.Method {
		case http.MethodGet:
			h.getPlaylistTracks(w, r, id, user)
		case http.MethodPost:
			var req struct { Ids string `json:"ids"` }
			json.NewDecoder(r.Body).Decode(&req)
			if req.Ids != "" {
				songIDs := strings.Split(req.Ids, ",")
				h.DB.AddSongsToPlaylist(id, songIDs)
			}
			w.Write([]byte("{}"))
		case http.MethodDelete:
			// 移除歌曲（注意：客户端传的 id 是行号，从 1 开始）
			q := r.URL.Query()
			rowIDs := q["id"]
			if len(rowIDs) > 0 {
				songs, _ := h.DB.GetPlaylistSongs(id)
				var toRemove []string
				for _, rowStr := range rowIDs {
					if idx, err := strconv.Atoi(rowStr); err == nil && idx > 0 && idx <= len(songs) {
						toRemove = append(toRemove, songs[idx-1].ID)
					}
				}
				if len(toRemove) > 0 {
					h.DB.RemoveSongsFromPlaylist(id, toRemove)
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
		if role != "admin" && p.Owner != user && !p.Public {
			continue
		}
		songs, _ := h.DB.GetPlaylistSongs(p.ID)
		var size int64
		var dur int
		for _, s := range songs { size += s.FileSize; dur += s.Dur }

		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "comment": "", "duration": dur, "size": size,
			"songCount": len(songs), "ownerName": p.Owner, "ownerId": genID(p.Owner),
			"public": p.Public, "path": "", "sync": false, "createdAt": nowStr, "updatedAt": nowStr,
			"rules": nil, "evaluatedAt": nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
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
	role := h.DB.GetUserRole(user)
	if role != "admin" && p.Owner != user && !p.Public {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		return
	}
	songs, _ := h.DB.GetPlaylistSongs(id)
	var size int64
	var dur int
	for _, s := range songs { size += s.FileSize; dur += s.Dur }

	nowStr := time.Now().Format(time.RFC3339)
	json.NewEncoder(w).Encode(map[string]any{
		"id": p.ID, "name": p.Name, "comment": "", "duration": dur, "size": size,
		"songCount": len(songs), "ownerName": p.Owner, "ownerId": genID(p.Owner),
		"public": p.Public, "path": "", "sync": false, "createdAt": nowStr, "updatedAt": nowStr,
		"rules": nil, "evaluatedAt": nil,
	})
}

func (h *Handler) getPlaylistTracks(w http.ResponseWriter, r *http.Request, id, user string) {
	songs, _ := h.DB.GetPlaylistSongs(id)
	nowStr := time.Now().Format(time.RFC3339)
	
	// 排序
	sortParam := r.URL.Query().Get("_sort")
	orderParam := strings.ToUpper(r.URL.Query().Get("_order"))
	// Navidrome 歌单默认按加入顺序排。如果传了排序参数可以加在这
	_ = sortParam
	_ = orderParam

	var out []map[string]any
	for idx, s := range songs {
		aID := genID(s.Album + "|" + s.Artist)
		arID := genID(s.Artist)

		lyricsStr := ""
		if s.Lyrics != "" {
			lines := meta.ParseLRC(s.Lyrics)
			var lrcLines []map[string]any
			hasTs := false
			for _, ln := range lines {
				m := map[string]any{"value": ln.Value}
				if ln.Start >= 0 { m["start"] = ln.Start; hasTs = true }
				lrcLines = append(lrcLines, m)
			}
			b, _ := json.Marshal([]map[string]any{{"lang": "xxx", "line": lrcLines, "synced": hasTs}})
			lyricsStr = string(b)
		}
		
		sObj := h.songObj(s, aID, arID, lyricsStr, nowStr)
		// 歌单特有字段
		sObj["id"] = strconv.Itoa(idx + 1) // 必须是行号
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

func (h *Handler) songObj(s db.Song, aID, arID string, lyricsStr string, nowStr string) map[string]any {
	return map[string]any{
		"id": s.ID, "path": s.Path, "title": s.Title, "album": s.Album,
		"artistId": arID, "artist": s.Artist, "albumArtistId": arID, "albumArtist": s.AlbumArtist,
		"albumId": aID, "hasCoverArt": s.CoverPath != "", "trackNumber": s.TrackNumber,
		"discNumber": s.DiscNumber, "year": s.Year, "date": strconv.Itoa(s.Year),
		"originalYear": 0, "releaseYear": 0, "size": s.FileSize, "suffix": s.Fmt,
		"duration": s.Dur, "bitRate": s.Bitrate, "channels": s.Channels, "genre": s.Genre,
		"genres": nil, "fullText": " " + s.Title + " " + s.Album + " " + s.Artist,
		"orderTitle": s.Title, "orderAlbumName": s.Album, "orderArtistName": s.Artist,
		"orderAlbumArtistName": s.AlbumArtist, "compilation": false, "lyrics": lyricsStr,
		"rgAlbumGain": 0.0, "rgAlbumPeak": 0.0, "rgTrackGain": 0.0, "rgTrackPeak": 0.0,
		"playCount": 0, "playDate": nil, "rating": 0, "starred": false, "starredAt": nil,
		"bookmarkPosition": 0, "createdAt": nowStr, "updatedAt": nowStr,
	}
}

func (h *Handler) visibleSongs(username string) []db.Song {
	songs, _ := h.DB.ListSongs()
	role := h.DB.GetUserRole(username)
	if role == "admin" {
		return songs
	}
	allowed := h.DB.GetUserSources(username)
	set := map[string]bool{}
	for _, id := range allowed {
		set[id] = true
	}
	out := make([]db.Song, 0, len(songs))
	for _, s := range songs {
		if s.SourceID == "" || set[s.SourceID] {
			out = append(out, s)
		}
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
	if s == "" {
		return nil
	}
	parts := splitArtistRe.Split(s, -1)
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}
