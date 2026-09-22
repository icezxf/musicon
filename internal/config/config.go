package config

import "os"

type Config struct {
	Listen    string
	DBPath    string
	DataDir   string
	StaticDir string
	WebUser   string
	WebPass   string
}

func Load() *Config {
	return &Config{
		Listen:    env("LISTEN", "0.0.0.0:8000"),
		DBPath:    env("DB_PATH", "./data/musicon.db"),
		DataDir:   env("DATA_DIR", "./data"),
		StaticDir: env("STATIC_DIR", "./static"),
		WebUser:   env("WEB_USER", "admin"),
		WebPass:   env("WEB_PASS", "admin"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
