package lx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
		http:     &http.Client{Timeout: 12 * time.Second},
	}
}

type SingerDetail struct {
	Name   string `json:"name"`
	Pic    string `json:"pic"`
	Bio    string `json:"bio"`
	MID    string `json:"mid"`
	Source string `json:"source"`
}

func (c *Client) base() string {
	if v := c.Settings.Get("lx_server_url"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:9527"
}

func (c *Client) GetSingerDetail(name string) (*SingerDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	u := c.base() + "/api/music/singer/detail?name=" + url.QueryEscape(name)
	resp, err := c.http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lx %d", resp.StatusCode)
	}
	var d SingerDetail
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}
