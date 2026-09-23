package lx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/icezxf/musicon-go/internal/settings"
)

// CacheStore 由 db.Holder 实现
type CacheStore interface {
	LoadArtist(name string) (pic, bio, source string, updatedAt time.Time, ok bool)
	SaveArtist(name, pic, bio, source string) error
}

type Client struct {
	Settings *settings.Manager
	cache    CacheStore
	http     *http.Client
}

func New(s *settings.Manager, c CacheStore) *Client {
	return &Client{
		Settings: s,
		cache:    c,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

const artistCacheTTL = 7 * 24 * time.Hour

func (c *Client) base() string {
	if v := c.Settings.GetLXURL(); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:9527"
}

// ============ 通用请求 ============

func (c *Client) get(path string, params url.Values) ([]byte, error) {
	u := c.base() + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "musicon-go/1.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lx: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lx http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func (c *Client) post(path string, payload any, headers map[string]string) ([]byte, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.base()+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "musicon-go/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lx: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lx http %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ============ 搜索 ============

type Song struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Singer   string         `json:"singer"`
	Album    string         `json:"album"`
	Source   string         `json:"source"`
	Duration int            `json:"duration"`
	Cover    string         `json:"cover"`
	Raw      map[string]any `json:"-"`
}

func (s *Song) Info() map[string]any {
	if s.Raw != nil {
		return s.Raw
	}
	return map[string]any{
		"id": s.ID, "name": s.Name, "singer": s.Singer,
		"album": s.Album, "source": s.Source,
	}
}

func (c *Client) Search(query, source string, page, limit int) ([]Song, error) {
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	if source == "" {
		source = "wy"
	}
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 30
	}
	params := url.Values{}
	params.Set("source", source)
	params.Set("name", query)
	params.Set("type", "song")
	params.Set("page", strconv.Itoa(page))
	params.Set("limit", strconv.Itoa(limit))

	body, err := c.get("/api/music/search", params)
	if err != nil {
		return nil, err
	}
	raws := extractList(body)
	out := make([]Song, 0, len(raws))
	for _, m := range raws {
		out = append(out, parseSong(m))
	}
	return out, nil
}

// extractList 兼容多种响应结构，提取对象数组
func extractList(body []byte) []map[string]any {
	var r1 struct {
		Data struct {
			List []map[string]any `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r1); err == nil && len(r1.Data.List) > 0 {
		return r1.Data.List
	}
	var r2 struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(body, &r2); err == nil && len(r2.List) > 0 {
		return r2.List
	}
	var r3 struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &r3); err == nil && len(r3.Data) > 0 {
		return r3.Data
	}
	var r4 []map[string]any
	if err := json.Unmarshal(body, &r4); err == nil {
		return r4
	}
	return nil
}

func parseSong(m map[string]any) Song {
	s := Song{Raw: m}
	s.ID = strField(m, "songmid", "id", "songId", "mid", "hash")
	s.Name = strField(m, "name", "title", "songName", "songname")
	s.Singer = strField(m, "singer", "artist", "singerName", "singername")
	s.Album = strField(m, "albumName", "album", "albumname")
	s.Source = strField(m, "source", "platform")
	s.Cover = strField(m, "img", "cover", "pic", "albumPic")
	if d := strField(m, "interval", "duration", "dt"); d != "" {
		s.Duration = parseDuration(d)
	}
	return s
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch x := v.(type) {
			case string:
				if x != "" {
					return x
				}
			case float64:
				return strconv.FormatInt(int64(x), 10)
			case int:
				return strconv.Itoa(x)
			case int64:
				return strconv.FormatInt(x, 10)
			}
		}
	}
	return ""
}

func parseDuration(s string) int {
	if s == "" {
		return 0
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) == 2 {
			m, _ := strconv.Atoi(parts[0])
			sec, _ := strconv.Atoi(parts[1])
			return m*60 + sec
		}
		if len(parts) == 3 {
			h, _ := strconv.Atoi(parts[0])
			m, _ := strconv.Atoi(parts[1])
			sec, _ := strconv.Atoi(parts[2])
			return h*3600 + m*60 + sec
		}
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n > 10000 {
			return n / 1000
		}
		return n
	}
	return 0
}

// ============ 播放 URL ============

func (c *Client) GetSongURL(songInfo map[string]any, quality string) (string, error) {
	if songInfo == nil {
		return "", fmt.Errorf("nil songInfo")
	}
	src, _ := songInfo["source"].(string)
	if src == "" {
		return "", fmt.Errorf("songInfo.source is empty")
	}
	if quality == "" {
		quality = "320k"
	}
	payload := map[string]any{"songInfo": songInfo, "quality": quality}
	headers := map[string]string{"x-user-name": "open"}
	body, err := c.post("/api/music/url", payload, headers)
	if err != nil {
		return "", err
	}
	if u := extractURL(body); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("cannot parse url from lx response: %s", truncate(string(body), 300))
}

func extractURL(body []byte) string {
	var r1 struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &r1); err == nil && strings.HasPrefix(r1.Data, "http") {
		return r1.Data
	}
	var r2 struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r2); err == nil && r2.Data.URL != "" {
		return r2.Data.URL
	}
	var r3 struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &r3); err == nil && r3.URL != "" {
		return r3.URL
	}
	return ""
}

// ============ 歌词 ============

func (c *Client) GetLyric(songID, source string) (string, error) {
	if songID == "" {
		return "", fmt.Errorf("empty songID")
	}
	if source == "" {
		source = "wy"
	}
	params := url.Values{}
	params.Set("source", source)
	params.Set("songId", songID)
	body, err := c.get("/api/music/lyric", params)
	if err != nil {
		return "", err
	}
	return extractLyric(body), nil
}

func extractLyric(body []byte) string {
	var r1 struct {
		Data struct {
			Lyric  string `json:"lyric"`
			Tlyric string `json:"tlyric"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r1); err == nil && r1.Data.Lyric != "" {
		if r1.Data.Tlyric != "" {
			return r1.Data.Lyric + "\n" + r1.Data.Tlyric
		}
		return r1.Data.Lyric
	}
	var r2 struct {
		Lyric string `json:"lyric"`
	}
	if err := json.Unmarshal(body, &r2); err == nil && r2.Lyric != "" {
		return r2.Lyric
	}
	var r3 struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &r3); err == nil && r3.Data != "" {
		return r3.Data
	}
	return ""
}

// ============ 歌手（方案 B：search + artistDetail 两步） ============

type SingerDetail struct {
	Name   string `json:"name"`
	Pic    string `json:"pic"`
	Bio    string `json:"bio"`
	MID    string `json:"mid"`
	Source string `json:"source"`
}

// GetSingerDetail 查询歌手详情。命中缓存直接返回；未命中则打 lxserver。
func (c *Client) GetSingerDetail(name string) (*SingerDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}

	// 1. 查缓存
	if c.cache != nil {
		pic, bio, source, updatedAt, ok := c.cache.LoadArtist(name)
		if ok && pic != "" && time.Since(updatedAt) < artistCacheTTL {
			return &SingerDetail{Name: name, Pic: pic, Bio: bio, Source: source}, nil
		}
	}

	// 2. 依次尝试 tx → wy
	var lastErr error
	for _, src := range []string{"tx", "wy"} {
		d, err := c.fetchSingerTwoStep(name, src)
		if err == nil && d != nil && (d.Pic != "" || d.Bio != "") {
			if c.cache != nil {
				_ = c.cache.SaveArtist(name, d.Pic, d.Bio, src)
			}
			return d, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return &SingerDetail{Name: name}, nil
}

// RefreshSingerDetail 忽略缓存
func (c *Client) RefreshSingerDetail(name string) (*SingerDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	var lastErr error
	for _, src := range []string{"tx", "wy"} {
		d, err := c.fetchSingerTwoStep(name, src)
		if err == nil && d != nil && (d.Pic != "" || d.Bio != "") {
			if c.cache != nil {
				_ = c.cache.SaveArtist(name, d.Pic, d.Bio, src)
			}
			return d, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return &SingerDetail{Name: name}, nil
}

// fetchSingerTwoStep：先用 search?type=singer 拿 mid + 头像，再用 artistDetail 拿简介
func (c *Client) fetchSingerTwoStep(name, source string) (*SingerDetail, error) {
	// Step 1: 搜索歌手
	params := url.Values{}
	params.Set("source", source)
	params.Set("name", name)
	params.Set("type", "singer")
	params.Set("limit", "5")
	body, err := c.get("/api/music/search", params)
	if err != nil {
		return nil, fmt.Errorf("search singer: %w", err)
	}

	raws := extractList(body)
	if len(raws) == 0 {
		return nil, fmt.Errorf("no singer result for %q", name)
	}

	// 优先取 name 完全匹配的，否则取第一条
	var hit map[string]any
	for _, m := range raws {
		if n, _ := m["name"].(string); n == name {
			hit = m
			break
		}
	}
	if hit == nil {
		hit = raws[0]
	}

	mid := strField(hit, "mid", "singerMid", "singer_mid", "id")
	pic := strField(hit, "picUrl", "pic", "img", "avatar")
	matchedName := strField(hit, "name")
	if matchedName == "" {
		matchedName = name
	}

	// 图片规范化：换 500x500、http → https
	if pic != "" {
		pic = strings.Replace(pic, "R300x300M000", "R500x500M000", 1)
		pic = strings.Replace(pic, "R800x800M000", "R500x500M000", 1)
		if strings.HasPrefix(pic, "http://") {
			pic = "https://" + pic[len("http://"):]
		}
	}

	if mid == "" {
		// 拿不到 mid，只能返回头像
		return &SingerDetail{Name: matchedName, Pic: pic, Source: source}, nil
	}

	// Step 2: artistDetail 拿简介
	bio := ""
	params2 := url.Values{}
	params2.Set("id", mid)
	params2.Set("source", source)
	if body2, err := c.get("/api/music/artistDetail", params2); err == nil {
		bio = extractArtistBio(body2)
		// artistDetail 里可能也有头像，优先用它
		if p2 := extractArtistPicFromDetail(body2); p2 != "" {
			p2 = strings.Replace(p2, "R300x300M000", "R500x500M000", 1)
			if strings.HasPrefix(p2, "http://") {
				p2 = "https://" + p2[len("http://"):]
			}
			pic = p2
		}
	}

	return &SingerDetail{
		Name:   matchedName,
		Pic:    pic,
		Bio:    bio,
		MID:    mid,
		Source: source,
	}, nil
}

// extractArtistBio 从 artistDetail 的响应里提取简介
// 兼容 {desc}、{briefDesc}、{data:{desc}}、{introduction:[{txt}]} 等结构
func extractArtistBio(body []byte) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	if s := bioFromMap(m); s != "" {
		return s
	}
	if data, ok := m["data"].(map[string]any); ok {
		if s := bioFromMap(data); s != "" {
			return s
		}
	}
	return ""
}

func bioFromMap(m map[string]any) string {
	if s, ok := m["desc"].(string); ok && strings.TrimSpace(s) != "" {
		return cleanBio(s)
	}
	if s, ok := m["briefDesc"].(string); ok && strings.TrimSpace(s) != "" {
		return cleanBio(s)
	}
	if s, ok := m["biography"].(string); ok && strings.TrimSpace(s) != "" {
		return cleanBio(s)
	}
	if intro, ok := m["introduction"].([]any); ok && len(intro) > 0 {
		if first, ok := intro[0].(map[string]any); ok {
			if s, ok := first["txt"].(string); ok && strings.TrimSpace(s) != "" {
				return cleanBio(s)
			}
		}
	}
	return ""
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func cleanBio(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	return strings.TrimSpace(s)
}

// extractArtistPicFromDetail 从 artistDetail 里找头像字段
func extractArtistPicFromDetail(body []byte) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	if p := picFromMap(m); p != "" {
		return p
	}
	if data, ok := m["data"].(map[string]any); ok {
		return picFromMap(data)
	}
	return ""
}

func picFromMap(m map[string]any) string {
	for _, k := range []string{"pic", "avatar", "img", "cover", "picUrl", "singerPic"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
