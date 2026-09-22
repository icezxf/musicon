package subsonic

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/auth"
	"github.com/icezxf/musicon-go/internal/config"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/lx"
	"github.com/icezxf/musicon-go/internal/meta"
)

type Handler struct {
	DB    *db.Holder
	AList *alist.Client
	LX    *lx.Client
	Cfg   *config.Config
}

func New(database *db.Holder, a *alist.Client, l *lx.Client, c *config.Config) *Handler {
	return &Handler{DB: database, AList: a, LX: l, Cfg: c}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/rest/", h.route)
}

func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/rest/")
	p = strings.TrimSuffix(p, ".view")
	q := r.URL.Query()

	u, pw, t, s := q.Get("u"), q.Get("p"), q.Get("t"), q.Get("s")
	if p != "ping" {
		if !auth.VerifySubsonic(h.DB.DB, u, pw, t, s) {
			h.writeErr(w, r, 40, "Wrong username or password")
			return
		}
	}

	log.Printf("[subsonic] %s id=%s type=%s", p, q.Get("id"), q.Get("type"))

	switch p {
	case "ping":
		h.writeOK(w, r, map[string]any{})
	case "getLicense":
		h.writeOK(w, r, map[string]any{"license": map[string]any{"@valid": true}})
	case "getOpenSubsonicExtensions":
		h.writeOK(w, r, map[string]any{"openSubsonicExtensions": []any{}})
	case "getMusicFolders":
		h.getMusicFolders(w, r)
	case "getIndexes":
		h.getIndexes(w, r)
	case "getArtists":
		h.getArtists(w, r)
	case "getArtist":
		h.getArtist(w, r)
	case "getAlbumList":
		h.getAlbumList(w, r)
	case "getAlbumList2":
		h.getAlbumList2(w, r)
	case "getAlbum":
		h.getAlbum(w, r)
	case "getSong":
		h.getSong(w, r)
	case "getRandomSongs":
		h.getRandomSongs(w, r)
	case "getGenres":
		h.getGenres(w, r)
	case "getSongsByGenre", "getTopSongs":
		h.getRandomSongs(w, r)
	case "getSimilarSongs", "getSimilarSongs2":
		h.getSimilarSongs(w, r, p)
	case "stream", "download":
		h.stream(w, r)
	case "getCoverArt":
		h.getCoverArt(w, r)
	case "getPlaylists":
		h.getPlaylists(w, r)
	case "getPlaylist":
		h.getPlaylist(w, r)
	case "updatePlaylist":
		h.updatePlaylist(w, r)
	case "getLyrics":
		h.getLyricsLegacy(w, r)
	case "getLyricsBySongId":
		h.getLyricsBySongId(w, r)
	case "getArtistInfo":
		h.getArtistInfo(w, r, "artistInfo")
	case "getArtistInfo2":
		h.getArtistInfo(w, r, "artistInfo2")
	case "getArtistImage":
		h.getArtistImage(w, r)
	case "scrobble":
		h.scrobble(w, r)
	case "search2":
		h.search(w, r, "searchResult2")
	case "search3":
		h.search(w, r, "searchResult3")
	case "getStarred", "getStarred2":
		h.writeOK(w, r, map[string]any{p: map[string]any{
			"artist": []any{}, "album": []any{}, "song": []any{},
		}})
	case "getUser":
		h.writeOK(w, r, map[string]any{"user": map[string]any{
			"@username": u, "@adminRole": true, "@scrobblingEnabled": true,
		}})
	case "getScanStatus":
		h.writeOK(w, r, map[string]any{"scanStatus": map[string]any{"@scanning": false, "@count": 0}})
	default:
		log.Printf("[subsonic] unimplemented: %s", p)
		h.writeErr(w, r, 70, "not implemented: "+p)
	}
}

// ==================== 响应 ====================

func (h *Handler) wantsJSON(r *http.Request) bool {
	return strings.ToLower(r.URL.Query().Get("f")) == "json"
}

func (h *Handler) writeOK(w http.ResponseWriter, r *http.Request, body map[string]any) {
	h.writeResp(w, r, "ok", 0, "", body)
}

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, code int, msg string) {
	h.writeResp(w, r, "failed", code, msg, nil)
}

