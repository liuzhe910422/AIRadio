package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUsersCreateLoginReset(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{true: "sqlite", false: "memory"}[persistent], func(t *testing.T) {
			s := newServer()
			path := filepath.Join(t.TempDir(), "users.db")
			if persistent {
				db, e := newSQLiteStore(path)
				if e != nil {
					t.Fatal(e)
				}
				s.store = db
			}
			if e := s.saveUser("alice", "initial123", false); e != nil {
				t.Fatal(e)
			}
			if !s.checkAdmin("alice", "initial123") || s.checkAdmin("alice", "wrong") {
				t.Fatal("login failed")
			}
			if e := s.saveUser("alice", "initial123", false); e == nil {
				t.Fatal("duplicate accepted")
			}
			if e := s.saveUser("bad name", "initial123", false); e == nil {
				t.Fatal("bad name accepted")
			}
			if e := s.saveUser("alice", "replacement123", true); e != nil {
				t.Fatal(e)
			}
			if s.checkAdmin("alice", "initial123") || !s.checkAdmin("alice", "replacement123") {
				t.Fatal("reset failed")
			}
			users, e := s.listUsers()
			if e != nil || len(users) != 2 {
				t.Fatalf("list users: %v %v", users, e)
			}
			for _, u := range users {
				if u.Hash != "" {
					t.Fatal("hash exposed")
				}
			}
			if persistent {
				db, e := newSQLiteStore(path)
				if e != nil {
					t.Fatal(e)
				}
				s.store = db
				if !s.checkAdmin("alice", "replacement123") {
					t.Fatal("reset not persisted")
				}
			}
		})
	}
}
func TestUserAPIRequiresLoginAndMasksPassword(t *testing.T) {
	s := newServer()
	a := &apiServer{s: s, sessions: map[string]time.Time{"test": time.Now().Add(time.Hour)}}
	req := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"username":"bob","password":"password123"}`))
	w := httptest.NewRecorder()
	a.usersHandler(w, req)
	if w.Code != 401 {
		t.Fatal("unauthorized create")
	}
	req = httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"username":"bob","password":"password123"}`))
	req.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "test"})
	w = httptest.NewRecorder()
	a.usersHandler(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/users", nil)
	req.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "test"})
	w = httptest.NewRecorder()
	a.usersHandler(w, req)
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "hash") {
		t.Fatal("password exposed")
	}
}
