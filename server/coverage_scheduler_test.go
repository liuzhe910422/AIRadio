package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventSchedulerExecutionAndReminder(t *testing.T) {
	// runEvents currently has a fixed one-minute ticker and no cancellation hook.
	// Keep this server isolated; its only long-lived goroutine owns this test fixture.
	if testing.Short() {
		t.Skip("scheduler uses a real one-minute ticker")
	}
	s := newServer()
	p := testPeer(t, s, "scheduled")
	s.store.RegisterDevice("creator", "")
	now := time.Now()
	e, err := s.events.CreateEvent(p.id, "creator", "execute", now.Add(time.Second).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	reminder, err := s.events.CreateEvent(p.id, "creator", "remind", now.Add(10*time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	go s.runEvents()
	deadline := time.NewTimer(65 * time.Second)
	defer deadline.Stop()
	executed, reminded := false, false
	for !executed || !reminded {
		select {
		case msg := <-p.out:
			var c control
			if err := json.Unmarshal(msg.data, &c); err != nil {
				t.Fatal(err)
			}
			executed = executed || c.Type == "event_execute" && c.EventID == e.ID
			reminded = reminded || c.Type == "event_notification" && c.EventID == reminder.ID
		case <-deadline.C:
			t.Fatal("scheduler did not execute and remind")
		}
	}
	// MarkOverdue occurs after event_execute; wait for the overdue notification as the barrier.
	for {
		ended := false
		for _, v := range s.events.AllEvents() {
			if v.ID == e.ID && v.Status == "overdue" {
				ended = true
			}
		}
		if ended {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("event not marked overdue")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestDispatcherWorkAndRedirectPolicy(t *testing.T) {
	ts := mockSpeech(t)
	s := newServer()
	p := testPeer(t, s, "dispatcher")
	p.chatEnabled = true
	m := &chatMessage{ID: "dispatch", Room: "default", packets: [][]byte{samplePacket(t)}, Expires: time.Now().Add(time.Hour), recipients: []chatRecipient{{p, p.playbackToken(), "zh-CN"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.chat.run(ctx, s) }()
	s.chat.jobs <- chatJob{m, asrConfig{URL: ts.URL}}
	select {
	case <-p.out:
	case <-time.After(time.Second):
		t.Fatal("ASR dispatch missing")
	}
	cancel()
	<-done
	s.mu.Lock()
	status := m.Status
	s.mu.Unlock()
	if status != "completed" {
		t.Fatal(status)
	}
	for _, client := range []*http.Client{s.chat.client, s.chat.ttsClient} {
		r := httptest.NewRequest("GET", "https://example.test", nil)
		if client.CheckRedirect(r, nil) != http.ErrUseLastResponse {
			t.Fatal("redirect allowed")
		}
	}
}
func TestLoginSessionCapacity(t *testing.T) {
	a := coverageAPI(t, false)
	for i := 0; i < 128; i++ {
		a.sessions[fmt.Sprint(i)] = time.Now().Add(time.Hour)
	}
	delete(a.sessions, "ut")
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"admin123"}`))
	w := httptest.NewRecorder()
	a.login(w, r)
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
	for k := range a.sessions {
		a.sessions[k] = time.Now().Add(-time.Hour)
	}
	r = httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"admin","password":"admin123"}`))
	w = httptest.NewRecorder()
	a.login(w, r)
	if w.Code != 200 || len(a.sessions) != 1 {
		t.Fatal("expired sessions not pruned")
	}
}