func (h *Handler) writeResp(w http.ResponseWriter, r *http.Request, status string, code int, msg string, body map[string]any) {
	resp := map[string]any{
		"@xmlns":         "http://subsonic.org/restapi",
		"@status":        status,
		"@version":       "1.16.1",
		"@type":          "musicon-go",
		"@serverVersion": "0.1.0",
		"@openSubsonic":  true,
	}
	if code != 0 {
		resp["error"] = map[string]any{"@code": code, "@message": msg}
	}
	for k, v := range body {
		resp[k] = v
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")

	if h.wantsJSON(r) {
		root := map[string]any{"subsonic-response": resp}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(stripAt(root))
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	w.Write([]byte(toXML("subsonic-response", resp)))
}

func stripAt(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[strings.TrimPrefix(k, "@")] = stripAt(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = stripAt(item)
		}
		return out
	case []map[string]any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = stripAt(item)
		}
		return out
	}
	return v
}

// ==================== XML ====================

func toXML(name string, v any) string {
	var b strings.Builder
	writeXML(&b, name, v)
	return b.String()
}

func writeXML(b *strings.Builder, name string, v any) {
	switch x := v.(type) {
	case map[string]any:
		b.WriteString("<" + name)
		for k, val := range x {
			if strings.HasPrefix(k, "@") && isPrimitive(val) {
				attr := strings.TrimPrefix(k, "@")
				b.WriteString(" " + attr + "=\"" + escapeXML(fmt.Sprintf("%v", val)) + "\"")
			}
		}
		hasChild := false
		for k := range x {
			if !strings.HasPrefix(k, "@") {
				hasChild = true
				break
			}
		}
		if !hasChild {
			b.WriteString("/>")
			return
		}
		b.WriteString(">")
		for k, val := range x {
			if strings.HasPrefix(k, "@") {
				continue
			}
			writeXML(b, k, val)
		}
		b.WriteString("</" + name + ">")
	case []any:
		for _, item := range x {
			writeXML(b, name, item)
		}
	case []map[string]any:
		for _, item := range x {
			writeXML(b, name, item)
		}
	case []string:
		for _, item := range x {
			writeXML(b, name, item)
		}
	case string:
		b.WriteString("<" + name + ">" + escapeXML(x) + "</" + name + ">")
	case bool:
		b.WriteString("<" + name + ">" + fmt.Sprintf("%v", x) + "</" + name + ">")
	case int, int64, float64:
		b.WriteString("<" + name + ">" + fmt.Sprintf("%v", x) + "</" + name + ">")
	case nil:
		b.WriteString("<" + name + "/>")
	default:
		b.WriteString("<" + name + ">" + escapeXML(fmt.Sprintf("%v", x)) + "</" + name + ">")
	}
}

