package config

import (
	"log"
	"os"
)

type Config struct {
	Listen    string
	DBPath    string
	DataDir   string
	StaticDir string
	WebUser   string
	WebPass   string
}

func Load() *Config {
	cfg := &Config{
		Listen:    env("LISTEN", "0.0.0.0:8000"),
		DBPath:    env("DB_PATH", "./data/musicon.db"),
		DataDir:   env("DATA_DIR", "./data"),
		StaticDir: env("STATIC_DIR", "./static"),
		WebUser:   env("WEB_USER", "admin"),
		WebPass:   env("WEB_PASS", "admin"),
	}
	if cfg.WebUser == "admin" && cfg.WebPass == "admin" {
		log.Println("========================================================")
		log.Println("  !! 警告：正在使用默认账号密码 admin/admin !!")
		log.Println("  请设置环境变量 WEB_USER / WEB_PASS 后重启")
		log.Println("  否则任何人都可以登录你的音乐服务")
		log.Println("========================================================")
	}
	return cfg
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
