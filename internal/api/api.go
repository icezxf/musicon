package api

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/disintegration/imaging"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/lx"
	"github.com/icezxf/musicon-go/internal/scan"
	"github.com/icezxf/musicon-go/internal/settings"
)

type Handler struct {
	DB       *db.Holder
	AList    *alist.Client
	LX       *lx.Client
	Cfg      *config.Config
	Settings *settings.Manager
	Scan     *scan.Scanner
}

func New(database *db.Holder, a *alist.Client, l *lx.Client, c *config.Config, s *settings.Manager, bgWG *sync.WaitGroup) *Handler {
	sc := &scan.Scanner{DB: database, AList: a, CoverDir: c.DataDir + "/covers", WG: bgWG}
	return &Handler{DB: database, AList: a, LX: l, Cfg: c, Settings: s, Scan: sc}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	// 公开接口
	mux.HandleFunc("/api/web-auth/login", h.login)
	mux.HandleFunc("/api/web-auth/logout", h.logout)
	mux.HandleFunc("/api/web-auth/session", h.session)
	mux.HandleFunc("/api/artist/image", h.artistImage)
	mux.HandleFunc("/api/covers/thumb/", h.coverThumb)
	mux.HandleFunc("GET /api/songs/{id}/stream", h.songStream)

	// 需要会话
	mux.HandleFunc("/api/", h.authWrap(h.handle))
}

func (h *Handler) authWrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.CheckWebSession(h.DB.DB, r); !ok {
			writeJSON(w, 401, map[string]any{"detail": "Unauthorized"})
			return
		}
		next(w, r)
	}
}

// ---------- 公开接口 ----------

func (h *Handler) songStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, 400, map[string]any{"detail": "missing id"})
		return
	}
	s, err := h.DB.GetSong(id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "Song not found"})
		return
	}
	u, err := h.AList.GetRawURL(s.Path)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u, http.StatusFound)
}

// ---------- 认证 ----------

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	dbUser := h.Settings.Get("web_user")
	dbPass := h.Settings.Get("web_pass")
	if dbUser == "" {
		dbUser = h.Cfg.WebUser
	}
	if dbPass == "" {
		dbPass = h.Cfg.WebPass
	}

	if body.Username != dbUser || body.Password != dbPass {
		writeJSON(w, 401, map[string]any{"detail": "Invalid credentials"})
		return
	}
	token, err := auth.CreateWebSession(h.DB.DB, body.Username)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.WebCookieName, Value: token, Path: "/",
		MaxAge: 30 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil,
	})
	writeJSON(w, 200, map[string]any{"ok": true, "username": body.Username})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.WebCookieName); err == nil {
		auth.DeleteWebSession(h.DB.DB, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.WebCookieName, Value: "", MaxAge: -1, Path: "/"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.CheckWebSession(h.DB.DB, r)
	if !ok {
		writeJSON(w, 401, map[string]any{"detail": "Unauthorized"})
		return
	}
	writeJSON(w, 200, map[string]any{"username": user})
}

// ---------- 路由分发 ----------

func (h *Handler) handle(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(r.URL.Path, "/")
	if p == "" {
		p = "/"
	}

	switch {
	// 歌曲
	case p == "/api/songs" && r.Method == "GET":
		h.listSongs(w, r)
	case p == "/api/songs/top" && r.Method == "GET":
		h.listSongs(w, r)
	case p == "/api/songs" && r.Method == "DELETE":
		h.deleteSongs(w, r)
	case strings.HasPrefix(p, "/api/songs/") && r.Method == "GET":
		h.getSong(w, r)
	case strings.HasPrefix(p, "/api/songs/") && r.Method == "PUT":
		h.updateSong(w, r)

	// 歌单
	case p == "/api/playlists" && r.Method == "GET":
		h.listPlaylists(w, r)
	case p == "/api/playlists" && r.Method == "POST":
		h.createPlaylist(w, r)
	case strings.HasPrefix(p, "/api/playlists/") && r.Method == "GET":
		h.getPlaylist(w, r)
	case strings.HasPrefix(p, "/api/playlists/") && r.Method == "PATCH":
		h.patchPlaylist(w, r)
	case strings.HasPrefix(p, "/api/playlists/") && r.Method == "DELETE":
		h.deletePlaylist(w, r)

	// Subsonic 用户
	case p == "/api/subsonic/users" && r.Method == "GET":
		h.listSubUsers(w, r)
	case p == "/api/subsonic/users" && r.Method == "POST":
		h.createSubUser(w, r)
	case strings.HasPrefix(p, "/api/subsonic/users/") && r.Method == "PATCH":
		h.updateSubUser(w, r)
	case strings.HasPrefix(p, "/api/subsonic/users/") && r.Method == "DELETE":
		h.deleteSubUser(w, r)

	// 艺术家
	case p == "/api/artist/photo" && r.Method == "GET":
		h.artistPhoto(w, r)

	// AList 目录树
	case p == "/api/alist/list" && r.Method == "GET":
		h.alistList(w, r)

	// 元数据搜索/歌词
	case p == "/api/music/metadata-search" && r.Method == "GET":
		h.metadataSearch(w, r)
	case p == "/api/music/metadata-lyric" && r.Method == "GET":
		h.metadataLyric(w, r)

	// 任务
	case p == "/api/tasks" && r.Method == "GET":
		h.listTasks(w, r)
	case p == "/api/scan" && r.Method == "POST":
		h.startScan(w, r)

	// 设置
	case p == "/api/settings" && r.Method == "GET":
		h.getSettings(w, r)
	case p == "/api/settings" && r.Method == "POST":
		h.saveSettings(w, r)
	case p == "/api/settings/storage/test" && r.Method == "POST":
		h.testStorage(w, r)

	// 统计
	case p == "/api/stats" && r.Method == "GET":
		h.stats(w, r)
	case p == "/api/plays/recent" && r.Method == "GET":
		h.recentPlays(w, r)

	// LX
	case p == "/api/lx/status" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{"running": true, "url": h.Settings.GetLXURL()})

	default:
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
	}
}

