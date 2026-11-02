package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRoomAPIActionsAndEventAudioQueue(t *testing.T) {
	a := coverageAPI(t, false)
	s := a.s
	p := testPeer(t, s, "owner")
	q := testPeer(t, s, "invitee")
	r, err := s.store.CreateRoom("room", p.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	callAPI(t, a, "PATCH", "/api/rooms/"+r.ID, "{", 400)
	callAPI(t, a, "PATCH", "/api/rooms/missing", map[string]string{"name": "x"}, 400)
	callAPI(t, a, "PATCH", "/api/rooms/"+r.ID, map[string]any{"name": "changed", "invite_device_ids": []string{q.id}}, 200)
	callAPI(t, a, "GET", "/api/rooms/"+r.ID+"/close", nil, 405)
	callAPI(t, a, "POST", "/api/rooms/missing/close", nil, 400)
	callAPI(t, a, "POST", "/api/rooms/"+r.ID+"/close", nil, 200)
	callAPI(t, a, "GET", "/api/rooms/missing/other", nil, 404)
	s.events.CreateEvent(p.id, q.id, "sync", time.Now().Add(time.Hour).Format(time.RFC3339))
	queuedControls(p)
	s.syncEvents(p)
	if len(queuedControls(p)) != 1 {
		t.Fatal("pending event not synced")
	}
	p.chatEnabled = true
	s.chat.ttsConfig.Enabled = true
	for len(s.chat.ttsJobs) < cap(s.chat.ttsJobs) {
		s.chat.ttsJobs <- ttsJob{}
	}
	s.notifyEvent(event{ID: "full", CreatorDeviceID: q.id}, p.id, "created", "notice")
	s.alerts.notify(alertRule{RoomID: "default", TTS: true}, "")
	if len(s.chat.messages) < 2 {
		t.Fatal("queue backpressure lost text notifications")
	}
}
func TestASRDispatcherTranslationFailure(t *testing.T) {
	s := newServer()
	p := testPeer(t, s, "dispatch-fail")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.chat.translationJobs <- translationJob{message: &chatMessage{Text: "hi"}, recipient: chatRecipient{peer: p}, config: hostingConfig{URL: ":invalid"}}
	go func() { defer close(done); s.chat.run(ctx, s) }()
	// A second ASR job provides a synchronized completion signal after the translation dispatch.
	p.chatEnabled = true
	m := &chatMessage{ID: "failed", Room: "default", recipients: []chatRecipient{{p, p.playbackToken(), "zh-CN"}}}
	s.chat.jobs <- chatJob{message: m}
	select {
	case <-p.out:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not process failure")
	}
	cancel()
	<-done
}
func TestTTSUnsupportedAudioAPI(t *testing.T) {
	a := coverageAPI(t, false)
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio" {
			w.Write([]byte("bad audio"))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"audio": map[string]string{"url": ts.URL + "/audio"}}})
	}))
	defer ts.Close()
	a.s.chat.ttsConfig = ttsConfig{URL: ts.URL, Key: "ut"}
	callAPI(t, a, "POST", "/api/tts/test", map[string]string{"text": "test"}, 502)
}
