package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hraban/opus"
)

func TestServiceConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
		asr  asrConfig
		tts  ttsConfig
		host hostingConfig
	}{
		{"valid", true, asrConfig{Enabled: true, URL: "https://x.test/v1/chat/completions", Key: "k", Model: "m"}, ttsConfig{Enabled: true, URL: "https://x.test/services/audio/tts/SpeechSynthesizer", Key: "k", Model: "m", Voice: "v"}, hostingConfig{Enabled: true, URL: "https://x.test/v1/chat/completions", Key: "k", Model: "m", Prompt: "p"}},
		{"bad urls", false, asrConfig{URL: "http://x.test/chat/completions"}, ttsConfig{URL: "https://x.test/tts"}, hostingConfig{URL: "https://x.test/chat"}},
		{"enabled missing fields", false, asrConfig{Enabled: true, URL: "https://x.test/chat/completions"}, ttsConfig{Enabled: true, URL: "https://x.test/services/audio/tts/SpeechSynthesizer"}, hostingConfig{Enabled: true, URL: "https://x.test/chat/completions"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.asr.validate() == nil && tt.tts.validate() == nil && tt.host.validate() == nil
			if got != tt.ok {
				t.Fatalf("valid=%v want %v", got, tt.ok)
			}
		})
	}
}

func TestHTTPServiceErrorAndSuccessPaths(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"bad status", ``, 500}, {"bad json", `{`, 200}, {"empty", `{"choices":[]}`, 200},
	} {
		t.Run("asr-"+tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			if _, err := recognize(context.Background(), ts.Client(), asrConfig{URL: ts.URL, Model: "m"}, []byte("x")); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" translated "}}]}`))
	}))
	defer ts.Close()
	got, err := translateText(context.Background(), ts.Client(), hostingConfig{URL: ts.URL, Model: "m"}, "hi", "en")
	if err != nil || got != "translated" {
		t.Fatalf("translate=%q err=%v", got, err)
	}
}

func TestDecideHostingValidation(t *testing.T) {
	for _, body := range []string{`{"choices":[]}`, `{"choices":[{"message":{"content":"{"}}]}`, `{"choices":[{"message":{"content":"{\"should_reply\":true,\"text\":\"\"}"}}]}`} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := decideHosting(context.Background(), ts.Client(), hostingConfig{URL: ts.URL, Model: "m", Prompt: "p"}, "hello")
		ts.Close()
		if err == nil {
			t.Fatalf("expected error for %s", body)
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"should_reply\":true,\"text\":\"ok\"}"}}]}`))
	}))
	d, err := decideHosting(context.Background(), ts.Client(), hostingConfig{URL: ts.URL, Model: "m", Prompt: "p"}, "hello")
	ts.Close()
	if err != nil || !d.Reply || d.Text != "ok" {
		t.Fatalf("decision=%+v err=%v", d, err)
	}
}

func TestSynthesizeAndWAVRecording(t *testing.T) {
	enc, err := opus.NewEncoder(16000, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, 960)
	packet := make([]byte, 400)
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		t.Fatal(err)
	}
	packet = packet[:n]
	s := newServer()
	s.recordDir = t.TempDir()
	p := &peer{id: "wav"}
	if err := s.beginRecording(p); err != nil {
		t.Fatal(err)
	}
	if err := p.recording.appendOpus(packet); err != nil {
		t.Fatal(err)
	}
	path := p.recording.path
	if err := p.recording.finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || len(b) < 44 || string(b[:4]) != "RIFF" {
		t.Fatalf("wav invalid: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("audio"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"audio": map[string]string{"url": r.URL.String()}}})
	}))
	defer ts.Close()
	if _, err := synthesize(context.Background(), ts.Client(), ttsConfig{URL: ts.URL, Model: "m", Voice: "v"}, strings.Repeat("x", 2)); err == nil {
		t.Fatal("relative audio URL accepted")
	}
}
