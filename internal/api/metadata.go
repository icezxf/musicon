package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// GET /api/music/metadata-search?keyword=xxx
// 转发到 LX 搜索
func (h *Handler) metadataSearch(w http.ResponseWriter, r *http.Request) {
	kw := r.URL.Query().Get("keyword")
	if kw == "" {
		writeJSON(w, 400, map[string]any{"detail": "keyword required"})
		return
	}
	lxURL := h.Settings.GetLXURL()
	u := strings.TrimRight(lxURL, "/") + "/api/music/search?source=tx&name=" + url.QueryEscape(kw) + "&type=song"
	resp, err := http.Get(u)
	if err != nil {
		writeJSON(w, 502, map[string]any{"detail": err.Error()})
		return
	}
	defer resp.Body.Close()
	var data any
	json.NewDecoder(resp.Body).Decode(&data)
	writeJSON(w, 200, map[string]any{"results": data})
}

// GET /api/music/metadata-lyric?source=tx&songId=xxx
func (h *Handler) metadataLyric(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	songID := r.URL.Query().Get("songId")
	if source == "" || songID == "" {
		writeJSON(w, 400, map[string]any{"detail": "source and songId required"})
		return
	}
	lxURL := h.Settings.GetLXURL()
	u := strings.TrimRight(lxURL, "/") + "/api/music/lyric?source=" + url.QueryEscape(source) + "&songId=" + url.QueryEscape(songID)
	resp, err := http.Get(u)
	if err != nil {
		writeJSON(w, 502, map[string]any{"detail": err.Error()})
		return
	}
	defer resp.Body.Close()
	var data any
	json.NewDecoder(resp.Body).Decode(&data)
	writeJSON(w, 200, data)
}