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

type Client struct {
	Settings *settings.Manager
	http     *http.Client
}

func New(s *settings.Manager) *Client {
	return &Client{
		Settings: s,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

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

func (c *Client) post(path string, payload any) ([]byte, error) {
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

// Search 搜索歌曲。source 可为 tx/wy/kg/kw/mg 等，默认 tx。
func (c *Client) Search(query, source string, page, limit int) ([]Song, error) {
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	if source == "" {
		source = "tx"
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

// extractSongList 尝试多种响应结构，提取歌曲数组。
// 兼容:
//   {code, data:{list:[...]}}
//   {list:[...]}
//   {data:[...]}
//   [...]
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
			}
		}
	}
	return ""
}

// parseDuration 支持 "03:35"、"215"、"215000" 三种格式，统一返回秒。
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
		if n > 10000 { // 毫秒
			return n / 1000
		}
		return n
	}
	return 0
}

// ============ 播放 URL ============

// GetSongURL 通过 lxserver 拿到歌曲的真实播放地址。
func (c *Client) GetSongURL(songID, source string) (string, error) {
	if songID == "" {
		return "", fmt.Errorf("empty songID")
	}
	if source == "" {
		source = "tx"
	}
	payload := map[string]string{
		"source":  source,
		"songId":  songID,
		"quality": "320k",
	}
	body, err := c.post("/api/music/url", payload)
	if err != nil {
		return "", err
	}
	if u := extractURL(body); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("cannot parse url from lx response: %s", truncate(string(body), 200))
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
		source = "tx"
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

// ============ 歌手 ============

type SingerDetail struct {
	Name   string `json:"name"`
	Pic    string `json:"pic"`
	Bio    string `json:"bio"`
	MID    string `json:"mid"`
	Source string `json:"source"`
}

func (c *Client) GetSingerDetail(name string) (*SingerDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	params := url.Values{}
	params.Set("name", name)
	body, err := c.get("/api/music/singer/detail", params)
	if err != nil {
		return nil, err
	}
	var d SingerDetail
	if err := json.Unmarshal(body, &d); err == nil && (d.Name != "" || d.Pic != "") {
		return &d, nil
	}
	var r struct {
		Data SingerDetail `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err == nil {
		return &r.Data, nil
	}
	return &d, nil
}
