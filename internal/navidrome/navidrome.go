package navidrome

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
)

type Handler struct {
	DB  *db.Holder
	Cfg *config.Config
}

func New(database *db.Holder, c *config.Config) *Handler {
	return &Handler{DB: database, Cfg: c}
}

// 内存中的 Token 缓存
var (
	sessionTokens = sync.Map{}
)

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/api/", h.route)
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

	// 复用 Subsonic 的认证逻辑
	if !auth.VerifySubsonic(h.DB.DB, req.Username, req.Password, "", "") {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	// 生成一个简单的 Token 并缓存
	hToken := md5.Sum([]byte(req.Username + req.Password))
	token := hex.EncodeToString(hToken[:])
	clientID := "musicon-" + token[:8]
	sessionTokens.Store(token, req.Username)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":             clientID,
		"isAdmin":        true,
		"name":           req.Username,
		"token":          token,
		"username":       req.Username,
		"subsonicSalt":   "123456",
		"subsonicToken":  "dummy",
		"lastFMApiKey":   "",
	})
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

	switch {
	case p == "artist":
		h.getArtists(w, r, username)
	case strings.HasPrefix(p, "artist/"):
		id := strings.TrimPrefix(p, "artist/")
		h.getArtistDetail(w, r, id, username)
	case p == "album":
		h.getAlbums(w, r, username)
	case strings.HasPrefix(p, "album/"):
		id := strings.TrimPrefix(p, "album/")
		h.getAlbumDetail(w, r, id, username)
	case p == "song":
		h.getSongs(w, r, username)
	default:
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
}

// ==================== 数据逻辑 ====================

func (h *Handler) getArtists(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)

	// 聚合歌手
	type artistAgg struct {
		name       string
		albumCount int
		songCount  int
	}
	artistMap := map[string]*artistAgg{}
	artistAlbums := map[string]map[string]bool{}

	for _, s := range songs {
		names := splitArtists(s.Artist)
		for _, name := range names {
			if _, ok := artistMap[name]; !ok {
				artistMap[name] = &artistAgg{name: name}
				artistAlbums[name] = map[string]bool{}
			}
			artistMap[name].songCount++
			if s.Album != "" {
				artistAlbums[name][s.Album] = true
			}
		}
	}

	var out []map[string]any
	for name, agg := range artistMap {
		id := genID(name)
		out = append(out, map[string]any{
			"id":         id,
			"name":       name,
			"albumCount": len(artistAlbums[name]),
			"songCount":  agg.songCount,
			"playCount":  0,
			"rating":     0,
			"starred":    false,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i]["name"].(string) < out[j]["name"].(string)
	})

	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(len(out)))
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) getArtistDetail(w http.ResponseWriter, r *http.Request, id, user string) {
	// 简略版：直接复用 getArtist 逻辑查数据
	h.getArtists(w, r, user)
}

func (h *Handler) getAlbums(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)

	type albumAgg struct {
		name   string
		artist string
		count  int
		dur    int
		size   int64
	}
	albumMap := map[string]*albumAgg{}

	for _, s := range songs {
		if s.Album == "" {
			continue
		}
		k := s.Album + "|" + s.Artist
		if _, ok := albumMap[k]; !ok {
			albumMap[k] = &albumAgg{name: s.Album, artist: s.Artist}
		}
		albumMap[k].count++
		albumMap[k].dur += s.Dur
		albumMap[k].size += s.FileSize
	}

	var out []map[string]any
	for _, agg := range albumMap {
		id := genID(agg.name + "|" + agg.artist)
		arID := genID(agg.artist)
		out = append(out, map[string]any{
			"id":          id,
			"name":        agg.name,
			"artistId":    arID,
			"artist":      agg.artist,
			"albumArtistId": arID,
			"albumArtist": agg.artist,
			"allArtistIds": arID,
			"songCount":   agg.count,
			"duration":    agg.dur,
			"size":        agg.size,
			"playCount":   0,
			"rating":      0,
			"starred":     false,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i]["name"].(string) < out[j]["name"].(string)
	})

	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(len(out)))
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) getAlbumDetail(w http.ResponseWriter, r *http.Request, id, user string) {
	h.getAlbums(w, r, user)
}

func (h *Handler) getSongs(w http.ResponseWriter, r *http.Request, user string) {
	songs := h.visibleSongs(user)
	
	// 简单过滤
	albumID := r.URL.Query().Get("album_id")
	
	var out []map[string]any
	for _, s := range songs {
		sID := s.ID
		aID := genID(s.Album + "|" + s.Artist)
		arID := genID(s.Artist)
		
		if albumID != "" && albumID != aID {
			continue
		}

		out = append(out, map[string]any{
			"id":           sID,
			"path":         s.Path,
			"title":        s.Title,
			"album":        s.Album,
			"artistId":     arID,
			"artist":       s.Artist,
			"albumArtistId": arID,
			"albumArtist":  s.AlbumArtist,
			"albumId":      aID,
			"hasCoverArt":  true,
			"trackNumber":  s.TrackNumber,
			"discNumber":   s.DiscNumber,
			"year":         s.Year,
			"size":         s.FileSize,
			"suffix":       s.Fmt,
			"duration":     s.Dur,
			"bitRate":      s.Bitrate,
			"channels":     s.Channels,
			"genre":        s.Genre,
			"playCount":    0,
			"rating":       0,
			"starred":      false,
			"compilation":  false,
		})
	}

	out = paginate(out, r)
	w.Header().Set("x-total-count", strconv.Itoa(len(out)))
	json.NewEncoder(w).Encode(out)
}

// ==================== 辅助函数 ====================

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
	
	if start > len(data) {
		start = len(data)
	}
	if end <= 0 || end > len(data) {
		end = len(data)
	}
	if start > end {
		start = end
	}
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
