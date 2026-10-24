package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type consoleUser struct {
	Username  string `json:"username"`
	CreatedAt string `json:"created_at"`
	Hash      string `json:"-"`
}

var validUsername = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func (s *server) listUsers() ([]consoleUser, error) {
	s.usersMu.Lock()
	defer s.usersMu.Unlock()
	if db, ok := s.store.(*sqliteStore); ok {
		b, e := db.exec("SELECT username,created_at FROM users ORDER BY username")
		if e != nil {
			return nil, e
		}
		rows := []consoleUser{}
		if len(b) > 0 {
			e = json.Unmarshal(b, &rows)
		}
		return rows, e
	}
	s.ensureMemoryUsers()
	out := []consoleUser{}
	for _, u := range s.users {
		u.Hash = ""
		out = append(out, u)
	}
	return out, nil
}
func (s *server) ensureMemoryUsers() {
	if s.users == nil {
		s.users = map[string]consoleUser{"admin": {Username: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339), Hash: adminPasswordHash("admin123", randomToken())}}
	}
}
func (s *server) saveUser(name, password string, reset bool) error {
	name = strings.TrimSpace(name)
	if !validUsername.MatchString(name) {
		return errors.New("用户名须为1-32位字母、数字、下划线或短横线")
	}
	if len(password) < 6 || len(password) > 128 {
		return errors.New("密码长度须为6-128字节")
	}
	hash := adminPasswordHash(password, randomToken())
	s.usersMu.Lock()
	defer s.usersMu.Unlock()
	if db, ok := s.store.(*sqliteStore); ok {
		b, e := db.exec("SELECT username FROM users WHERE username=" + sqliteString(name))
		if e != nil {
			return errors.New("读取用户失败")
		}
		exists := len(strings.TrimSpace(string(b))) > 0
		if reset && !exists {
			return errors.New("用户不存在")
		}
		if !reset && exists {
			return errors.New("用户名已存在")
		}
		q := "INSERT INTO users(username,password_hash,created_at) VALUES(" + sqliteString(name) + "," + sqliteString(hash) + "," + sqliteString(time.Now().UTC().Format(time.RFC3339)) + ")"
		if reset {
			q = "UPDATE users SET password_hash=" + sqliteString(hash) + " WHERE username=" + sqliteString(name)
		}
		if _, e = db.exec(q); e != nil {
			return errors.New("保存用户失败")
		}
		return nil
	}
	s.ensureMemoryUsers()
	u, exists := s.users[name]
	if reset && !exists {
		return errors.New("用户不存在")
	}
	if !reset && exists {
		return errors.New("用户名已存在")
	}
	if !exists {
		u = consoleUser{Username: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	u.Hash = hash
	s.users[name] = u
	return nil
}
func (a *apiServer) usersHandler(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.URL.Path == "/api/users" && r.Method == "GET" {
		rows, e := a.s.listUsers()
		if e != nil {
			http.Error(w, "读取用户失败", 500)
			return
		}
		a.json(w, rows)
		return
	}
	reset := strings.HasPrefix(r.URL.Path, "/api/users/") && strings.HasSuffix(r.URL.Path, "/password")
	if (!reset && (r.URL.Path != "/api/users" || r.Method != "POST")) || (reset && r.Method != "PUT") {
		w.WriteHeader(405)
		return
	}
	var x struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&x) != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	if reset {
		x.Username = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/password")
	}
	if e := a.s.saveUser(x.Username, x.Password, reset); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	// Password resets invalidate existing sessions; every user can log in again.
	if reset {
		a.mu.Lock()
		a.sessions = map[string]time.Time{}
		a.mu.Unlock()
	}
	a.json(w, map[string]bool{"ok": true})
}
