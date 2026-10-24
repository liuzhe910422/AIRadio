package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAdminSessions(t *testing.T) {
	s := newServer()
	s.adminPassword = "configured-password"
	a := &apiServer{s: s, sessions: map[string]time.Time{}}
	login := func(password string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
		w := httptest.NewRecorder()
		a.login(w, httptest.NewRequest("POST", "/api/login", strings.NewReader(string(body))))
		return w
	}
	if w := login("admin"); w.Code != 401 {
		t.Fatalf("default password accepted: %d", w.Code)
	}
	first, second := login(s.adminPassword), login(s.adminPassword)
	if first.Code != 200 || second.Code != 200 {
		t.Fatal("configured password rejected")
	}
	c1, c2 := first.Result().Cookies()[0], second.Result().Cookies()[0]
	if c1.Value == c2.Value || c1.Value == "admin-session" || !c1.HttpOnly || c1.SameSite != http.SameSiteStrictMode || c1.MaxAge != 43200 {
		t.Fatalf("unsafe session cookie: %+v", c1)
	}
	request := func(token string) bool {
		r := httptest.NewRequest("GET", "/api/rooms", nil)
		r.AddCookie(&http.Cookie{Name: "airadio_admin", Value: token})
		return a.auth(httptest.NewRecorder(), r)
	}
	if request("admin-session") || !request(c1.Value) {
		t.Fatal("session validation failed")
	}
	a.sessions[c1.Value] = time.Now().Add(-time.Second)
	if request(c1.Value) {
		t.Fatal("expired session accepted")
	}
	r := httptest.NewRequest("POST", "https://example.test/api/login", strings.NewReader(`{"username":"admin","password":"configured-password"}`))
	w := httptest.NewRecorder()
	a.login(w, r)
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie missing Secure")
	}
}

func newSQLiteTestStore(t *testing.T) *sqliteStore {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	s, err := newSQLiteStore(filepath.Join(t.TempDir(), "rooms.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLiteMembershipSurvivesRestart(t *testing.T) {
	s := newSQLiteTestStore(t)
	s.RegisterDevice("a", "")
	s.RegisterDevice("b", "")
	r, err := s.CreateRoom("调度组", "a", []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join("b", r.JoinCode); err != nil {
		t.Fatal(err)
	}
	restored, err := newSQLiteStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if got := restored.RegisterDevice(id, ""); got.ID != r.ID {
			t.Fatalf("%s lost membership: %+v", id, got)
		}
	}
	if _, err := restored.CloseRoom(r.ID); err != nil {
		t.Fatal(err)
	}
	restored, err = newSQLiteStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if got := restored.CurrentRoom(id); got.ID != "default" {
			t.Fatalf("%s still in closed room: %+v", id, got)
		}
	}
}

func TestSQLiteConcurrentMembership(t *testing.T) {
	s := newSQLiteTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("device-%d", i)
			s.RegisterDevice(id, "")
			r, err := s.CreateRoom(id, id, nil)
			if err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < 3; j++ {
				s.Leave(id)
				if _, err := s.Join(id, r.JoinCode); err != nil {
					t.Error(err)
					return
				}
				s.ListDevices()
			}
		}(i)
	}
	wg.Wait()
	restored, err := newSQLiteStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range s.ListDevices() {
		if restored.CurrentRoom(d.ID).ID != d.RoomID {
			t.Fatalf("stale persisted membership for %s", d.ID)
		}
	}
}

func testPeer(t *testing.T, s *server, id string) *peer {
	t.Helper()
	p := &peer{out: make(chan outgoing, 32)}
	if err := s.register(p, id); err != nil {
		t.Fatal(err)
	}
	return p
}

func queuedControls(p *peer) []control {
	var out []control
	for len(p.out) > 0 {
		msg := <-p.out
		if msg.kind == websocket.TextMessage {
			var c control
			json.Unmarshal(msg.data, &c)
			out = append(out, c)
		}
	}
	return out
}

