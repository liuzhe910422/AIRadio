package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func (s *server) loadHostingConfig() {
	db, ok := s.store.(*sqliteStore)
	if !ok {
		return
	}
	b, err := db.exec("SELECT value FROM service_config WHERE name='hosting'")
	if err != nil {
		return
	}
	var rows []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(b, &rows) == nil && len(rows) > 0 {
		cfg := defaultHostingConfig()
		if json.Unmarshal([]byte(rows[0].Value), &cfg) == nil {
			s.chat.hostingConfig = cfg
		}
	}
}
func (a *apiServer) hostingConfig(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	c := a.s.chat
	switch r.Method {
	case "GET":
		c.mu.Lock()
		cfg := c.hostingConfig
		c.mu.Unlock()
		a.json(w, map[string]any{"enabled": cfg.Enabled, "url": cfg.URL, "model": cfg.Model, "prompt": cfg.Prompt, "key_configured": cfg.Key != ""})
	case "PUT":
		var x struct {
			hostingConfig
			ClearKey bool `json:"clear_key"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&x) != nil {
			http.Error(w, "invalid config", 400)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if x.Key == "" {
			x.Key = c.hostingConfig.Key
		}
		if x.ClearKey {
			x.Key = ""
		}
		if x.Model == "" {
			x.Model = defaultHostingConfig().Model
		}
		if x.Prompt == "" {
			x.Prompt = defaultHostingConfig().Prompt
		}
		if err := x.validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if db, ok := a.s.store.(*sqliteStore); ok {
			b, _ := json.Marshal(x.hostingConfig)
			if _, err := db.exec("INSERT INTO service_config(name,value) VALUES('hosting'," + sqliteString(string(b)) + ") ON CONFLICT(name) DO UPDATE SET value=excluded.value"); err != nil {
				http.Error(w, "cannot save LLM config", 500)
				return
			}
		}
		c.hostingConfig = x.hostingConfig
		a.json(w, map[string]bool{"ok": true})
	default:
		w.WriteHeader(405)
	}
}

func (a *apiServer) hostingTest(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var x struct {
		DeviceID string `json:"device_id"`
		Text     string `json:"text"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&x) != nil || len(x.Text) == 0 || len(x.Text) > 2048 {
		http.Error(w, "invalid test input", 400)
		return
	}
	c := a.s.chat
	c.mu.Lock()
	cfg := c.hostingConfig
	c.mu.Unlock()
	if cfg.Key == "" {
		http.Error(w, "configure LLM key first", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	d, e := decideHosting(ctx, c.client, cfg, map[string]string{"device_id": x.DeviceID, "message": x.Text})
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	a.json(w, d)
}
