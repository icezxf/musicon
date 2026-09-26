package settings

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

type Manager struct {
	db       *sql.DB
	mu       sync.RWMutex
	reloadMu sync.Mutex
	cache    map[string]string
	loadedAt time.Time
}

func New(db *sql.DB) *Manager {
	m := &Manager{db: db, cache: map[string]string{}}
	m.reload()
	return m
}

func (m *Manager) Get(key string) string {
	m.mu.RLock()
	if time.Since(m.loadedAt) < 10*time.Second {
		v := m.cache[key]
		m.mu.RUnlock()
		return v
	}
	m.mu.RUnlock()
	m.reload()
	m.mu.RLock()
	v := m.cache[key]
	m.mu.RUnlock()
	return v
}

func (m *Manager) Set(key, val string) error {
	_, err := m.db.Exec(
		`INSERT INTO app_settings(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`,
		key, val)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cache[key] = val
	m.mu.Unlock()
	return nil
}

func (m *Manager) GetAll() map[string]string {
	m.reload()
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.cache))
	for k, v := range m.cache {
		out[k] = v
	}
	return out
}

func (m *Manager) reload() {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	m.mu.RLock()
	fresh := time.Since(m.loadedAt) < time.Second
	m.mu.RUnlock()
	if fresh {
		return
	}

	defer func() {
		m.mu.Lock()
		m.loadedAt = time.Now()
		m.mu.Unlock()
	}()

	rows, err := m.db.Query(`SELECT key, value FROM app_settings`)
	if err != nil {
		return
	}
	defer rows.Close()
	newCache := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		newCache[k] = v
	}
	m.mu.Lock()
	m.cache = newCache
	m.mu.Unlock()
}

// ==================== AList 配置 ====================

type AListConfig struct {
	URL      string
	User     string
	Pass     string
	Token    string
	Provider string
}

// GetAList 返回默认 alist 配置（兼容旧代码）
func (m *Manager) GetAList() *AListConfig {
	c := &AListConfig{
		URL:   m.Get("alist_url"),
		User:  m.Get("alist_user"),
		Pass:  m.Get("alist_password"),
		Token: m.Get("alist_token"),
	}
	raw := m.Get("storage_providers")
	if raw != "" {
		var list []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Config struct {
				BaseURL         string `json:"base_url"`
				RefreshUsername string `json:"refresh_username"`
				RefreshPassword string `json:"refresh_password"`
				Token           string `json:"token"`
			} `json:"config"`
		}
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			defaultID := m.Get("storage_default_provider_id")
			for _, p := range list {
				if p.Type != "alist" {
					continue
				}
				if defaultID != "" && p.ID != defaultID {
					continue
				}
				if p.Config.BaseURL != "" {
					c.URL = p.Config.BaseURL
				}
				if p.Config.RefreshUsername != "" {
					c.User = p.Config.RefreshUsername
				}
				if p.Config.RefreshPassword != "" {
					c.Pass = p.Config.RefreshPassword
				}
				if p.Config.Token != "" {
					c.Token = p.Config.Token
				}
				c.Provider = p.ID
				break
			}
		}
	}
	return c
}

// GetAListByID 按 provider ID 读配置；id 为空/"default" 时走默认逻辑
func (m *Manager) GetAListByID(id string) *AListConfig {
	if id == "" || id == "default" {
		return m.GetAList()
	}
	c := &AListConfig{Provider: id}
	raw := m.Get("storage_providers")
	if raw == "" {
		return m.GetAList()
	}
	var list []struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Config struct {
			BaseURL         string `json:"base_url"`
			RefreshUsername string `json:"refresh_username"`
			RefreshPassword string `json:"refresh_password"`
			Token           string `json:"token"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return m.GetAList()
	}
	for _, p := range list {
		if p.ID != id || p.Type != "alist" {
			continue
		}
		c.URL = p.Config.BaseURL
		c.User = p.Config.RefreshUsername
		c.Pass = p.Config.RefreshPassword
		c.Token = p.Config.Token
		return c
	}
	return m.GetAList()
}

// SetAListTokenByID 把 token 写回 storage_providers JSON 对应位置
func (m *Manager) SetAListTokenByID(id, tok string) error {
	raw := m.Get("storage_providers")
	if raw == "" {
		return m.Set("alist_token", tok)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return m.Set("alist_token", tok)
	}
	changed := false
	for _, p := range list {
		if pid, _ := p["id"].(string); pid == id {
			cfg, _ := p["config"].(map[string]any)
			if cfg == nil {
				cfg = map[string]any{}
			}
			cfg["token"] = tok
			p["config"] = cfg
			changed = true
			break
		}
	}
	if !changed {
		return m.Set("alist_token", tok)
	}
	b, _ := json.Marshal(list)
	return m.Set("storage_providers", string(b))
}

// ==================== 其他配置 ====================

func (m *Manager) GetScanPath() string {
	if v := m.Get("last_scan_path"); v != "" {
		return v
	}
	return m.Get("scan_path")
}

func (m *Manager) GetLXURL() string {
	if v := m.Get("lx_server_url"); v != "" {
		return v
	}
	return "http://127.0.0.1:9527"
}

func (m *Manager) GetNCMMode() string {
	if v := m.Get("ncm_mode"); v != "" {
		return v
	}
	return "internal"
}

func (m *Manager) GetNCMEmbeddedPort() int {
	if v := m.Get("ncm_embedded_port"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3000
}

func (m *Manager) GetNCMURL() string {
	if v := m.Get("ncm_server_url"); v != "" {
		return v
	}
	return "http://127.0.0.1:3000"
}
