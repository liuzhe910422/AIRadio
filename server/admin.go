package main

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func adminPasswordHash(password, salt string) string {
	key, _ := pbkdf2.Key(sha256.New, password, []byte(salt), 100000, 32)
	return salt + ":" + hex.EncodeToString(key)
}
func (s *sqliteStore) initAdmin() error {
	if _, err := s.exec("CREATE TABLE IF NOT EXISTS admin(id INTEGER PRIMARY KEY CHECK(id=1),username TEXT NOT NULL,password_hash TEXT NOT NULL)"); err != nil {
		return err
	}
	_, err := s.exec("INSERT OR IGNORE INTO admin(id,username,password_hash) VALUES(1,'admin'," + sqliteString(adminPasswordHash("admin123", randomToken())) + ")")
	if err != nil {
		return err
	}
	_, err = s.exec("CREATE TABLE IF NOT EXISTS users(username TEXT PRIMARY KEY,password_hash TEXT NOT NULL,created_at TEXT NOT NULL); INSERT OR IGNORE INTO users(username,password_hash,created_at) SELECT username,password_hash,strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM admin WHERE id=1;")
	return err
}
func (s *server) checkAdmin(username, password string) bool {
	if db, ok := s.store.(*sqliteStore); ok {
		b, err := db.exec("SELECT username,password_hash FROM users WHERE username=" + sqliteString(username))
		if err != nil {
			return false
		}
		var rows []struct {
			Username string `json:"username"`
			Hash     string `json:"password_hash"`
		}
		if json.Unmarshal(b, &rows) != nil || len(rows) != 1 || rows[0].Username != username {
			return false
		}
		salt, _, ok := strings.Cut(rows[0].Hash, ":")
		if !ok {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(rows[0].Hash), []byte(adminPasswordHash(password, salt))) == 1
	}
	s.usersMu.Lock()
	defer s.usersMu.Unlock()
	s.ensureMemoryUsers()
	user, ok := s.users[username]
	if !ok {
		return false
	}
	salt, _, ok := strings.Cut(user.Hash, ":")
	return ok && subtle.ConstantTimeCompare([]byte(user.Hash), []byte(adminPasswordHash(password, salt))) == 1
}