func isPrimitive(v any) bool {
	switch v.(type) {
	case string, int, int64, float64, bool:
		return true
	}
	return false
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// ==================== 音乐库 ====================

func (h *Handler) getMusicFolders(w http.ResponseWriter, r *http.Request) {
	h.writeOK(w, r, map[string]any{
		"musicFolders": map[string]any{
			"musicFolder": []map[string]any{
				{"@id": "1", "@name": "Music"},
			},
		},
	})
}

func (h *Handler) buildArtistGroups() []map[string]any {
	songs, _ := h.DB.ListSongs()

	artistAlbums := map[string]map[string]bool{}
	for _, s := range songs {
		if s.Artist == "" {
			continue
		}
		if _, ok := artistAlbums[s.Artist]; !ok {
			artistAlbums[s.Artist] = map[string]bool{}
		}
		if s.Album != "" {
			artistAlbums[s.Artist][s.Album] = true
		}
	}

	groups := map[string][]map[string]any{}
	for artist, albums := range artistAlbums {
		runes := []rune(artist)
		first := "#"
		if len(runes) > 0 {
			c := strings.ToUpper(string(runes[0]))
			if c >= "A" && c <= "Z" {
				first = c
			}
		}
		groups[first] = append(groups[first], map[string]any{
			"@id":         artistID(artist),
			"@name":       artist,
			"@albumCount": len(albums),
			"@coverArt":   artistID(artist),
		})
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var indexes []map[string]any
	for _, k := range keys {
		sort.Slice(groups[k], func(i, j int) bool {
			return groups[k][i]["@name"].(string) < groups[k][j]["@name"].(string)
		})
		indexes = append(indexes, map[string]any{
			"@name":  k,
			"artist": groups[k],
		})
	}
	return indexes
}

func (h *Handler) getArtists(w http.ResponseWriter, r *http.Request) {
	indexes := h.buildArtistGroups()
	h.writeOK(w, r, map[string]any{
		"artists": map[string]any{
			"@ignoredArticles": "The El La Los Las Le Les",
			"index":            indexes,
		},
	})
}

func (h *Handler) getIndexes(w http.ResponseWriter, r *http.Request) {
	indexes := h.buildArtistGroups()
	h.writeOK(w, r, map[string]any{
		"indexes": map[string]any{
			"@ignoredArticles": "The El La Los Las Le Les",
			"@lastModified":    "0",
			"index":            indexes,
		},
	})
}

func (h *Handler) getArtist(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	songs, _ := h.DB.ListSongs()

	type albumAgg struct {
		name   string
		artist string
		count  int
		dur    int
	}
	albums := map[string]*albumAgg{}
	artistName := ""
	for _, s := range songs {
		if artistID(s.Artist) != id {
			continue
		}
		artistName = s.Artist
		k := s.Album + "|" + s.Artist
		if _, ok := albums[k]; !ok {
			albums[k] = &albumAgg{name: s.Album, artist: s.Artist}
		}
		albums[k].count++
		albums[k].dur += s.Dur
	}
	if artistName == "" {
		h.writeErr(w, r, 70, "Artist not found")
		return
	}
	var albumList []map[string]any
	for _, a := range albums {
		albumList = append(albumList, albumToMap(a.name, a.artist, a.count, a.dur))
	}
	h.writeOK(w, r, map[string]any{
		"artist": map[string]any{
			"@id":         id,
			"@name":       artistName,
			"@albumCount": len(albumList),
			"@coverArt":   id,
			"album":       albumList,
		},
	})
}

// ==================== 专辑列表 ====================

// 老版 getAlbumList
// 响应 key: albumList
// album 字段: title（不是 name）
func (h *Handler) getAlbumList(w http.ResponseWriter, r *http.Request) {
	songs, _ := h.DB.ListSongs()

	type albumAgg struct {
		name   string
		artist string
		count  int
		dur    int
	}
	albums := map[string]*albumAgg{}
	for _, s := range songs {
		if s.Album == "" {
			continue
		}
		k := s.Album + "|" + s.Artist
		if _, ok := albums[k]; !ok {
			albums[k] = &albumAgg{name: s.Album, artist: s.Artist}
		}
		albums[k].count++
		albums[k].dur += s.Dur
	}

	var out []map[string]any
	for _, a := range albums {
		id := albumID(a.name, a.artist)
		out = append(out, map[string]any{
			"@id":       id,
			"@parent":   "1",
			"@isDir":    true,
			"@title":    a.name,
			"@album":    a.name,
			"@artist":   a.artist,
			"@coverArt": id,
			"@created":  "2024-01-01T00:00:00.000Z",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["@title"].(string) < out[j]["@title"].(string)
	})

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 500
	}
	if offset > len(out) {
		offset = len(out)
	}
	end := offset + size
	if end > len(out) {
		end = len(out)
	}
	out = out[offset:end]

	log.Printf("[subsonic] getAlbumList returned %d", len(out))
	h.writeOK(w, r, map[string]any{
		"albumList": map[string]any{"album": out},
	})
}

// 新版 getAlbumList2
// 响应 key: albumList2  ← 关键修复
// album 字段: name（不是 title）
func (h *Handler) getAlbumList2(w http.ResponseWriter, r *http.Request) {
	songs, _ := h.DB.ListSongs()

	type albumAgg struct {
		name   string
		artist string
		count  int
		dur    int
	}
	albums := map[string]*albumAgg{}
	for _, s := range songs {
		if s.Album == "" {
			continue
		}
		k := s.Album + "|" + s.Artist
		if _, ok := albums[k]; !ok {
			albums[k] = &albumAgg{name: s.Album, artist: s.Artist}
		}
		albums[k].count++
		albums[k].dur += s.Dur
	}

	var out []map[string]any
	for _, a := range albums {
		out = append(out, albumToMap(a.name, a.artist, a.count, a.dur))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["@name"].(string) < out[j]["@name"].(string)
	})

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 500
	}
	if offset > len(out) {
		offset = len(out)
	}
	end := offset + size
	if end > len(out) {
		end = len(out)
	}
	out = out[offset:end]

	log.Printf("[subsonic] getAlbumList2 returned %d", len(out))
	h.writeOK(w, r, map[string]any{
		"albumList2": map[string]any{"album": out},
	})
}

