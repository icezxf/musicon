package subsonic

import (
"encoding/json"
"encoding/xml"
"fmt"
"io"
"log"
"net/http"
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

switch p {
case "ping":
h.writeOK(w, r, map[string]any{})
case "getArtists":
h.getArtists(w, r)
case "getAlbumList2":
h.getAlbumList2(w, r)
case "getSong":
h.getSong(w, r)
case "stream":
h.stream(w, r)
case "getCoverArt":
h.getCoverArt(w, r)
case "getPlaylists":
h.getPlaylists(w, r)
case "getPlaylist":
h.getPlaylist(w, r)
case "updatePlaylist":
h.updatePlaylist(w, r)
case "getLyricsBySongId":
h.getLyricsBySongId(w, r)
case "getArtistInfo2", "getArtistInfo":
h.getArtistInfo(w, r, p)
case "getArtistImage":
h.getArtistImage(w, r)
case "scrobble":
h.scrobble(w, r)
default:
h.writeErr(w, r, 70, "Unknown method: "+p)
}
}

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
"status":        status,
"version":       "1.16.1",
"type":          "musicon-go",
"serverVersion": "0.1.0",
"openSubsonic":  true,
}
if code != 0 {
resp["error"] = map[string]any{"code": code, "message": msg}
}
for k, v := range body {
resp[k] = v
}
root := map[string]any{"subsonic-response": resp}
w.Header().Set("Access-Control-Allow-Origin", "*")
if h.wantsJSON(r) {
w.Header().Set("Content-Type", "application/json; charset=utf-8")
json.NewEncoder(w).Encode(root)
return
}
w.Header().Set("Content-Type", "application/xml; charset=utf-8")
w.Write([]byte(xml.Header))
w.Write([]byte(toXML("subsonic-response", resp)))
}

func toXML(name string, v any) string {
var b strings.Builder
writeXML(&b, name, v)
return b.String()
}

