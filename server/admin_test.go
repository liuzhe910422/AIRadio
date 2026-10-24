package main

import (
	"path/filepath"
	"testing"
)

func TestDatabaseAdmin(t *testing.T) {
	db, e := newSQLiteStore(filepath.Join(t.TempDir(), "admin.db"))
	if e != nil {
		t.Fatal(e)
	}
	s := newServer()
	s.store = db
	if !s.checkAdmin("admin", "admin123") {
		t.Fatal("new password rejected")
	}
	if s.checkAdmin("admin", "admin") || s.checkAdmin("other", "admin123") {
		t.Fatal("wrong credentials accepted")
	}
}
