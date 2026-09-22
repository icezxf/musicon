package alist

import (
"bytes"
"encoding/json"
"fmt"
"io"
"net/http"
"strings"
"time"
)

type Client struct {
base  string
token string
http  *http.Client
}

func New(base, token string) *Client {
return &Client{
base:  strings.TrimRight(base, "/"),
token: token,
http:  &http.Client{Timeout: 30 * time.Second},
}
}

type FileEntry struct {
Name     string `json:"name"`
Size     int64  `json:"size"`
IsDir    bool   `json:"is_dir"`
Modified string `json:"modified"`
}

func (c *Client) List(path string) ([]FileEntry, error) {
body := map[string]any{"path": path, "page": 1, "per_page": 1000, "refresh": false}
buf, _ := json.Marshal(body)
req, _ := http.NewRequest("POST", c.base+"/api/fs/list", bytes.NewReader(buf))
req.Header.Set("Content-Type", "application/json")
if c.token != "" {
req.Header.Set("Authorization", c.token)
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

func (c *Client) GetRawURL(path string) (string, error) {
body := map[string]any{"path": path}
buf, _ := json.Marshal(body)
req, _ := http.NewRequest("POST", c.base+"/api/fs/get", bytes.NewReader(buf))
req.Header.Set("Content-Type", "application/json")
if c.token != "" {
req.Header.Set("Authorization", c.token)
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
