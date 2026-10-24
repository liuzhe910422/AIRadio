package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

func TestHostingDecision(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing key")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"should_reply\":true,\"text\":\"收到。\"}"}}]}`))
	}))
	defer upstream.Close()
	d, e := decideHosting(context.Background(), upstream.Client(), hostingConfig{URL: upstream.URL, Key: "secret", Model: "qwen3.8-flash"}, map[string]string{"message": "002请回答"})
	if e != nil || !d.Reply || d.Text != "收到。" {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestHostingScopeAndCancellation(t *testing.T) {
	s := newServer()
	s.chat.hostingConfig.Enabled = true
	s.chat.ttsConfig.Enabled = true
	a := &peer{}
	b := &peer{}
	s.register(a, "a")
	s.register(b, "b")
	b.hosted.Store(true)
	m := &chatMessage{Room: "default", Source: "a", Text: "b请回答", Status: "completed"}
	s.mu.Lock()
	s.queueHosting(m)
	s.mu.Unlock()
	if len(s.chat.hostingJobs) != 1 {
		t.Fatal("missing hosted job")
	}
	j := <-s.chat.hostingJobs
	if !s.validHosting(j) {
		t.Fatal("job unexpectedly invalid")
	}
	b.hosted.Store(false)
	b.hostingEpoch.Add(1)
	if s.validHosting(j) {
		t.Fatal("exit did not cancel")
	}
	b.hosted.Store(true)
	b.hostingBusy = false
	m.Automated = true
	s.mu.Lock()
	s.queueHosting(m)
	s.mu.Unlock()
	if len(s.chat.hostingJobs) != 0 {
		t.Fatal("automated feedback loop")
	}
}
func TestAliyunHostingLive(t *testing.T) {
	if os.Getenv("AIRADIO_HOSTING_LIVE_TEST") != "1" {
		t.Skip("explicit live only")
	}
	b, e := os.ReadFile("../count.tx")
	if e != nil {
		t.Fatal(e)
	}
	cfg := defaultHostingConfig()
	cfg.Key = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`).FindString(string(b))
	host := regexp.MustCompile(`[A-Za-z0-9.-]+\.cn-beijing\.maas\.aliyuncs\.com`).FindString(string(b))
	if cfg.Key == "" || host == "" {
		t.Fatal("missing credentials")
	}
	cfg.URL = "https://" + host + "/compatible-mode/v1/chat/completions"
	d, e := decideHosting(context.Background(), newChatService().client, cfg, map[string]string{"device_id": "002", "message": "002，听到请回答。"})
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("decision: %+v", d)
}