func writeXML(b *strings.Builder, name string, v any) {
switch x := v.(type) {
case map[string]any:
b.WriteString("<" + name + ">")
for k, val := range x {
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
case int:
b.WriteString("<" + name + ">" + strconv.Itoa(x) + "</" + name + ">")
case int64:
b.WriteString("<" + name + ">" + strconv.FormatInt(x, 10) + "</" + name + ">")
case float64:
b.WriteString("<" + name + ">" + strconv.FormatFloat(x, 'f', -1, 64) + "</" + name + ">")
default:
b.WriteString("<" + name + "></" + name + ">")
}
}

func escapeXML(s string) string {
r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
return r.Replace(s)
}

func (h *Handler) getArtists(w http.ResponseWriter, r *http.Request) {
songs, _ := h.DB.ListSongs()
seen := map[string]bool{}
var artists []map[string]any
for _, s := range songs {
if s.Artist == "" || seen[s.Artist] {
continue
}
seen[s.Artist] = true
artists = append(artists, map[string]any{"id": s.Artist, "name": s.Artist})
}
h.writeOK(w, r, map[string]any{
"artists": map[string]any{
"ignoredArticles": "",
"index": []map[string]any{{"name": "#", "artist": artists}},
},
})
}

func (h *Handler) getAlbumList2(w http.ResponseWriter, r *http.Request) {
songs, _ := h.DB.ListSongs()
seen := map[string]bool{}
var albums []map[string]any
for _, s := range songs {
key := s.Album + "|" + s.Artist
if s.Album == "" || seen[key] {
continue
}
seen[key] = true
albums = append(albums, map[string]any{"id": s.Album, "name": s.Album, "artist": s.Artist})
}
h.writeOK(w, r, map[string]any{"albumList2": map[string]any{"album": albums}})
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
log.Printf("[stream] 302 -> %s", clip)
http.Redirect(w, r, url, http.StatusFound)
}

func (h *Handler) getCoverArt(w http.ResponseWriter, r *http.Request) {
id := r.URL.Query().Get("id")
typ := r.URL.Query().Get("type")

if typ == "artist" {
name := r.URL.Query().Get("artist_name")
if name == "" {
name = id
}
if d, err := h.LX.GetSingerDetail(name); err == nil && d.Pic != "" {
proxyImage(w, d.Pic)
return
}
h.writeErr(w, r, 70, "Artist avatar not found")
return
}

s, err := h.DB.GetSong(id)
if err != nil {
h.writeErr(w, r, 70, "Song not found")
return
}
if s.CoverPath != "" {
http.ServeFile(w, r, s.CoverPath)
return
}
h.writeErr(w, r, 70, "Cover not found")
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

func (h *Handler) getPlaylists(w http.ResponseWriter, r *http.Request) {
pls, _ := h.DB.ListPlaylists()
var out []map[string]any
for _, p := range pls {
out = append(out, map[string]any{
"id": p.ID, "name": p.Name, "owner": p.Owner,
"public": p.Public, "songCount": p.Count,
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
for _, s := range songs {
entries = append(entries, songToMap(&s))
}
h.writeOK(w, r, map[string]any{
"playlist": map[string]any{
"id": p.ID, "name": p.Name, "owner": p.Owner,
"public": p.Public, "songCount": p.Count, "entry": entries,
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
m := map[string]any{"value": ln.Value}
if ln.Start > 0 {
m["start"] = ln.Start
synced = true
}
jsonLines = append(jsonLines, m)
}
h.writeOK(w, r, map[string]any{
"lyricsList": map[string]any{
"structuredLyrics": []map[string]any{{
"displayArtist": s.Artist, "displayTitle": s.Title,
"lang": "zho", "synced": synced, "line": jsonLines,
}},
},
})
}

func (h *Handler) getArtistInfo(w http.ResponseWriter, r *http.Request, key string) {
id := r.URL.Query().Get("id")
host := r.Host
scheme := "http"
if r.TLS != nil {
scheme = "https"
}
bio := ""
hasPic := false
if d, err := h.LX.GetSingerDetail(id); err == nil {
bio = d.Bio
hasPic = d.Pic != ""
}
base := scheme + "://" + host
small := ""
if hasPic {
small = base + "/rest/getArtistImage?id=" + id
}
h.writeOK(w, r, map[string]any{
key: map[string]any{
"biography": bio, "smallImageUrl": small,
"mediumImageUrl": small, "largeImageUrl": small,
"similarArtist": []any{},
},
})
}

func (h *Handler) getArtistImage(w http.ResponseWriter, r *http.Request) {
id := r.URL.Query().Get("id")
if d, err := h.LX.GetSingerDetail(id); err == nil && d.Pic != "" {
proxyImage(w, d.Pic)
return
}
http.Error(w, "not found", 404)
}

func (h *Handler) scrobble(w http.ResponseWriter, r *http.Request) {
id := r.URL.Query().Get("id")
sub := r.URL.Query().Get("submission") == "true"
if sub && id != "" {
h.DB.RecordPlay(id, "subsonic", r.URL.Query().Get("c"))
}
h.writeOK(w, r, map[string]any{})
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
	albumID := slugify(s.Album)
	artistID := slugify(s.Artist)
	return map[string]any{
		"id":          s.ID,
		"parent":      albumID,
		"isDir":       false,
		"title":       s.Title,
		"album":       s.Album,
		"artist":      s.Artist,
		"track":       0,
		"year":        0,
		"genre":       s.Genre,
		"coverArt":    s.ID,
		"size":        0,
		"contentType": ct,
		"suffix":      strings.ToLower(s.Fmt),
		"duration":    s.Dur,
		"bitRate":     0,
		"path":        s.Path,
		"albumId":     albumID,
		"artistId":    artistID,
		"type":        "music",
		"created":     "2024-01-01T00:00:00.000Z",
		"isVideo":     false,
	}
}

func slugify(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if r == ' ' {
			b.WriteByte('-')
		} else if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
