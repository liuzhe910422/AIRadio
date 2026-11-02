package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestImageUploadDownload(t *testing.T) {
	a := coverageAPI(t, false)
	for _, data := range [][]byte{nil, []byte("bad"), {255, 216, 255, 0}} {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		f, err := mw.CreateFormFile("file", "image.jpg")
		if err != nil {
			t.Fatal(err)
		}
		f.Write(data)
		mw.Close()
		r := httptest.NewRequest("POST", "/api/intercom/images", &b)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		a.image(w, r)
		if len(data) < 3 || data[0] != 255 {
			if w.Code != 400 {
				t.Fatal(w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var result map[string]string
		json.Unmarshal(w.Body.Bytes(), &result)
		out := httptest.NewRecorder()
		a.image(out, httptest.NewRequest("GET", result["image_url"], nil))
		if !bytes.Equal(out.Body.Bytes(), data) || out.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatal("download mismatch")
		}
	}
	r := httptest.NewRequest("POST", "/api/intercom/images", nil)
	r.ContentLength = 512*1024 + 1
	w := httptest.NewRecorder()
	a.image(w, r)
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("other", "x")
	mw.Close()
	r = httptest.NewRequest("POST", "/api/intercom/images", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	a.image(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestAPIBadInputsAndUsers(t *testing.T) {
	a := coverageAPI(t, false)
	callAPI(t, a, "GET", "/api/devices/x", nil, 405)
	callAPI(t, a, "DELETE", "/api/devices/bad!", nil, 400)
	callAPI(t, a, "DELETE", "/api/devices/missing", nil, 200)
	callAPI(t, a, "POST", "/api/devices", map[string]string{"device_id": "simdel"}, 200)
	callAPI(t, a, "DELETE", "/api/devices/simdel", nil, 200)
	callAPI(t, a, "POST", "/api/devices", "{", 400)
	callAPI(t, a, "POST", "/api/devices", map[string]string{"device_id": "bad!"}, 400)
	callAPI(t, a, "PUT", "/api/devices", nil, 405)
	callAPI(t, a, "GET", "/api/users", nil, 200)
	callAPI(t, a, "PATCH", "/api/users", nil, 405)
	callAPI(t, a, "POST", "/api/users", "{", 400)
	callAPI(t, a, "POST", "/api/users", map[string]string{"username": "ut-user", "password": "abcdefgh"}, 200)
	callAPI(t, a, "POST", "/api/users", map[string]string{"username": "ut-user", "password": "abcdefgh"}, 400)
	callAPI(t, a, "PUT", "/api/users/missing/password", map[string]string{"password": "abcdefgh"}, 400)
	callAPI(t, a, "PUT", "/api/users/ut-user/password", map[string]string{"password": "12345678"}, 200)
	if len(a.sessions) != 0 || !a.s.checkAdmin("ut-user", "12345678") {
		t.Fatal("reset did not invalidate sessions or change password")
	}
}
func TestAlertValidation(t *testing.T) {
	a := coverageAPI(t, false)
	a.s.store.RegisterDevice("sensor", "")
	high, low := 40.0, 20.0
	base := alertRule{ID: "temperature", Name: "rule", Type: "temperature", DeviceID: "sensor", RoomID: "default", Interval: 10, TempAbove: &high}
	cases := []func(*alertRule){
		func(r *alertRule) { r.Name = "" }, func(r *alertRule) { r.DeviceID = "missing" }, func(r *alertRule) { r.RoomID = "missing" },
		func(r *alertRule) { r.Type = "vision"; r.Prompt = "" }, func(r *alertRule) { r.Type = "vision"; r.Prompt = strings.Repeat("x", 12001) },
		func(r *alertRule) { r.TempAbove = nil }, func(r *alertRule) { r.TempBelow = &high }, func(r *alertRule) { r.Type = "humidity"; r.HumAbove = &low; r.HumBelow = &high },
		func(r *alertRule) { r.Name = strings.Repeat("字", 81) },
	}
	for _, mutate := range cases {
		r := base
		mutate(&r)
		callAPI(t, a, "PUT", "/api/alerts", r, 400)
	}
	for _, path := range []string{"/api/rooms", "/api/rooms/default", "/api/devices", "/api/devices/x", "/api/events", "/api/events/x/cancel", "/api/agent/skills", "/api/agent/runs", "/api/alerts", "/api/alerts/x", "/api/asr/config", "/api/asr/test", "/api/tts/config", "/api/tts/test", "/api/hosting/config", "/api/hosting/test", "/api/intercom/auth/config", "/api/users"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		a.handler(w, r)
		if w.Code != 401 {
			t.Fatalf("%s auth=%d", path, w.Code)
		}
	}
}
func TestSpeechTestAPIs(t *testing.T) {
	ts := mockSpeech(t)
	a := coverageAPI(t, false)
	a.s.chat.config = asrConfig{URL: ts.URL, Key: "ut"}
	a.s.chat.hostingConfig = hostingConfig{URL: ts.URL + "/hosting", Key: "ut"}
	a.s.chat.ttsConfig = ttsConfig{URL: ts.URL + "/tts", Key: "ut"}
	callAPI(t, a, "POST", "/api/asr/test", string(sampleTTSWAV()), 200)
	callAPI(t, a, "POST", "/api/hosting/test", map[string]string{"text": "hello"}, 200)
	callAPI(t, a, "POST", "/api/tts/test", map[string]string{"text": "hello"}, 200)
	a.s.chat.config.URL = ":bad"
	a.s.chat.hostingConfig.URL = ":bad"
	a.s.chat.ttsConfig.URL = ":bad"
	callAPI(t, a, "POST", "/api/asr/test", string(sampleTTSWAV()), 502)
	callAPI(t, a, "POST", "/api/hosting/test", map[string]string{"text": "hello"}, 502)
	callAPI(t, a, "POST", "/api/tts/test", map[string]string{"text": "hello"}, 502)
	a.s.chat.config.Key = ""
	callAPI(t, a, "POST", "/api/asr/test", string(sampleTTSWAV()), 400)
}
func TestRecordingFailureCleanup(t *testing.T) {
	s := newServer()
	s.recordDir = t.TempDir()
	p := &peer{id: "record"}
	if err := s.beginRecording(p); err != nil {
		t.Fatal(err)
	}
	name := p.recording.file.Name()
	if err := p.recording.finish(); err == nil {
		t.Fatal("empty recording accepted")
	}
	if err := p.recording.appendOpus([]byte{255}); err == nil {
		t.Fatal("invalid opus accepted")
	}
	p.recording.pcmBytes = 960000
	if err := p.recording.appendOpus(samplePacket(t)); err == nil {
		t.Fatal("oversize recording accepted")
	}
	s.discardRecording(p)
	if p.recording != nil {
		t.Fatal("recording retained")
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("temporary recording leaked")
	}
	s.discardRecording(p)
	(&wavRecording{}).discard()
	s.recordDir = name + "/nonexistent"
	if err := s.beginRecording(p); err == nil {
		t.Fatal("invalid directory accepted")
	}
	s.recordDir = t.TempDir()
	if err := s.beginRecording(p); err != nil {
		t.Fatal(err)
	}
	p.recording.file.Close()
	if err := p.recording.appendOpus(samplePacket(t)); err == nil {
		t.Fatal("closed file write accepted")
	}
	p.recording.pcmBytes = 2
	if err := p.recording.finish(); err == nil {
		t.Fatal("closed file finish accepted")
	}
	s.discardRecording(p)
}
func TestEventNotificationsAndChatRetention(t *testing.T) {
	s := newServer()
	p := testPeer(t, s, "notify")
	p.chatEnabled = true
	s.chat.ttsConfig.Enabled = true
	e := event{ID: "event", CreatorDeviceID: "creator", Content: "notice", Status: "pending"}
	s.notifyEvent(e, p.id, "created", "notice")
	if len(s.chat.messages) != 1 || len(s.chat.ttsJobs) != 1 {
		t.Fatal("notification audio not queued")
	}
	s.syncEvents(p)
	if s.count() != 1 {
		t.Fatal("count")
	}
	s.chat.config.Enabled = true
	for i := 0; i < 22; i++ {
		p.chatPackets = [][]byte{{1}}
		p.chatRecipients = []chatRecipient{{p, p.playbackToken(), "zh-CN"}}
		p.chatRoom = "default"
		s.completeChat(p)
	}
	if len(s.chat.messages) > 21 {
		t.Fatal("recipient history limit ignored")
	}
	if s.chat.messages[len(s.chat.messages)-1].Status != "failed" {
		t.Fatal("full queue not marked failed")
	}
	if err := s.store.ReturnDefault(p.id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.chat.run(ctx, s)
}
