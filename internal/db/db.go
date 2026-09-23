package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	// 增加 synchronous(NORMAL) 提升写入性能，WAL 模式下安全[reference:0][reference:1]
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 从 1 改为 4/2，避免扫描时 HTTP 请求被阻塞[reference:2]
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	schema := []string{
		// ... 此处保持原样，省略 ...
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}

	// 增量迁移：给旧库补列（列已存在会报错，忽略即可）
	alters := []string{
		"ALTER TABLE songs ADD COLUMN track_number INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN disc_number INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN year INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN composer TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE songs ADD COLUMN bitrate INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN channels INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE songs ADD COLUMN isrc TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE songs ADD COLUMN bpm INTEGER NOT NULL DEFAULT 0",
	}
	for _, a := range alters {
		if _, err := db.Exec(a); err != nil {
			// 只忽略 "duplicate column name" 错误，其他错误应返回[reference:3]
			if !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("migrate %q: %w", a, err)
			}
		}
	}
	return nil
}