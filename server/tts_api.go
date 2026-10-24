package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func (s *server) loadTTSConfig() {
	db, ok := s.store.(*sqliteStore)
	if !ok {
		return
	}
	b, err := db.exec("SELECT value FROM service_config WHERE name='tts'")
	if err != nil {
		return
	}
	var rows []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(b, &rows) == nil && len(rows) > 0 {
		cfg := defaultTTSConfig()
		if json.Unmarshal([]byte(rows[0].Value), &cfg) == nil {
			s.chat.ttsConfig = cfg
		}
	}
}
func (a *apiServer) ttsConfig(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	c := a.s.chat
	switch r.Method {
	case "GET":
		c.mu.Lock()
		cfg := c.ttsConfig
		c.mu.Unlock()
		a.json(w, map[string]any{"enabled": cfg.Enabled, "url": cfg.URL, "model": cfg.Model, "voice": cfg.Voice, "key_configured": cfg.Key != ""})
	case "PUT":
		var x struct {
			ttsConfig
			ClearKey bool `json:"clear_key"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&x) != nil {
			http.Error(w, "invalid config", 400)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if x.Key == "" {
			x.Key = c.ttsConfig.Key
		}
		if x.ClearKey {
			x.Key = ""
		}
		if x.Model == "" {
			x.Model = defaultTTSConfig().Model
		}
		if x.Voice == "" {
			x.Voice = defaultTTSConfig().Voice
		}
		if err := x.validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if db, ok := a.s.store.(*sqliteStore); ok {
			b, _ := json.Marshal(x.ttsConfig)
			if _, err := db.exec("INSERT INTO service_config(name,value) VALUES('tts'," + sqliteString(string(b)) + ") ON CONFLICT(name) DO UPDATE SET value=excluded.value"); err != nil {
				http.Error(w, "cannot save TTS config", 500)
				return
			}
		}
		c.ttsConfig = x.ttsConfig
		a.json(w, map[string]bool{"ok": true})
	default:
		w.WriteHeader(405)
	}
}
func (a *apiServer) ttsTest(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var x struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&x) != nil || len([]rune(x.Text)) == 0 || len([]rune(x.Text)) > 160 {
		http.Error(w, "text must contain 1-160 characters", 400)
		return
	}
	c := a.s.chat
	c.mu.Lock()
	cfg := c.ttsConfig
	c.mu.Unlock()
	if cfg.Key == "" {
		http.Error(w, "configure TTS key first", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	wav, err := synthesize(ctx, c.ttsClient, cfg, x.Text)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	packets, duration, err := ttsOpus(wav)
	if err != nil {
		http.Error(w, "TTS audio format unsupported", 502)
		return
	}
	a.json(w, map[string]any{"ok": true, "duration_ms": duration, "opus_packets": len(packets)})
}