func (h *Handler) getAlbum(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	songs, _ := h.DB.ListSongs()

	var albumSongs []db.Song
	albumName := ""
	artistName := ""
	totalDur := 0
	for _, s := range songs {
		if albumID(s.Album, s.Artist) == id {
			albumSongs = append(albumSongs, s)
			albumName = s.Album
			artistName = s.Artist
			totalDur += s.Dur
		}
	}
	if albumName == "" {
		h.writeErr(w, r, 70, "Album not found")
		return
	}
	var out []map[string]any
	for i := range albumSongs {
		out = append(out, songToMap(&albumSongs[i]))
	}
	h.writeOK(w, r, map[string]any{
		"album": map[string]any{
			"@id":        id,
			"@name":      albumName,
			"@artist":    artistName,
			"@artistId":  artistID(artistName),
			"@coverArt":  id,
			"@songCount": len(out),
			"@duration":  totalDur,
			"@created":   "2024-01-01T00:00:00.000Z",
			"song":       out,
		},
	})
}

func (h *Handler) getSong(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s, err := h.DB.GetSong(id)
	if err != nil {
		h.writeErr(w, r, 70, "Song not found")
		return
	}
	h.writeOK(w, r, map[string]any{"song": songToMap(s)})
}

func (h *Handler) getRandomSongs(w http.ResponseWriter, r *http.Request) {
	songs, _ := h.DB.ListSongs()
	rand.Shuffle(len(songs), func(i, j int) { songs[i], songs[j] = songs[j], songs[i] })
	size := 50
	if v := r.URL.Query().Get("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}
	if len(songs) > size {
		songs = songs[:size]
	}
	var out []map[string]any
	for i := range songs {
		out = append(out, songToMap(&songs[i]))
	}
	h.writeOK(w, r, map[string]any{
		"randomSongs": map[string]any{"song": out},
	})
}

func (h *Handler) getSimilarSongs(w http.ResponseWriter, r *http.Request, key string) {
	songs, _ := h.DB.ListSongs()
	if len(songs) > 20 {
		songs = songs[:20]
	}
	var out []map[string]any
	for i := range songs {
		out = append(out, songToMap(&songs[i]))
	}
	respKey := "similarSongs"
	if key == "getSimilarSongs2" {
		respKey = "similarSongs2"
	}
	h.writeOK(w, r, map[string]any{
		respKey: map[string]any{"song": out},
	})
}

func (h *Handler) getGenres(w http.ResponseWriter, r *http.Request) {
	songs, _ := h.DB.ListSongs()
	gm := map[string]int{}
	for _, s := range songs {
		if s.Genre != "" {
			gm[s.Genre]++
		}
	}
	var out []map[string]any
	for g, n := range gm {
		out = append(out, map[string]any{
			"@value": g, "@songCount": n, "@albumCount": 0,
		})
	}
	h.writeOK(w, r, map[string]any{
		"genres": map[string]any{"genre": out},
	})
}

// ==================== 播放 ====================

func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s, err := h.DB.GetSong(id)
	if err != nil {
		h.writeErr(w, r, 70, "Song not found")
		return
	}
	url, err := h.AList.GetRawURL(s.Path)
	if err != nil {
		h.writeErr(w, r, 0, err.Error())
		return
	}
	clip := url
	if len(clip) > 120 {
		clip = clip[:120]
	}
	log.Printf("[stream] %s %s -> 302 %s", r.Method, id, clip)
	http.Redirect(w, r, url, http.StatusFound)
}

// ==================== 封面 ====================

