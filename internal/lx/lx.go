package lx

import (
"encoding/json"
"fmt"
"net/http"
"net/url"
"strings"
"time"
)

type Client struct {
base string
http *http.Client
}

func New(base string) *Client {
return &Client{
base: strings.TrimRight(base, "/"),
http: &http.Client{Timeout: 12 * time.Second},
}
}

type SingerDetail struct {
Name   string `json:"name"`
Pic    string `json:"pic"`
Bio    string `json:"bio"`
MID    string `json:"mid"`
Source string `json:"source"`
}

func (c *Client) GetSingerDetail(name string) (*SingerDetail, error) {
if c.base == "" || name == "" {
return nil, fmt.Errorf("lx not configured")
}
u := c.base + "/api/music/singer/detail?name=" + url.QueryEscape(name)
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
