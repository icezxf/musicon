package lx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/icezxf/musicon-go/internal/settings"
)

// CacheStore 由 db.Holder 实现，用于歌手信息的持久化缓存
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

// 缓存 7 天
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
	raws := extractSongList(body)
	out := make([]Song, 0, len(raws))
	for _, m := range raws {
		out = append(out, parseSong(m))
	}
	return out, nil
}

func extractSongList(body []byte) []map[string]any {
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
	var r4 struct {
		Success bool `json:"success"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r4); err == nil && r4.Success && r4.Data.URL != "" {
		return r4.Data.URL
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

// ============ 歌手（带缓存） ============

type SingerDetail struct {
	Name   string `json:"name"`
	Pic    string `json:"pic"`
	Bio    string `json:"bio"`
	MID    string `json:"mid"`
	Source string `json:"source"`
}

// GetSingerDetail 查询歌手详情。命中缓存则直接返回，未命中才打 LX。
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

	// 2. 打 LX
	d, err := c.fetchSinger(name)
	if err != nil {
		return nil, err
	}

	// 3. 回写缓存
	if c.cache != nil && (d.Pic != "" || d.Bio != "") {
		_ = c.cache.SaveArtist(name, d.Pic, d.Bio, d.Source)
	}
	return d, nil
}

// RefreshSingerDetail 忽略缓存，强制刷新
func (c *Client) RefreshSingerDetail(name string) (*SingerDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	d, err := c.fetchSinger(name)
	if err != nil {
		return nil, err
	}
	if c.cache != nil && (d.Pic != "" || d.Bio != "") {
		_ = c.cache.SaveArtist(name, d.Pic, d.Bio, d.Source)
	}
	return d, nil
}

func (c *Client) fetchSinger(name string) (*SingerDetail, error) {
	params := url.Values{}
	params.Set("name", name)
	body, err := c.get("/api/music/singer/detail", params)
	if err != nil {
		return nil, err
	}
	var d SingerDetail
	if err := json.Unmarshal(body, &d); err == nil && (d.Name != "" || d.Pic != "") {
		if d.Name == "" {
			d.Name = name
		}
		return &d, nil
	}
	var r struct {
		Data SingerDetail `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err == nil {
		if r.Data.Name == "" {
			r.Data.Name = name
		}
		return &r.Data, nil
	}
	return &SingerDetail{Name: name}, nil
}