func (h *Handler) getCoverArt(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	typ := r.URL.Query().Get("type")
	log.Printf("[getCoverArt] id=%s type=%s", id, typ)

	// 艺术家头像
	if typ == "artist" {
		name := r.URL.Query().Get("artist_name")
		if name == "" {
			name = h.resolveArtistName(id)
		}
		if d, err := h.LX.GetSingerDetail(name); err == nil && d.Pic != "" {
			proxyImage(w, d.Pic)
			return
		}
		http.Error(w, "not found", 404)
		return
	}

	// 专辑封面
	if typ == "album" || strings.HasPrefix(id, "al-") {
		songs, _ := h.DB.ListSongs()
		for _, s := range songs {
			if albumID(s.Album, s.Artist) == id && s.CoverPath != "" {
				serveLocalCover(w, r, s.CoverPath)
				return
			}
		}
		log.Printf("[getCoverArt] album %s 无封面", id)
		http.Error(w, "not found", 404)
		return
	}

	// 歌单封面
	if typ == "playlist" {
		songs, _ := h.DB.GetPlaylistSongs(id)
		for _, s := range songs {
			if s.CoverPath != "" {
				serveLocalCover(w, r, s.CoverPath)
				return
			}
		}
		http.Error(w, "not found", 404)
		return
	}

	// 默认：歌曲封面
	s, err := h.DB.GetSong(id)
	if err != nil {
		log.Printf("[getCoverArt] song %s 不存在", id)
		http.Error(w, "not found", 404)
		return
	}
	log.Printf("[getCoverArt] song=%q cover_path=%q", s.Title, s.CoverPath)
	if s.CoverPath == "" {
		http.Error(w, "not found", 404)
		return
	}
	serveLocalCover(w, r, s.CoverPath)
}

func serveLocalCover(w http.ResponseWriter, r *http.Request, path string) {
	if _, err := os.Stat(path); err != nil {
		base := path
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		alt := "/app/data/covers/" + base
		if _, err := os.Stat(alt); err == nil {
			path = alt
		}
	}

	f, err := os.Open(path)
	if err != nil {
		log.Printf("[getCoverArt] 打开封面失败: %s: %v", path, err)
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "stat failed", 500)
		return
	}
	log.Printf("[getCoverArt] ✓ 返回 %s (%d bytes)", path, fi.Size())
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func proxyImage(w http.ResponseWriter, url string) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
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
	io.Copy(w, resp.Body)
}

// ==================== 歌单 ====================

func (h *Handler) getPlaylists(w http.ResponseWriter, r *http.Request) {
	pls, _ := h.DB.ListPlaylists()
	var out []map[string]any
	for _, p := range pls {
		out = append(out, map[string]any{
			"@id":        p.ID,
			"@name":      p.Name,
			"@owner":     p.Owner,
			"@public":    p.Public,
			"@songCount": p.Count,
			"@duration":  p.Duration,
			"@created":   "2024-01-01T00:00:00.000Z",
			"@changed":   "2024-01-01T00:00:00.000Z",
		})
	}
	h.writeOK(w, r, map[string]any{"playlists": map[string]any{"playlist": out}})
}

func (h *Handler) getPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	p, err := h.DB.GetPlaylist(id)
	if err != nil {
		h.writeErr(w, r, 70, "Playlist not found")
		return
	}
	songs, _ := h.DB.GetPlaylistSongs(id)
	var entries []map[string]any
	totalDur := 0
	for i := range songs {
		entries = append(entries, songToMap(&songs[i]))
		totalDur += songs[i].Dur
	}
	h.writeOK(w, r, map[string]any{
		"playlist": map[string]any{
			"@id":        p.ID,
			"@name":      p.Name,
			"@owner":     p.Owner,
			"@public":    p.Public,
			"@songCount": len(entries),
			"@duration":  totalDur,
			"@created":   "2024-01-01T00:00:00.000Z",
			"@changed":   "2024-01-01T00:00:00.000Z",
			"entry":      entries,
		},
	})
}

func (h *Handler) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.FormValue("playlistId")
	if id == "" {
		id = r.URL.Query().Get("playlistId")
	}
	if r.Form != nil {
		if add := r.Form["songIdToAdd"]; len(add) > 0 {
			h.DB.AddSongsToPlaylist(id, add)
		}
		if rem := r.Form["songIndexToRemove"]; len(rem) > 0 {
			h.DB.RemoveSongsFromPlaylist(id, rem)
		}
	}
	h.writeOK(w, r, map[string]any{})
}

