package alist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/icezxf/musicon-go/internal/settings"
)

// ==================== Manager ====================

type Manager struct {
	Settings *settings.Manager
	mu       sync.Mutex
	cache    map[string]*Client
}

func NewManager(s *settings.Manager) *Manager {
	return &Manager{Settings: s, cache: map[string]*Client{}}
}

// Get 按 providerID 拿 client（缓存复用）
func (m *Manager) Get(id string) *Client {
	if id == "" {
		id = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.cache[id]; ok {
		return c
	}
	c := &Client{
		Settings:   m.Settings,
		ProviderID: id,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
	m.cache[id] = c
	return c
}

// Refresh 清空缓存（设置变更后调用）
func (m *Manager) Refresh() {
	m.mu.Lock()
	m.cache = map[string]*Client{}
	m.mu.Unlock()
}

// ==================== Client ====================

type Client struct {
	Settings   *settings.Manager
	ProviderID string
	http       *http.Client
}

// 兼容旧代码：New 返回默认 client（等价于 Manager.Get("default")）
func New(s *settings.Manager) *Client {
	return &Client{
		Settings:   s,
		ProviderID: "default",
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) cfg() *settings.AListConfig {
	return c.Settings.GetAListByID(c.ProviderID)
}

func (c *Client) base() string {
	return strings.TrimRight(c.cfg().URL, "/")
}

func (c *Client) saveToken(tok string) {
	if c.ProviderID == "" || c.ProviderID == "default" {
		c.Settings.Set("alist_token", tok)
		return
	}
	_ = c.Settings.SetAListTokenByID(c.ProviderID, tok)
}

func (c *Client) ensureToken() (string, error) {
	cfg := c.cfg()
	if cfg.Token != "" {
		return cfg.Token, nil
	}
	if cfg.User == "" || cfg.Pass == "" {
		return "", fmt.Errorf("AList 未配置")
	}
	tok, err := c.Login(cfg.User, cfg.Pass)
	if err != nil {
		return "", err
	}
	c.saveToken(tok)
	return tok, nil
}

func (c *Client) forceRelogin() (string, error) {
	c.saveToken("")
	return c.ensureToken()
}

func (c *Client) Login(user, pass string) (string, error) {
	body := map[string]string{"username": user, "password": pass}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.base()+"/api/auth/login", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 200 || out.Data.Token == "" {
		return "", fmt.Errorf("AList 登录失败: %d %s", out.Code, out.Msg)
	}
	return out.Data.Token, nil
}

type FileEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
}

// ---------- List ----------

func (c *Client) doList(path, token string) ([]FileEntry, error) {
	body := map[string]any{"path": path, "page": 1, "per_page": 1000, "refresh": false}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.base()+"/api/fs/list", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Content []FileEntry `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 200 {
		return nil, fmt.Errorf("alist %d: %s", out.Code, out.Msg)
	}
	return out.Data.Content, nil
}

func (c *Client) List(path string) ([]FileEntry, error) {
	tok, err := c.ensureToken()
	if err != nil {
		return nil, err
	}
	entries, err := c.doList(path, tok)
	if err == nil {
		return entries, nil
	}
	if !isAuthError(err) {
		return nil, err
	}
	tok2, err2 := c.forceRelogin()
	if err2 != nil {
		return nil, fmt.Errorf("alist relogin failed: %w (original: %v)", err2, err)
	}
	return c.doList(path, tok2)
}

// ---------- GetRawURL ----------

func (c *Client) doGetRawURL(path, token string) (string, error) {
	body := map[string]any{"path": path}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", c.base()+"/api/fs/get", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			RawURL string `json:"raw_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 200 {
		return "", fmt.Errorf("alist %d: %s", out.Code, out.Msg)
	}
	return out.Data.RawURL, nil
}

func (c *Client) GetRawURL(path string) (string, error) {
	tok, err := c.ensureToken()
	if err != nil {
		return "", err
	}
	url, err := c.doGetRawURL(path, tok)
	if err == nil {
		return url, nil
	}
	if !isAuthError(err) {
		return "", err
	}
	tok2, err2 := c.forceRelogin()
	if err2 != nil {
		return "", fmt.Errorf("alist relogin failed: %w (original: %v)", err2, err)
	}
	return c.doGetRawURL(path, tok2)
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") ||
		strings.Contains(msg, "token is expired") ||
		strings.Contains(msg, "token is invalidated") ||
		strings.Contains(msg, "unauthorized")
}

// ---------- ReadRange ----------

func (c *Client) ReadRange(rawURL string, start, end int64) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	max := end - start + 1
	return io.ReadAll(io.LimitReader(resp.Body, max+1024))
}