func TestRoomChangeAbortsReceiveAndPreservesSender(t *testing.T) {
	s := newServer()
	sender, receiver := testPeer(t, s, "sender"), testPeer(t, s, "receiver")
	r, err := s.store.CreateRoom("另一房间", "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.start(sender, control{MessageID: "voice", FrameMS: 60, SampleRate: 16000})
	queuedControls(sender)
	queuedControls(receiver)
	token := receiver.playbackToken()
	receiver.hosted.Store(true)
	if _, err := s.changeRoom(receiver.id, func() (room, error) { return s.store.Join(receiver.id, "invalid") }); err == nil {
		t.Fatal("invalid room accepted")
	}
	if receiver.receiving != "voice" || receiver.playbackToken() != token || !receiver.hosted.Load() || len(receiver.out) != 0 {
		t.Fatal("failed join changed active view")
	}
	if _, err := s.changeRoom(receiver.id, func() (room, error) { return s.store.Join(receiver.id, r.JoinCode) }); err != nil {
		t.Fatal(err)
	}
	got := queuedControls(receiver)
	if len(got) != 2 || got[0].Type != "abort" || got[0].MessageID != "voice" || got[1].Type != "hosting_state" {
		t.Fatalf("missing migration controls: %+v", got)
	}
	if receiver.receiving != "" || receiver.ready || receiver.hosted.Load() || receiver.playbackToken() == token {
		t.Fatal("old room view retained")
	}
	if _, err := s.changeRoom(sender.id, func() (room, error) { return s.store.Leave(sender.id), nil }); err == nil {
		t.Fatal("speaking sender allowed to move")
	}
	if err := s.frame(sender, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if len(receiver.out) != 0 {
		t.Fatal("audio crossed rooms")
	}
	if err := s.end(sender, control{MessageID: "voice", FrameCount: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRoomUpdatesOnlineMembers(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			s := newServer()
			a, b := testPeer(t, s, "a"), testPeer(t, s, "b")
			r, err := s.store.CreateRoom("调度组", a.id, nil)
			if err != nil {
				t.Fatal(err)
			}
			s.store.Join(b.id, r.JoinCode)
			a.hosted.Store(true)
			b.hosted.Store(true)
			tokenA, tokenB := a.playbackToken(), b.playbackToken()
			if automatic {
				s.active = &peer{id: a.id, sending: "voice", automatic: true, chatRoom: r.ID}
				a.receiving, a.ready = "voice", true
				b.receiving, b.ready = "voice", true
			} else {
				s.start(a, control{MessageID: "voice", FrameMS: 60, SampleRate: 16000})
			}
			queuedControls(a)
			queuedControls(b)
			api := &apiServer{s: s, sessions: map[string]time.Time{"test": time.Now().Add(time.Hour)}}
			request := func(method string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, "/api/rooms/"+r.ID+"/close", nil)
				req.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "test"})
				w := httptest.NewRecorder()
				api.handler(w, req)
				return w
			}
			if w := request("GET"); w.Code != 405 || s.active == nil {
				t.Fatal("GET closed room")
			}
			if w := request("POST"); w.Code != 200 {
				t.Fatalf("close room failed: %d %s", w.Code, w.Body.String())
			}
			if s.active != nil {
				t.Fatal("closed room retained active sender")
			}
			for _, p := range []*peer{a, b} {
				if p.hosted.Load() || p.receiving != "" || s.store.CurrentRoom(p.id).ID != "default" {
					t.Fatal("member state not reset")
				}
				controls := queuedControls(p)
				last := controls[len(controls)-1]
				if last.Type != "room_state" || last.RoomID != "default" || last.ChatSessionID != p.playbackToken() || last.PlaybackToken != last.ChatSessionID {
					t.Fatalf("invalid new room state: %+v", last)
				}
			}
			if a.playbackToken() == tokenA || b.playbackToken() == tokenB {
				t.Fatal("view tokens retained")
			}
		})
	}
}

func TestSwitchingPeerCannotStartTalk(t *testing.T) {
	s := newServer()
	p := testPeer(t, s, "a")
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error)
	go func() {
		_, err := s.changeRoom(p.id, func() (room, error) {
			close(entered)
			<-release
			return s.store.Leave(p.id), nil
		})
		done <- err
	}()
	<-entered
	queuedControls(p)
	s.start(p, control{MessageID: "voice", FrameMS: 60, SampleRate: 16000})
	if s.active != nil {
		t.Fatal("switching peer started talk")
	}
	if got := queuedControls(p); len(got) != 1 || got[0].Type != "start_ack" || *got[0].OK {
		t.Fatalf("missing busy start_ack: %+v", got)
	}
	if _, err := s.changeRoom(p.id, func() (room, error) { return s.store.Leave(p.id), nil }); err == nil {
		t.Fatal("concurrent room change accepted")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.start(p, control{MessageID: "voice", FrameMS: 60, SampleRate: 16000})
	if s.active != p {
		t.Fatal("talk rejected after room change")
	}
}

func TestCloseRoomKeepsTalkInOtherRoom(t *testing.T) {
	s := newServer()
	a, b := testPeer(t, s, "a"), testPeer(t, s, "b")
	r, err := s.store.CreateRoom("调度组", b.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.start(a, control{MessageID: "voice", FrameMS: 60, SampleRate: 16000})
	if err := s.closeRoom(r.ID); err != nil {
		t.Fatal(err)
	}
	if s.active != a {
		t.Fatal("closing another room aborted talk")
	}
	if err := s.closeRoom("missing"); err == nil || err.Error() != "room not found" {
		t.Fatalf("unexpected missing room error: %v", err)
	}
}
