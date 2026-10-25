package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

type intercomAuthConfig struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key,omitempty"`
}

func (s *server) loadIntercomAuthConfig() {
	db, ok := s.store.(*sqliteStore)
	if !ok {
		return
	}
	b, err := db.exec("SELECT value FROM service_config WHERE name='intercom_auth'")
	if err != nil {
		return
	}
	var rows []struct {
		Value string `json:"value"`
	}
	var cfg intercomAuthConfig
	if json.Unmarshal(b, &rows) == nil && len(rows) > 0 && json.Unmarshal([]byte(rows[0].Value), &cfg) == nil {
		s.authMu.Lock()
		s.authEnabled, s.authKey = cfg.Enabled, cfg.APIKey
		s.authMu.Unlock()
	}
}

func (a *apiServer) intercomAuthConfig(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	s := a.s
	s.authMu.RLock()
	cfg := intercomAuthConfig{Enabled: s.authEnabled, APIKey: s.authKey}
	s.authMu.RUnlock()
	switch r.Method {
	case http.MethodGet:
		// Development mode intentionally returns the key for local debugging.
		a.json(w, cfg)
	case http.MethodPut:
		var next intercomAuthConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&next) != nil || len(next.APIKey) > 256 {
			http.Error(w, "invalid intercom auth config", http.StatusBadRequest)
			return
		}
		if next.Enabled && strings.TrimSpace(next.APIKey) == "" {
			http.Error(w, "API key required when authentication is enabled", http.StatusBadRequest)
			return
		}
		s.authMu.Lock()
		s.authEnabled, s.authKey = next.Enabled, next.APIKey
		s.authMu.Unlock()
		if db, ok := s.store.(*sqliteStore); ok {
			b, _ := json.Marshal(next)
			if _, err := db.exec("INSERT INTO service_config(name,value) VALUES('intercom_auth'," + sqliteString(string(b)) + ") ON CONFLICT(name) DO UPDATE SET value=excluded.value"); err != nil {
				http.Error(w, "cannot save intercom auth config", http.StatusInternalServerError)
				return
			}
		}
		log.Printf("intercom auth config updated enabled=%t api_key=%q", next.Enabled, next.APIKey)
		a.json(w, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
