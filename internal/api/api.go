package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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

func New(database *db.Holder, a *alist.Client, l *lx.Client, c *config.Config, s *settings.Manager) *Handler {
	sc := &scan.Scanner{DB: database, AList: a, CoverDir: c.DataDir + "/covers"}
	return &Handler{DB: database, AList: a, LX: l, Cfg: c, Settings: s, Scan: sc}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/api/web-auth/login", h.login)
	mux.HandleFunc("/api/web-auth/logout", h.logout)
	mux.HandleFunc("/api/web-auth/session", h.session)
	mux.HandleFunc("/api/artist/image", h.artistImage)
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

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// 优先用 DB 里的，回退到环境变量
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

func (h *Handler) handle(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/songs" && r.Method == "GET":
		h.listSongs(w, r)

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

	case p == "/api/subsonic/users" && r.Method == "GET":
		h.listSubUsers(w, r)
	case p == "/api/subsonic/users" && r.Method == "POST":
		h.createSubUser(w, r)
	case strings.HasPrefix(p, "/api/subsonic/users/") && r.Method == "PATCH":
		h.updateSubUser(w, r)
	case strings.HasPrefix(p, "/api/subsonic/users/") && r.Method == "DELETE":
		h.deleteSubUser(w, r)

	case p == "/api/artist/photo" && r.Method == "GET":
		h.artistPhoto(w, r)

	case p == "/api/tasks" && r.Method == "GET":
		h.listTasks(w, r)
	case p == "/api/scan" && r.Method == "POST":
		h.startScan(w, r)

	case p == "/api/settings" && r.Method == "GET":
		h.getSettings(w, r)
	case p == "/api/settings" && r.Method == "POST":
		h.saveSettings(w, r)
	case p == "/api/settings/storage/test" && r.Method == "POST":
		h.testStorage(w, r)

	case p == "/api/stats" && r.Method == "GET":
		h.stats(w, r)
	case p == "/api/plays/recent" && r.Method == "GET":
		h.recentPlays(w, r)

	case p == "/api/lx/status" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{"running": true, "url": h.Settings.GetLXURL()})

	default:
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
	}
}

// ---- 歌曲 / 歌单 ----

func (h *Handler) listSongs(w http.ResponseWriter, r *http.Request) {
	songs, err := h.DB.ListSongs()
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"songs": songs})
}

func (h *Handler) listPlaylists(w http.ResponseWriter, r *http.Request) {
	pls, _ := h.DB.ListPlaylists()
	writeJSON(w, 200, map[string]any{"playlists": pls})
}

func (h *Handler) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string   `json:"name"`
		Comment string   `json:"comment"`
		SongIDs []string `json:"song_ids"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		writeJSON(w, 400, map[string]any{"message": "歌单名不能为空"})
		return
	}
	id, err := h.DB.CreatePlaylist(body.Name, body.Comment, "admin")
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

func (h *Handler) patchPlaylist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/playlists/")
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	if v, ok := body["song_ids_to_add"].([]any); ok {
		h.DB.AddSongsToPlaylist(id, toStrings(v))
	}
	if v, ok := body["song_ids_to_remove"].([]any); ok {
		h.DB.RemoveSongsFromPlaylist(id, toStrings(v))
	}
	writeJSON(w, 200, map[string]any{"message": "歌单已更新"})
}

func (h *Handler) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/playlists/")
	h.DB.DeletePlaylist(id)
	writeJSON(w, 200, map[string]any{"message": "歌单已删除"})
}

// ---- Subsonic 账号 ----

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

// ---- 艺术家 ----

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
	resp, err := http.Get(d.Pic)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	io.Copy(w, resp.Body)
}

// ---- 任务 ----

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
	// 存下最近的扫描路径
	h.Settings.Set("last_scan_path", body.Path)

	taskID, err := h.DB.CreateScanTask(body.Path, body.Mode, body.Source)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	go h.Scan.Run(taskID, body.Path, body.Source)
	writeJSON(w, 200, map[string]any{"message": "扫描任务已创建", "task_id": taskID})
}

// ---- 设置 ----

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	all := h.Settings.GetAll()

	// 脱敏 secret
	for _, k := range []string{"alist_password", "web_pass", "subsonic_pass"} {
		if v, ok := all[k]; ok && v != "" {
			all["has_"+k] = "true"
			all[k] = ""
		}
	}

	// 补充 storage_providers 脱敏
	if raw, ok := all["storage_providers"]; ok && raw != "" {
		var list []map[string]any
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, p := range list {
				if cfg, ok := p["config"].(map[string]any); ok {
					if pw, ok := cfg["refresh_password"].(string); ok && pw != "" {
						cfg["has_refresh_password"] = true
						cfg["refresh_password"] = ""
					}
					if t, ok := cfg["token"].(string); ok && t != "" {
						cfg["has_token"] = true
						cfg["token"] = ""
					}
				}
			}
			if b, err := json.Marshal(list); err == nil {
				all["storage_providers"] = string(b)
			}
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
			// 空值跳过（是脱敏字段没改）
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

	// 如果改了 storage_providers 且里面有 AList 密码，清空 token 强制重新登录
	if _, ok := body["storage_providers"]; ok {
		// 但空密码要保留原值
		raw := h.Settings.Get("storage_providers")
		var list []map[string]any
		if json.Unmarshal([]byte(raw), &list) == nil {
			// 与 DB 里之前的值合并——简化起见，先不做复杂合并
		}
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

// ---- 统计 ----

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"total_songs": h.DB.CountSongs()})
}

func (h *Handler) recentPlays(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.DB.RecentPlays(10)
	writeJSON(w, 200, map[string]any{"plays": rows})
}

// ---- 工具 ----

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
