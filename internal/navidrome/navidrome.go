package navidrome

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

func New(database *db.Holder, a *alist.Manager, c *config.Config) *Handler {
	return &Handler{DB: database, AList: a, Cfg: c}
}

var (
	sessionTokens = sync.Map{}
)

func (h *Handler) Mount(mux *http.ServeMux) {
	// 核心 Navidrome 私有接口
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/api/keepalive/keepalive", h.keepalive)
	mux.HandleFunc("/api/", h.route)

	// 独立接管 Navidrome 客户端调用的 Subsonic 兼容接口
	mux.HandleFunc("/rest/getCoverArt", h.restGetCoverArt)
	mux.HandleFunc("/rest/getCoverArt.view", h.restGetCoverArt)
	mux.HandleFunc("/rest/stream", h.restStream)
	mux.HandleFunc("/rest/stream.view", h.restStream)
}

// ==================== 认证与保活 ====================

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

// ==================== /api/ 路由分发 ====================

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

// ==================== 接管 Subsonic 接口：封面 ====================

func (h *Handler) restGetCoverArt(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	log.Printf("[navidrome] getCoverArt id=%s", id)

	// 【核心修复】剥离 Navidrome 客户端自作主张加的 mf- 前缀
	if strings.HasPrefix(id, "mf-") {
		id = strings.TrimPrefix(id, "mf-")
	}

	// 找歌
	if strings.HasPrefix(id, "go-") {
		if s, err := h.DB.GetSong(id); err == nil {
			if s.CoverPath != "" {
				serveLocalCover(w, r, s.CoverPath)
				return
			}
		}
	}

	// 找专辑
	if strings.HasPrefix(id, "al-") {
		songs, _ := h.DB.ListSongs()
		for _, s := range songs {
			if genID(s.Album+"|"+s.Artist) == id && s.CoverPath != "" {
				serveLocalCover(w, r, s.CoverPath)
				return
			}
		}
	}

	serveDefaultCover(w)
}

// ==================== 接管 Subsonic 接口：播放流 ====================

func (h *Handler) restStream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	user := r.URL.Query().Get("u") // 从参数获取用户名，供日志使用

	// 【核心修复】剥离 mf- 前缀
	if strings.HasPrefix(id, "mf-") {
		id = strings.TrimPrefix(id, "mf-")
	}

	s, err := h.DB.GetSong(id)
	if err != nil {
		http.Error(w, "Song not found", http.StatusNotFound)
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		return
	}

	if s.Fmt == "VIRTUAL" || strings.HasPrefix(s.ID, "lxv-") {
		http.Error(w, "不支持在线歌曲的 Navidrome 原生播放", http.StatusInternalServerError)
		return
	}

	providerID := s.ProviderID
	if providerID == "" {
		providerID = "alist-1"
	}
	
	rawURL, err := h.AList.Get(providerID).GetRawURL(s.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if u, err := url.Parse(rawURL); err == nil {
		rawURL = u.String()
	}
	log.Printf("[navidrome] stream user=%s %s -> 302 %s", user, id, rawURL)

	http.Redirect(w, r, rawURL, http.StatusFound)
}

// ==================== 图片读取辅助 ====================

func serveDefaultCover(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}
	w.Write(pngData)
}

func serveLocalCover(w http.ResponseWriter, r *http.Request, path string) {
	if _, err := os.Stat(path); err != nil {
		base := path
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		alt := filepath.Join(filepath.Dir(path), base)
		if _, err := os.Stat(alt); err == nil {
			path = alt
		}
	}
	f, err := os.Open(path)
	if err != nil {
		serveDefaultCover(w)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		serveDefaultCover(w)
		return
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// ==================== 原有辅助函数 ====================

// 从 subsonic.go 里拷贝过来的 splitArtists 等辅助函数保持不变
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

func paginate(data []map[string]any, r *http.Request) []map[string]any {
	start, _ := strconv.Atoi(r.URL.Query().Get("_start"))
	end, _ := strconv.Atoi(r.URL.Query().Get("_end"))
	if start > len(data) { start = len(data) }
	if end <= 0 || end > len(data) { end = len(data) }
	if start > end { start = end }
	return data[start:end]
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
