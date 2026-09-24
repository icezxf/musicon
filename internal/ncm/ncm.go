package ncm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CacheStore 与 lx 包一致，由 db.Holder 实现
type CacheStore interface {
	LoadArtist(name string) (pic, bio, source string, updatedAt time.Time, ok bool)
	SaveArtist(name, pic, bio, source string) error
}

type Client struct {
	BaseURL string
	cache   CacheStore
	http    *http.Client
}

func New(baseURL string, cache CacheStore) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:3000"
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		cache:   cache,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

const artistCacheTTL = 7 * 24 * time.Hour

// ---------- 通用请求 ----------

func (c *Client) get(path string, params url.Values, out any) error {
	u := c.BaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "musicon-go/1.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ncm: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("ncm http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// ---------- 搜索歌曲 ----------

type Song struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Singer   string `json:"singer"`
	Album    string `json:"album"`
	Duration int    `json:"duration"`
	Cover    string `json:"cover"`
}

func (c *Client) Search(keyword string, limit int) ([]Song, error) {
	if keyword == "" {
		return nil, fmt.Errorf("empty keyword")
	}
	if limit <= 0 {
		limit = 30
	}

	var raw struct {
		Result struct {
			Songs []map[string]any `json:"songs"`
		} `json:"result"`
	}
	if err := c.get("/cloudsearch", url.Values{
		"keywords": {keyword},
		"type":     {"1"},
		"limit":    {strconv.Itoa(limit)},
	}, &raw); err != nil {
		return nil, err
	}

	out := make([]Song, 0, len(raw.Result.Songs))
	for _, s := range raw.Result.Songs {
		song := Song{
			ID:       strField(s, "id"),
			Name:     strField(s, "name"),
			Duration: intField(s, "dt") / 1000,
		}
		if artists, ok := s["ar"].([]any); ok {
			var names []string
			for _, a := range artists {
				if m, ok := a.(map[string]any); ok {
					names = append(names, strField(m, "name"))
				}
			}
			song.Singer = strings.Join(names, "、")
		}
		if album, ok := s["al"].(map[string]any); ok {
			song.Album = strField(album, "name")
			song.Cover = strField(album, "picUrl")
		}
		out = append(out, song)
	}
	return out, nil
}

// ---------- 播放链接 ----------

func (c *Client) GetSongURL(songID string) (string, error) {
	if songID == "" {
		return "", fmt.Errorf("empty songID")
	}
	var raw struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := c.get("/song/url", url.Values{"id": {songID}}, &raw); err != nil {
		return "", err
	}
	if len(raw.Data) == 0 || raw.Data[0].URL == "" {
		return "", fmt.Errorf("ncm: no url returned")
	}
	return raw.Data[0].URL, nil
}

// ---------- 艺术家详情 ----------

type ArtistDetail struct {
	Name     string `json:"name"`
	Pic      string `json:"pic"`
	Bio      string `json:"bio"`
	Alias    string `json:"alias"`
	AlbumNum int    `json:"album_num"`
	MusicNum int    `json:"music_num"`
}

func (c *Client) GetArtistDetail(name string) (*ArtistDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}

	// 1. 缓存
	if c.cache != nil {
		pic, bio, _, updatedAt, ok := c.cache.LoadArtist(name)
		if ok && pic != "" && time.Since(updatedAt) < artistCacheTTL {
			return &ArtistDetail{Name: name, Pic: pic, Bio: bio}, nil
		}
	}

	// 2. 搜 ID
	id, err := c.searchArtistID(name)
	if err != nil {
		return nil, err
	}

	// 3. 查详情
	detail, err := c.fetchArtistDetail(id)
	if err != nil {
		return nil, err
	}
	if detail.Name == "" {
		detail.Name = name
	}

	// 4. 回写缓存
	if c.cache != nil && (detail.Pic != "" || detail.Bio != "") {
		_ = c.cache.SaveArtist(name, detail.Pic, detail.Bio, "ncm")
	}
	return detail, nil
}

// searchArtistID 用 /cloudsearch?type=100 搜歌手，优先精确匹配
func (c *Client) searchArtistID(name string) (string, error) {
	var raw struct {
		Result struct {
			Artists []map[string]any `json:"artists"`
		} `json:"result"`
	}
	if err := c.get("/cloudsearch", url.Values{
		"keywords": {name},
		"type":     {"100"},
		"limit":    {"5"},
	}, &raw); err != nil {
		return "", err
	}
	if len(raw.Result.Artists) == 0 {
		return "", fmt.Errorf("ncm: artist %q not found", name)
	}
	for _, a := range raw.Result.Artists {
		if strField(a, "name") == name {
			return strField(a, "id"), nil
		}
	}
	return strField(raw.Result.Artists[0], "id"), nil
}

// fetchArtistDetail 调 /artist/detail，返回 avatar + briefDesc
func (c *Client) fetchArtistDetail(id string) (*ArtistDetail, error) {
	if id == "" {
		return nil, fmt.Errorf("empty artist id")
	}
	var det struct {
		Code int `json:"code"`
		Data struct {
			Artist struct {
				Name      string   `json:"name"`
				Avatar    string   `json:"avatar"`
				Cover     string   `json:"cover"`
				BriefDesc string   `json:"briefDesc"`
				Alias     []string `json:"alias"`
				AlbumSize int      `json:"albumSize"`
				MusicSize int      `json:"musicSize"`
			} `json:"artist"`
		} `json:"data"`
	}
	if err := c.get("/artist/detail", url.Values{"id": {id}}, &det); err != nil {
		return nil, err
	}

	d := &ArtistDetail{
		Name:     det.Data.Artist.Name,
		Pic:      det.Data.Artist.Avatar,
		Bio:      det.Data.Artist.BriefDesc,
		AlbumNum: det.Data.Artist.AlbumSize,
		MusicNum: det.Data.Artist.MusicSize,
	}
	if d.Pic == "" {
		d.Pic = det.Data.Artist.Cover
	}
	if len(det.Data.Artist.Alias) > 0 {
		d.Alias = strings.Join(det.Data.Artist.Alias, "、")
	}

	// briefDesc 为空时，兜底用 /artist/desc
	if d.Bio == "" {
		var desc struct {
			BriefDesc string `json:"briefDesc"`
		}
		if err := c.get("/artist/desc", url.Values{"id": {id}}, &desc); err == nil {
			d.Bio = desc.BriefDesc
		}
	}

	return d, nil
}

// ---------- 健康检查 ----------

func (c *Client) IsAvailable() bool {
	resp, err := c.http.Get(c.BaseURL + "/cloudsearch?keywords=test&type=1&limit=1")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// ---------- 工具 ----------

func strField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return strconv.FormatInt(int64(x), 10)
		}
	}
	return ""
}

func intField(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
