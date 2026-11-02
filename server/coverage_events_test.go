package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMemoryEventStoreLifecycle(t *testing.T) {
	s := newMemoryEventStore()
	if _, err := s.CreateEvent("bad!", "creator", "x", time.Now().Add(time.Hour).Format(time.RFC3339)); err == nil {
		t.Fatal("invalid id accepted")
	}
	if _, err := s.CreateEvent("dev", "creator", "", time.Now().Add(time.Hour).Format(time.RFC3339)); err == nil {
		t.Fatal("empty content accepted")
	}
	if _, err := s.CreateEvent("dev", "creator", "x", "bad"); err == nil {
		t.Fatal("invalid time accepted")
	}
	if _, err := s.CreateEvent("dev", "creator", "x", time.Now().Add(-time.Hour).Format(time.RFC3339)); err == nil {
		t.Fatal("past event accepted")
	}
	e, err := s.CreateEvent("dev", "creator", "提醒", time.Now().Add(time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ListEvents("dev")) != 1 || len(s.AllEvents()) != 1 {
		t.Fatal("event not listed")
	}
	if _, err = s.SetEventStatus(e.ID, "other", "completed"); err == nil {
		t.Fatal("unauthorized update accepted")
	}
	if _, err = s.SetEventStatus(e.ID, "dev", "bad"); err == nil {
		t.Fatal("bad status accepted")
	}
	if _, err = s.SetEventStatus(e.ID, "dev", "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetEventStatus(e.ID, "dev", "cancelled"); err == nil {
		t.Fatal("ended event changed")
	}
	e2, _ := s.CreateEvent("dev", "creator", "逾期", time.Now().Add(10*time.Minute).Format(time.RFC3339))
	if len(s.DueEvents(time.Now())) != 1 {
		t.Fatal("due event missing")
	}
	if _, err = s.MarkOverdue(e2.ID); err != nil {
		t.Fatal(err)
	}
	if got := eventText(e2, "前缀"); got != "前缀：逾期" {
		t.Fatal(got)
	}
}

func TestImageAPIAndAlertHelpers(t *testing.T) {
	s := newServer()
	a := &apiServer{s: s, sessions: map[string]time.Time{}}
	w := httptest.NewRecorder()
	a.image(w, httptest.NewRequest("GET", "/api/intercom/images/missing", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	a.image(w, httptest.NewRequest("POST", "/api/intercom/images/x", bytes.NewBufferString("bad")))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	v := true
	if boolValue(&v) != true || boolValue(nil) != nil {
		t.Fatal("boolValue")
	}
	f := 1.2
	if pointerValue(&f) != f || pointerValue(nil) != nil {
		t.Fatal("pointerValue")
	}
	if _, ok := legacyRooms([]room{{ID: "x"}})[0]["id"]; !ok {
		t.Fatal("legacy room")
	}
}

func TestAPIJSONAndAuthBranches(t *testing.T) {
	s := newServer()
	a := &apiServer{s: s, sessions: map[string]time.Time{"ok": time.Now().Add(time.Hour)}}
	w := httptest.NewRecorder()
	a.json(w, map[string]string{"ok": "yes"})
	var v map[string]string
	if json.Unmarshal(w.Body.Bytes(), &v) != nil || v["ok"] != "yes" {
		t.Fatal("json")
	}
	w = httptest.NewRecorder()
	if a.auth(w, httptest.NewRequest("GET", "/", nil)) || w.Code != 401 {
		t.Fatal("auth")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "ok"})
	w = httptest.NewRecorder()
	if !a.auth(w, r) {
		t.Fatal("valid auth")
	}
}
