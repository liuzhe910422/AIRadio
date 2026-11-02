package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type utTransport func(*http.Request) (*http.Response, error)

func (f utTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestServiceTransportFailures(t *testing.T) {
	for _, url := range []string{":bad", "https://example.test"} {
		client := &http.Client{Transport: utTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
		if _, err := recognize(context.Background(), client, asrConfig{URL: url}, nil); err == nil {
			t.Fatal("ASR accepted failure")
		}
		if _, err := translateText(context.Background(), client, hostingConfig{URL: url}, "x", "en"); err == nil {
			t.Fatal("translation accepted failure")
		}
		if _, err := decideHosting(context.Background(), client, hostingConfig{URL: url}, "x"); err == nil {
			t.Fatal("hosting accepted failure")
		}
		if _, err := synthesize(context.Background(), client, ttsConfig{URL: url}, "x"); err == nil {
			t.Fatal("TTS accepted failure")
		}
	}
	for _, body := range []string{`{`, `{"choices":[]}`, `{"choices":[{"message":{"content":" "}}]}`} {
		client := &http.Client{Transport: utTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		if _, err := translateText(context.Background(), client, hostingConfig{URL: "https://x.test"}, "x", "en"); err == nil {
			t.Fatal("invalid translation accepted")
		}
	}
	for _, code := range []int{401, 500} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		if _, err := translateText(context.Background(), ts.Client(), hostingConfig{URL: ts.URL}, "x", "en"); err == nil {
			t.Fatal("translation status")
		}
		if _, err := decideHosting(context.Background(), ts.Client(), hostingConfig{URL: ts.URL}, "x"); err == nil {
			t.Fatal("hosting status")
		}
		if _, err := synthesize(context.Background(), ts.Client(), ttsConfig{URL: ts.URL}, "x"); err == nil {
			t.Fatal("TTS status")
		}
		ts.Close()
	}
	for _, mode := range []string{"json", "url", "download", "oversize"} {
		var ts *httptest.Server
		ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/audio" {
				if mode == "download" {
					w.WriteHeader(500)
				} else {
					w.Write(make([]byte, 1440045))
				}
				return
			}
			if mode == "json" {
				w.Write([]byte("{"))
				return
			}
			url := ts.URL + "/audio"
			if mode == "url" {
				url = "file:///tmp/audio"
			}
			json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"audio": map[string]string{"url": url}}})
		}))
		if _, err := synthesize(context.Background(), ts.Client(), ttsConfig{URL: ts.URL}, "x"); err == nil {
			t.Fatalf("TTS accepted %s", mode)
		}
		ts.Close()
	}
}
func TestVisionAlertEvaluation(t *testing.T) {
	s := newServer()
	p := testPeer(t, s, "vision-device")
	s.images["ut-image"] = []byte{255, 216, 255}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"alert\":true,\"message\":\"fire\"}"}}]}`))
	}))
	defer ts.Close()
	s.chat.hostingConfig = hostingConfig{URL: ts.URL, Key: "ut"}
	r := alertRule{ID: "vision-ut", Type: "vision", Enabled: true, DeviceID: p.id, RoomID: "default"}
	s.alerts.rules[r.ID] = r
	// saveImage has a fixed production directory; remove only files created by this test.
	dir := "recordings/pic"
	before, _ := filepath.Glob(filepath.Join(dir, "*"))
	existing := map[string]bool{}
	for _, p := range before {
		existing[p] = true
	}
	t.Cleanup(func() {
		after, _ := filepath.Glob(filepath.Join(dir, "*vision-ut*"))
		for _, p := range after {
			if !existing[p] {
				os.Remove(p)
			}
		}
	})
	captureReply(t, s, p, r, control{ImageURL: "/api/intercom/images/ut-image"})
	if len(s.alerts.records) != 1 || !s.alerts.active[r.ID] {
		t.Fatal("vision hit not recorded")
	}
	queuedControls(p)
	captureReply(t, s, p, r, control{})
	queuedControls(p)
	captureReply(t, s, p, r, control{ImageURL: ":bad"})
}
func TestRealTimeRecordingAndBackpressure(t *testing.T) {
	s := newServer()
	s.saveWAV = true
	s.recordDir = t.TempDir()
	p := testPeer(t, s, "speaker")
	q := testPeer(t, s, "listener")
	s.active = &peer{automatic: true, sending: "synthetic"}
	s.start(p, control{MessageID: "real", FrameMS: 60, SampleRate: 16000})
	if s.active != p {
		t.Fatal("human did not interrupt automation")
	}
	if err := s.frame(p, samplePacket(t)); err != nil {
		t.Fatal(err)
	}
	if err := s.end(p, control{MessageID: "real", FrameCount: 1}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(s.recordDir, "*.wav"))
	if len(files) != 1 {
		t.Fatal("recording not finished")
	}
	queuedControls(p)
	queuedControls(q)
	s.start(p, control{})
	if len(queuedControls(p)) != 1 {
		t.Fatal("invalid start not rejected")
	}
	for len(q.out) < cap(q.out) {
		q.out <- outgoing{}
	}
	s.start(p, control{MessageID: "full", FrameMS: 60, SampleRate: 16000})
	s.abort(p)
	s.store.RegisterDevice("physical", "", "physical")
	a := &apiServer{s: s, sessions: map[string]time.Time{"ut": time.Now().Add(time.Hour)}}
	callAPI(t, a, "DELETE", "/api/devices/physical", nil, 400)
}
func TestConfigLengthLimitsAndDefaults(t *testing.T) {
	for _, url := range []string{"https://u:p@x.test/chat/completions", "https://x.test/chat/completions?q=x", "https://x.test/chat/completions#frag", "%"} {
		if (asrConfig{URL: url}).validate() == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
	if (asrConfig{URL: "https://x.test/chat/completions", Model: strings.Repeat("x", 129)}).validate() == nil {
		t.Fatal("long ASR model")
	}
	if (hostingConfig{URL: "https://x.test/chat/completions", Prompt: strings.Repeat("x", 12001)}).validate() == nil {
		t.Fatal("long prompt")
	}
	if (ttsConfig{URL: "https://x.test/services/audio/tts/SpeechSynthesizer", Voice: strings.Repeat("x", 129)}).validate() == nil {
		t.Fatal("long voice")
	}
	a := coverageAPI(t, true)
	s := a.s
	s.store.RegisterDevice("ut", "")
	if err := s.store.ReturnDefault("ut"); err != nil {
		t.Fatal(err)
	}
	for _, load := range []func(){s.loadASRConfig, s.loadHostingConfig, s.loadTTSConfig, s.loadIntercomAuthConfig} {
		load()
	}
	s.store = newMemoryStore()
	for _, load := range []func(){s.loadASRConfig, s.loadHostingConfig, s.loadTTSConfig, s.loadIntercomAuthConfig, s.agent.load, s.alerts.load} {
		load()
	}
	h := httpHandler(s)
	for _, path := range []string{"/", "/index.html", "/missing"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		want := 200
		if path == "/index.html" {
			want = 301
		}
		if path == "/missing" {
			want = 404
		}
		if w.Code != want {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.handle(w, httptest.NewRequest("GET", "/wrong", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	for i := 0; i < cap(s.slots); i++ {
		s.slots <- struct{}{}
	}
	w = httptest.NewRecorder()
	s.handle(w, httptest.NewRequest("GET", "/ws", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
