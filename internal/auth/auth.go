package auth

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const WebCookieName = "musicon_session"

func CreateWebSession(db *sql.DB, username string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	exp := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO web_sessions(token, username, expires_at) VALUES(?,?,?)`,
		token, username, exp); err != nil {
		return "", err
	}
	// 顺手清理过期会话
	db.Exec(`DELETE FROM web_sessions WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339))
	return token, nil
}

func CheckWebSession(db *sql.DB, r *http.Request) (string, bool) {
	c, err := r.Cookie(WebCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	var user, exp string
	err = db.QueryRow(`SELECT username, expires_at FROM web_sessions WHERE token=?`, c.Value).Scan(&user, &exp)
	if err != nil {
		return "", false
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || t.Before(time.Now()) {
		db.Exec(`DELETE FROM web_sessions WHERE token=?`, c.Value)
		return "", false
	}
	return user, true
}

func DeleteWebSession(db *sql.DB, token string) {
	db.Exec(`DELETE FROM web_sessions WHERE token=?`, token)
}

func VerifySubsonic(db *sql.DB, u, p, t, s string) bool {
	if u == "" {
		return false
	}
	var stored string
	err := db.QueryRow(`SELECT password FROM subsonic_users WHERE username=? AND enabled=1`, u).Scan(&stored)
	if err != nil {
		return false
	}
	// 方式 1: 明文 p=（支持 enc: 前缀）
	if p != "" {
		plain := p
		if strings.HasPrefix(p, "enc:") {
			if b, err := hex.DecodeString(p[4:]); err == nil {
				plain = string(b)
			}
		}
		return subtle.ConstantTimeCompare([]byte(plain), []byte(stored)) == 1
	}
	// 方式 2: t=md5(password+salt)
	if t != "" && s != "" {
		h := md5.Sum([]byte(stored + s))
		expect := hex.EncodeToString(h[:])
		return subtle.ConstantTimeCompare([]byte(expect), []byte(t)) == 1
	}
	return false
}

func EnsureDefaultUser(db *sql.DB, user, pass string) error {
	if user == "" || pass == "" {
		return nil
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM subsonic_users WHERE username=?`, user).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := db.Exec(`INSERT INTO subsonic_users(username, password, role) VALUES(?,?,'admin')`, user, pass)
		return err
	}
	return nil
}