// ---------- 歌曲 ----------

func (h *Handler) listSongs(w http.ResponseWriter, r *http.Request) {
	songs, err := h.DB.ListSongs()
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"songs": songs})
}

func (h *Handler) getSong(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/songs/")
	s, err := h.DB.GetSong(id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
		return
	}
	writeJSON(w, 200, map[string]any{"song": s})
}

func (h *Handler) updateSong(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/songs/")
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	fields := map[string]string{}
	for _, k := range []string{"title", "artist", "album", "album_artist", "genre", "lyrics", "cover_art"} {
		if v, ok := body[k]; ok {
			if s, ok := v.(string); ok {
				fields[k] = s
			}
		}
	}
	if err := h.DB.UpdateSong(id, fields); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"message": "已更新"})
}

func (h *Handler) deleteSongs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SongIDs []string `json:"song_ids"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if len(body.SongIDs) == 0 {
		writeJSON(w, 400, map[string]any{"detail": "song_ids required"})
		return
	}
	if err := h.DB.DeleteSongs(body.SongIDs); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"message": fmt.Sprintf("已删除 %d 首", len(body.SongIDs))})
}

// ---------- 歌单 ----------

func (h *Handler) listPlaylists(w http.ResponseWriter, r *http.Request) {
	pls, _ := h.DB.ListPlaylists()
	writeJSON(w, 200, map[string]any{"playlists": pls})
}

func (h *Handler) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Comment string   `json:"comment"`
		SongIDs []string `json:"song_ids"`
		Public  bool     `json:"public"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		writeJSON(w, 400, map[string]any{"message": "歌单名不能为空"})
		return
	}
	id, err := h.DB.CreatePlaylist(body.Name, body.Comment, "admin", body.Public)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	if len(body.SongIDs) > 0 {
		h.DB.AddSongsToPlaylist(id, body.SongIDs)
	}
	writeJSON(w, 200, map[string]any{"message": "歌单已创建", "id": id})
}

func (h *Handler) getPlaylist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/playlists/")
	p, err := h.DB.GetPlaylist(id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
		return
	}
	songs, _ := h.DB.GetPlaylistSongs(id)
	writeJSON(w, 200, map[string]any{
		"playlist": map[string]any{
			"id": p.ID, "name": p.Name, "comment": p.Comment,
			"owner": p.Owner, "is_readonly": p.Readonly,
			"song_count": p.Count, "entries": songs,
		},
	})
}

// 修复：错误不再被吞掉
func (h *Handler) patchPlaylist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/playlists/")
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	if v, ok := body["song_ids_to_add"].([]any); ok {
		if err := h.DB.AddSongsToPlaylist(id, toStrings(v)); err != nil {
			writeJSON(w, 500, map[string]any{"detail": err.Error()})
			return
		}
	}
	if v, ok := body["song_ids_to_remove"].([]any); ok {
		if err := h.DB.RemoveSongsFromPlaylist(id, toStrings(v)); err != nil {
			writeJSON(w, 500, map[string]any{"detail": err.Error()})
			return
		}
	}
	writeJSON(w, 200, map[string]any{"message": "歌单已更新"})
}

func (h *Handler) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/playlists/")
	if err := h.DB.DeletePlaylist(id); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"message": "歌单已删除"})
}

