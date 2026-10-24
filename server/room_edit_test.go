package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoomEditAndClose(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "sqlite"}[sqlite], func(t *testing.T) {
			var store roomStore = newMemoryStore()
			path := filepath.Join(t.TempDir(), "rooms.db")
			if sqlite {
				db, e := newSQLiteStore(path)
				if e != nil {
					t.Fatal(e)
				}
				store = db
			}
			store.RegisterDevice("a", "")
			store.RegisterDevice("b", "")
			r, _ := store.CreateRoom("old", "a", []string{"b"})
			updated, e := store.UpdateRoom(r.ID, "new", []string{"b", "c"})
			if e != nil || updated.Name != "new" || updated.JoinCode != r.JoinCode {
				t.Fatalf("update: %+v %v", updated, e)
			}
			if store.CurrentRoom("b").ID != "default" {
				t.Fatal("edit moved invited device")
			}
			if _, e = store.UpdateRoom("default", "bad", nil); e == nil {
				t.Fatal("default edited")
			}
			if _, e = store.CloseRoom(r.ID); e != nil {
				t.Fatal(e)
			}
			if store.CurrentRoom("a").ID != "default" {
				t.Fatal("close did not restore default")
			}
			if sqlite {
				db, e := newSQLiteStore(path)
				if e != nil || db.CurrentRoom("a").ID != "default" {
					t.Fatal("close not persisted")
				}
			}
		})
	}
}
func TestRoomEditAPI(t *testing.T) {
	s := newServer()
	r, _ := s.store.CreateRoom("old", "a", nil)
	a := &apiServer{s: s, sessions: map[string]time.Time{"test": time.Now().Add(time.Hour)}}
	req := httptest.NewRequest("PATCH", "/api/rooms/"+r.ID, strings.NewReader(`{"name":"edited","invite_device_ids":["b"]}`))
	req.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "test"})
	w := httptest.NewRecorder()
	a.roomAction(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if s.store.CurrentRoom("b").ID != "default" {
		t.Fatal("invitation moved device")
	}
}

func TestSQLiteJoinSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := newSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	db.RegisterDevice("creator", "")
	db.RegisterDevice("004", "")
	r, err := db.CreateRoom("444", "creator", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Join("004", r.JoinCode); err != nil {
		t.Fatal(err)
	}
	restored, err := newSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.CurrentRoom("004"); got.ID != r.ID {
		t.Fatalf("restored room=%+v want=%s", got, r.ID)
	}
	if got := restored.RegisterDevice("004", ""); got.ID != r.ID {
		t.Fatalf("hello reset room: %+v", got)
	}
}
