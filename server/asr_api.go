package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func (s *server) loadASRConfig() {
	db, ok := s.store.(*sqliteStore)
	if !ok {
		return
	}
	if _, err := db.exec("CREATE TABLE IF NOT EXISTS service_config(name TEXT PRIMARY KEY,value TEXT)"); err != nil {
		log.Print("ASR config storage unavailable")
		return
	}
	b, err := db.exec("SELECT value FROM service_config WHERE name='asr'")
	if err != nil {
		return
	}
	var rows []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(b, &rows) == nil && len(rows) > 0 {
		var cfg asrConfig
		if json.Unmarshal([]byte(rows[0].Value), &cfg) == nil {
			s.chat.config = cfg
		}
	}
}
func (a *apiServer) asrConfig(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	c := a.s.chat
	switch r.Method {
	case "GET":
		c.mu.Lock()
		cfg := c.config
		c.mu.Unlock()
		a.json(w, map[string]any{"enabled": cfg.Enabled, "url": cfg.URL, "model": cfg.Model, "key_configured": cfg.Key != ""})
	case "PUT":
		var x struct {
			asrConfig
			ClearKey bool `json:"clear_key"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&x) != nil {
			http.Error(w, "invalid config", 400)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if x.Key == "" {
			x.Key = c.config.Key
		}
		if x.ClearKey {
			x.Key = ""
		}
		if x.Model == "" {
			x.Model = defaultASRModel
		}
		if err := x.validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if db, ok := a.s.store.(*sqliteStore); ok {
			b, _ := json.Marshal(x.asrConfig)
			if _, err := db.exec("INSERT INTO service_config(name,value) VALUES('asr'," + sqliteString(string(b)) + ") ON CONFLICT(name) DO UPDATE SET value=excluded.value"); err != nil {
				http.Error(w, "cannot save ASR config", 500)
				return
			}
		}
		c.config = x.asrConfig
		a.json(w, map[string]bool{"ok": true})
	default:
		w.WriteHeader(405)
	}
}
func (a *apiServer) asrTest(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 960044))
	if err != nil || len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		http.Error(w, "upload a WAV file (maximum 30 seconds)", 400)
		return
	}
	c := a.s.chat
	c.mu.Lock()
	cfg := c.config
	c.mu.Unlock()
	if cfg.Key == "" {
		http.Error(w, "configure API key first", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	text, err := recognize(ctx, c.client, cfg, b)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	a.json(w, map[string]string{"text": text})
}
func (a *apiServer) chatAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/intercom/messages/"), "/audio")
	c := a.s.chat
	c.mu.Lock()
	var m *chatMessage
	for _, v := range c.messages {
		if v.ID == id {
			m = v
			break
		}
	}
	if m == nil || time.Now().After(m.Expires) {
		c.mu.Unlock()
		http.Error(w, "audio expired", 410)
		return
	}
	c.mu.Unlock()
	// AIR1: 4-byte magic, uint32 LE rate, uint16 LE channels/frame_ms,
	// uint32 LE packet count, then uint32 LE length + one raw Opus packet.
	var b bytes.Buffer
	b.WriteString("AIR1")
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(60))
	binary.Write(&b, binary.LittleEndian, uint32(len(m.packets)))
	for _, p := range m.packets {
		binary.Write(&b, binary.LittleEndian, uint32(len(p)))
		b.Write(p)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b.Bytes())
}