// ==================== 歌词 ====================

func (h *Handler) getLyricsLegacy(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s, err := h.DB.GetSong(id)
	if err != nil || s.Lyrics == "" {
		h.writeOK(w, r, map[string]any{"lyrics": map[string]any{
			"@artist": r.URL.Query().Get("artist"),
			"@title":  r.URL.Query().Get("title"),
		}})
		return
	}
	h.writeOK(w, r, map[string]any{"lyrics": map[string]any{
		"@artist": s.Artist,
		"@title":  s.Title,
		"#text":   s.Lyrics,
	}})
}

func (h *Handler) getLyricsBySongId(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s, err := h.DB.GetSong(id)
	if err != nil || s.Lyrics == "" {
		h.writeOK(w, r, map[string]any{"lyricsList": map[string]any{"structuredLyrics": []any{}}})
		return
	}
	lines := meta.ParseLRC(s.Lyrics)
	var jsonLines []map[string]any
	synced := false
	for _, ln := range lines {
		m := map[string]any{"@value": ln.Value}
		if ln.Start > 0 {
			m["@start"] = ln.Start
			synced = true
		}
		jsonLines = append(jsonLines, m)
	}
	h.writeOK(w, r, map[string]any{
		"lyricsList": map[string]any{
			"structuredLyrics": []map[string]any{{
				"@displayArtist": s.Artist,
				"@displayTitle":  s.Title,
				"@lang":          "zho",
				"@synced":        synced,
				"line":           jsonLines,
			}},
		},
	})
}

// ==================== 艺术家信息 ====================

func (h *Handler) getArtistInfo(w http.ResponseWriter, r *http.Request, key string) {
	id := r.URL.Query().Get("id")
	host := r.Host
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	name := h.resolveArtistName(id)

	bio := ""
	hasPic := false
	if name != "" {
		if d, err := h.LX.GetSingerDetail(name); err == nil {
			bio = d.Bio
			hasPic = d.Pic != ""
		}
	}
	base := scheme + "://" + host
	small := ""
	if hasPic {
		small = base + "/rest/getArtistImage?id=" + id
	}
	h.writeOK(w, r, map[string]any{
		key: map[string]any{
			"biography":      bio,
			"smallImageUrl":  small,
			"mediumImageUrl": small,
			"largeImageUrl":  small,
			"similarArtist":  []any{},
		},
	})
}

func (h *Handler) resolveArtistName(id string) string {
	if id == "" {
		return ""
	}
	songs, _ := h.DB.ListSongs()
	for _, s := range songs {
		if artistID(s.Artist) == id || s.Artist == id {
			return s.Artist
		}
	}
	return id
}

func (h *Handler) getArtistImage(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	name := h.resolveArtistName(id)
	if d, err := h.LX.GetSingerDetail(name); err == nil && d.Pic != "" {
		proxyImage(w, d.Pic)
		return
	}
	http.Error(w, "not found", 404)
}

// ==================== 上报 ====================

func (h *Handler) scrobble(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	sub := r.URL.Query().Get("submission") == "true"
	if sub && id != "" {
		h.DB.RecordPlay(id, "subsonic", r.URL.Query().Get("c"))
	}
	h.writeOK(w, r, map[string]any{})
}

// ==================== 搜索 ====================

