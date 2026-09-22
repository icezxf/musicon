package auth

import (
"crypto/md5"
"crypto/rand"
"crypto/subtle"
"database/sql"
"encoding/hex"
"net/http"
"time"
)

const WebCookieName = "musicon_session"

func CreateWebSession(db *sql.DB, username string) (string, error) {
b := make([]byte, 32)
rand.Read(b)
token := hex.EncodeToString(b)
exp := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
_, err := db.Exec(`INSERT INTO web_sessions(token, username, expires_at) VALUES(?,?,?)`, token, username, exp)
return token, err
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
if p != "" {
return subtle.ConstantTimeCompare([]byte(p), []byte(stored)) == 1
}
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
db.QueryRow(`SELECT COUNT(*) FROM subsonic_users WHERE username=?`, user).Scan(&n)
if n == 0 {
_, err := db.Exec(`INSERT INTO subsonic_users(username, password, role) VALUES(?,?,'admin')`, user, pass)
return err
}
return nil
}