// ---------- Subsonic 用户 ----------

func (h *Handler) listSubUsers(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.DB.DB.Query(`SELECT username, role, enabled, created_at FROM subsonic_users ORDER BY username`)
	defer rows.Close()
	users := []map[string]any{}
	for rows.Next() {
		var u, role, created string
		var en int
		rows.Scan(&u, &role, &en, &created)
		users = append(users, map[string]any{"username": u, "role": role, "enabled": en == 1, "created_at": created})
	}
	writeJSON(w, 200, map[string]any{"users": users})
}

func (h *Handler) createSubUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Username == "" || body.Password == "" {
		writeJSON(w, 400, map[string]any{"detail": "用户名和密码必填"})
		return
	}
	if body.Role == "" {
		body.Role = "user"
	}
	_, err := h.DB.DB.Exec(`INSERT INTO subsonic_users(username, password, role, enabled) VALUES(?,?,?,1)`,
		body.Username, body.Password, body.Role)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"message": "账号已创建", "username": body.Username})
}

func (h *Handler) updateSubUser(w http.ResponseWriter, r *http.Request) {
	u, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/subsonic/users/"))
	var body struct {
		Password string `json:"password"`
		Role     string `json:"role"`
		Enabled  *bool  `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Password != "" {
		h.DB.DB.Exec(`UPDATE subsonic_users SET password=? WHERE username=?`, body.Password, u)
	}
	if body.Role != "" {
		h.DB.DB.Exec(`UPDATE subsonic_users SET role=? WHERE username=?`, body.Role, u)
	}
	if body.Enabled != nil {
		en := 0
		if *body.Enabled {
			en = 1
		}
		h.DB.DB.Exec(`UPDATE subsonic_users SET enabled=? WHERE username=?`, en, u)
	}
	writeJSON(w, 200, map[string]any{"message": "已更新"})
}

func (h *Handler) deleteSubUser(w http.ResponseWriter, r *http.Request) {
	u, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/subsonic/users/"))
	h.DB.DB.Exec(`DELETE FROM subsonic_users WHERE username=?`, u)
	writeJSON(w, 200, map[string]any{"message": "已删除"})
}

// ---------- 艺术家 ----------

func (h *Handler) artistPhoto(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	d, err := h.LX.GetSingerDetail(name)
	if err != nil {
		writeJSON(w, 200, map[string]any{"name": name, "pic": "", "bio": "", "source": "none"})
		return
	}
	writeJSON(w, 200, map[string]any{"name": d.Name, "pic": d.Pic, "bio": d.Bio, "source": d.Source})
}

func (h *Handler) artistImage(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	d, err := h.LX.GetSingerDetail(name)
	if err != nil || d.Pic == "" {
		http.Error(w, "not found", 404)
		return
	}
	client := &http.Client{Timeout: 15e9}
	req, err := http.NewRequest("GET", d.Pic, nil)
	if err != nil {
		http.Error(w, "bad url", 400)
		return
	}
	req.Header.Set("Referer", "https://y.qq.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	io.Copy(w, io.LimitReader(resp.Body, 10<<20))
}

// ---------- 封面缩略图 ----------

func (h *Handler) coverThumb(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/covers/thumb/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "bad path", 400)
		return
	}
	size, err := strconv.Atoi(parts[0])
	if err != nil || size <= 0 || size > 2000 {
		size = 96
	}
	// 用 Base 去掉路径成分，再拒绝 "." 和 ".."
	filename := filepath.Base(parts[1])
	if filename == "." || filename == ".." || filename == "" {
		http.Error(w, "bad filename", 400)
		return
	}
	for _, c := range filename {
		if !(c == '.' || c == '-' || c == '_' ||
			(c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z')) {
			http.Error(w, "bad filename", 400)
			return
		}
	}
	srcPath := filepath.Join(h.Cfg.DataDir, "covers", filename)
	if _, err := os.Stat(srcPath); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	thumbDir := filepath.Join(h.Cfg.DataDir, "covers_thumb")
	os.MkdirAll(thumbDir, 0755)
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	thumbPath := filepath.Join(thumbDir, fmt.Sprintf("%s_%d.jpg", base, size))
	if fi, err := os.Stat(thumbPath); err == nil && fi.Size() > 0 {
		http.ServeFile(w, r, thumbPath)
		return
	}
	f, err := os.Open(srcPath)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		http.ServeFile(w, r, srcPath)
		return
	}
	thumb := imaging.Fill(src, size, size, imaging.Center, imaging.Lanczos)
	if err := imaging.Save(thumb, thumbPath, imaging.JPEGQuality(85)); err != nil {
		http.Error(w, "save failed", 500)
		return
	}
	http.ServeFile(w, r, thumbPath)
}

// ---------- AList 目录树 ----------

func (h *Handler) alistList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	entries, err := h.AList.List(path)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	dirs := []map[string]any{}
	for _, e := range entries {
		if e.IsDir {
			dirs = append(dirs, map[string]any{
				"name": e.Name,
				"path": strings.TrimRight(path, "/") + "/" + e.Name,
			})
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i]["name"].(string) < dirs[j]["name"].(string)
	})
	writeJSON(w, 200, map[string]any{
		"path": path,
		"dirs": dirs,
	})
}

// ---------- 元数据搜索 / 歌词（转 LX） ----------

func (h *Handler) metadataSearch(w http.ResponseWriter, r *http.Request) {
	kw := r.URL.Query().Get("keyword")
	if kw == "" {
		writeJSON(w, 400, map[string]any{"detail": "keyword required"})
		return
	}
	lxURL := h.Settings.GetLXURL()
	u := strings.TrimRight(lxURL, "/") + "/api/music/search?source=tx&name=" + url.QueryEscape(kw) + "&type=song"
	client := &http.Client{Timeout: 15e9}
	resp, err := client.Get(u)
	if err != nil {
		writeJSON(w, 502, map[string]any{"detail": err.Error()})
		return
	}
	defer resp.Body.Close()
	var data any
	json.NewDecoder(resp.Body).Decode(&data)
	writeJSON(w, 200, map[string]any{"results": data})
}

func (h *Handler) metadataLyric(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	songID := r.URL.Query().Get("songId")
	if source == "" || songID == "" {
		writeJSON(w, 400, map[string]any{"detail": "source and songId required"})
		return
	}
	lxURL := h.Settings.GetLXURL()
	u := strings.TrimRight(lxURL, "/") + "/api/music/lyric?source=" + url.QueryEscape(source) + "&songId=" + url.QueryEscape(songID)
	client := &http.Client{Timeout: 15e9}
	resp, err := client.Get(u)
	if err != nil {
		writeJSON(w, 502, map[string]any{"detail": err.Error()})
		return
	}
	defer resp.Body.Close()
	var data any
	json.NewDecoder(resp.Body).Decode(&data)
	writeJSON(w, 200, data)
}

// ---------- 任务 ----------

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	ts, _ := h.DB.ListScanTasks(50)
	writeJSON(w, 200, map[string]any{"tasks": ts})
}

func (h *Handler) startScan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path   string `json:"path"`
		Mode   string `json:"mode"`
		Source string `json:"source"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" {
		writeJSON(w, 400, map[string]any{"detail": "path 必填"})
		return
	}
	h.Settings.Set("last_scan_path", body.Path)
	taskID, err := h.DB.CreateScanTask(body.Path, body.Mode, body.Source)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	go h.Scan.Run(taskID, body.Path, body.Source)
	writeJSON(w, 200, map[string]any{"message": "扫描任务已创建", "task_id": taskID})
}

