package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRecognizeInlineWAV(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing auth")
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Audio struct {
						Data string `json:"data"`
					} `json:"input_audio"`
				} `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != defaultASRModel || req.Messages[0].Content[0].Audio.Data != "data:audio/wav;base64,"+base64.StdEncoding.EncodeToString([]byte("wav")) {
			t.Error("bad inline audio request")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"喂喂。"}}]}`))
	}))
	defer upstream.Close()
	text, err := recognize(context.Background(), upstream.Client(), asrConfig{URL: upstream.URL, Key: "test-secret", Model: defaultASRModel}, []byte("wav"))
	if err != nil || text != "喂喂。" {
		t.Fatalf("%q %v", text, err)
	}
}
func TestChatPublicPlaybackAndConfigMask(t *testing.T) {
	s := newServer()
	p := &peer{id: "one"}
	s.resetChatView(p)
	s.peers[p.id] = p
	s.store.RegisterDevice(p.id, "")
	token := p.playbackToken()
	s.chat.messages = []*chatMessage{{ID: "message", Room: "default", Expires: time.Now().Add(time.Minute), recipients: []chatRecipient{{p, token}}, packets: [][]byte{{1, 2}}}}
	api := &apiServer{s: s, sessions: map[string]bool{"test": true}}
	request := httptest.NewRequest("GET", "/api/intercom/messages/message/audio", nil)
	w := httptest.NewRecorder()
	api.chatAudio(w, request)
	if w.Code != 200 || string(w.Body.Bytes()[:4]) != "AIR1" {
		t.Fatalf("playback %d", w.Code)
	}
	s.resetChatView(p)
	w = httptest.NewRecorder()
	api.chatAudio(w, request)
	if w.Code != 200 {
		t.Fatal("public playback should not require a session")
	}
	s.chat.messages[0].Expires = time.Now().Add(-time.Second)
	w = httptest.NewRecorder()
	api.chatAudio(w, request)
	if w.Code != 410 {
		t.Fatal("expired audio should return 410")
	}
	s.chat.config.Key = "do-not-expose"
	w = httptest.NewRecorder()
	request = httptest.NewRequest("GET", "/api/asr/config", nil)
	request.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "test"})
	api.asrConfig(w, request)
	var cfg map[string]any
	json.Unmarshal(w.Body.Bytes(), &cfg)
	if _, ok := cfg["api_key"]; ok {
		t.Fatal("key exposed")
	}
	if cfg["key_configured"] != true {
		t.Fatal("key status missing")
	}
}

func TestAliyunASRLive(t *testing.T) {
	if os.Getenv("AIRADIO_ASR_LIVE_TEST") != "1" {
		t.Skip("explicit live test only")
	}
	credentials, err := os.ReadFile("../count.tx")
	if err != nil {
		t.Fatal(err)
	}
	key := regexp.MustCompile(`sk-[A-Za-z0-9_-]+`).FindString(string(credentials))
	host := regexp.MustCompile(`[A-Za-z0-9.-]+\.cn-beijing\.maas\.aliyuncs\.com`).FindString(string(credentials))
	if key == "" || host == "" {
		t.Fatal("credentials missing")
	}
	wav, err := os.ReadFile("recordings/002-20261024T071238Z-3158505286.wav")
	if err != nil {
		t.Fatal(err)
	}
	c := newChatService()
	text, err := recognize(context.Background(), c.client, asrConfig{Key: key, URL: "https://" + host + "/compatible-mode/v1/chat/completions", Model: defaultASRModel}, wav)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "喂") {
		t.Fatalf("unexpected transcript: %q", text)
	}
	t.Logf("ASR transcript: %s", text)
}
