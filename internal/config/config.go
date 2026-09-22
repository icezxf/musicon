package config

import "os"

type Config struct {
Listen      string
DBPath      string
DataDir     string
StaticDir   string
AListURL    string
AListToken  string
LXServerURL string
WebUser     string
WebPass     string
}

func Load() *Config {
return &Config{
Listen:      env("LISTEN", "0.0.0.0:8000"),
DBPath:      env("DB_PATH", "./data/musicon.db"),
DataDir:     env("DATA_DIR", "./data"),
StaticDir:   env("STATIC_DIR", "./static"),
AListURL:    env("AListURL", ""),
AListToken:  env("AListToken", ""),
LXServerURL: env("LXServerURL", "http://127.0.0.1:9527"),
WebUser:     env("WEB_USER", "fire"),
WebPass:     env("WEB_PASS", "boc81838"),
}
}

func env(k, def string) string {
if v := os.Getenv(k); v != "" {
return v
}
return def
}