// ---------- 设置 ----------

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	all := h.Settings.GetAll()
	for _, k := range []string{"alist_password", "web_pass", "subsonic_pass"} {
		if v, ok := all[k]; ok && v != "" {
			all["has_"+k] = "true"
			all[k] = ""
		}
	}
	writeJSON(w, 200, map[string]any{"settings": all})
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)

	save := func(k string, v any) {
		switch x := v.(type) {
		case string:
			if x == "" && (k == "alist_password" || k == "web_pass" || k == "subsonic_pass") {
				return
			}
			h.Settings.Set(k, x)
		case bool:
			if x {
				h.Settings.Set(k, "true")
			} else {
				h.Settings.Set(k, "false")
			}
		case float64:
			h.Settings.Set(k, fmt.Sprintf("%v", x))
		case map[string]any, []any:
			b, _ := json.Marshal(x)
			h.Settings.Set(k, string(b))
		}
	}
	for k, v := range body {
		save(k, v)
	}
	if _, ok := body["storage_providers"]; ok {
		h.Settings.Set("alist_token", "")
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) testStorage(w http.ResponseWriter, r *http.Request) {
	entries, err := h.AList.List("/")
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": map[string]any{
			"message": err.Error(),
			"hint":    "检查 AList 地址和账号密码",
		}})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("AList 连通，根目录列出 %d 项", len(entries)),
		"count":   len(entries),
	})
}

// ---------- 统计 ----------

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"total_songs": h.DB.CountSongs()})
}

func (h *Handler) recentPlays(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.DB.RecentPlays(10)
	writeJSON(w, 200, map[string]any{"plays": rows})
}

// ---------- 工具 ----------

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}

func toStrings(a []any) []string {
	out := make([]string, 0, len(a))
	for _, v := range a {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