// key 传 "searchResult2" 或 "searchResult3"
// key 传 "searchResult2" 或 "searchResult3"
func (h *Handler) search(w http.ResponseWriter, r *http.Request, key string) {
	q := r.URL.Query().Get("query")
	if q == "" {
		q = r.URL.Query().Get("any")
	}
	ql := strings.ToLower(q)

	songCount, _ := strconv.Atoi(r.URL.Query().Get("songCount"))
	songOffset, _ := strconv.Atoi(r.URL.Query().Get("songOffset"))
	artistCount, _ := strconv.Atoi(r.URL.Query().Get("artistCount"))
	albumCount, _ := strconv.Atoi(r.URL.Query().Get("albumCount"))

	log.Printf("[search] key=%s query=%q songCount=%d songOffset=%d artistCount=%d albumCount=%d",
		key, q, songCount, songOffset, artistCount, albumCount)

	// 空查询 / 通配符 → 返回全部
	all := ql == "" || ql == "*"

	songs, _ := h.DB.ListSongs()

	var matchedSongs []map[string]any
	artistSet := map[string]bool{}
	albumSet := map[string]bool{}
	var matchedArtists []map[string]any
	var matchedAlbums []map[string]any

	for i := range songs {
		s := &songs[i]
		if !all {
			hay := strings.ToLower(s.Title + " " + s.Artist + " " + s.Album)
			if !strings.Contains(hay, ql) {
				continue
			}
		}
		matchedSongs = append(matchedSongs, songToMap(s))
		if !artistSet[s.Artist] && s.Artist != "" {
			artistSet[s.Artist] = true
			matchedArtists = append(matchedArtists, map[string]any{
				"@id": artistID(s.Artist), "@name": s.Artist, "@albumCount": 1,
			})
		}
		ak := s.Album + "|" + s.Artist
		if !albumSet[ak] && s.Album != "" {
			albumSet[ak] = true
			matchedAlbums = append(matchedAlbums, albumToMap(s.Album, s.Artist, 1, s.Dur))
		}
	}

	// 应用 songOffset / songCount 分页
	if songOffset > 0 && songOffset < len(matchedSongs) {
		matchedSongs = matchedSongs[songOffset:]
	} else if songOffset >= len(matchedSongs) {
		matchedSongs = nil
	}
	if songCount > 0 && len(matchedSongs) > songCount {
		matchedSongs = matchedSongs[:songCount]
	}
	if artistCount > 0 && len(matchedArtists) > artistCount {
		matchedArtists = matchedArtists[:artistCount]
	}
	if albumCount > 0 && len(matchedAlbums) > albumCount {
		matchedAlbums = matchedAlbums[:albumCount]
	}

	log.Printf("[search] 返回 %d song / %d artist / %d album",
		len(matchedSongs), len(matchedArtists), len(matchedAlbums))

	h.writeOK(w, r, map[string]any{
		key: map[string]any{
			"artist": matchedArtists,
			"album":  matchedAlbums,
			"song":   matchedSongs,
		},
	})
}

// ==================== 辅助 ====================

func albumToMap(name, artist string, songCount, duration int) map[string]any {
	id := albumID(name, artist)
	return map[string]any{
		"@id":        id,
		"@name":      name,
		"@artist":    artist,
		"@artistId":  artistID(artist),
		"@coverArt":  id,
		"@songCount": songCount,
		"@duration":  duration,
		"@created":   "2024-01-01T00:00:00.000Z",
	}
}

func songToMap(s *db.Song) map[string]any {
	ct := "audio/mpeg"
	switch strings.ToLower(s.Fmt) {
	case "flac":
		ct = "audio/flac"
	case "mp3":
		ct = "audio/mpeg"
	case "m4a", "aac":
		ct = "audio/mp4"
	case "ogg":
		ct = "audio/ogg"
	case "opus":
		ct = "audio/opus"
	case "wav":
		ct = "audio/wav"
	}
	aID := albumID(s.Album, s.Artist)
	arID := artistID(s.Artist)

	coverArt := s.ID
	if s.CoverPath == "" {
		coverArt = aID
	}

	return map[string]any{
		"@id":          s.ID,
		"@parent":      aID,
		"@isDir":       false,
		"@title":       s.Title,
		"@album":       s.Album,
		"@artist":      s.Artist,
		"@track":       0,
		"@year":        0,
		"@genre":       s.Genre,
		"@coverArt":    coverArt,
		"@size":        0,
		"@contentType": ct,
		"@suffix":      strings.ToLower(s.Fmt),
		"@duration":    s.Dur,
		"@bitRate":     0,
		"@path":        s.Path,
		"@albumId":     aID,
		"@artistId":    arID,
		"@type":        "music",
		"@created":     "2024-01-01T00:00:00.000Z",
		"@isVideo":     false,
	}
}

func artistID(name string) string {
	if name == "" {
		return ""
	}
	h := md5.Sum([]byte(name))
	return "ar-" + hex.EncodeToString(h[:8])
}

func albumID(name, artist string) string {
	if name == "" {
		return ""
	}
	h := md5.Sum([]byte(name + "|" + artist))
	return "al-" + hex.EncodeToString(h[:8])
}
