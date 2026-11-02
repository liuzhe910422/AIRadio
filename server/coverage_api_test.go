package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func coverageAPI(t *testing.T, sqlite bool) *apiServer {
	t.Helper()
	s := newServer()
	if sqlite {
		s.store = newSQLiteTestStore(t)
		s.loadASRConfig()
	}
	return &apiServer{s: s, sessions: map[string]time.Time{"ut": time.Now().Add(time.Hour)}}
}
func callAPI(t *testing.T, a *apiServer, method, path string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if raw, ok := body.(string); ok {
		b = []byte(raw)
	} else if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.AddCookie(&http.Cookie{Name: "airadio_admin", Value: "ut"})
	w := httptest.NewRecorder()
	a.handler(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d want %d body=%s", method, path, w.Code, want, w.Body.String())
	}
	return w
}
func TestConfigAPIRoundTrips(t *testing.T) {
	for _, db := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "sqlite"}[db], func(t *testing.T) {
			a := coverageAPI(t, db)
			for _, service := range []string{"asr", "tts", "hosting"} {
				path := "/api/" + service + "/config"
				url := "https://example.test/v1/chat/completions"
				if service == "tts" {
					url = "https://example.test/services/audio/tts/SpeechSynthesizer"
				}
				callAPI(t, a, "GET", path, nil, 200)
				callAPI(t, a, "POST", path, nil, 405)
				callAPI(t, a, "PUT", path, "{", 400)
				callAPI(t, a, "PUT", path, map[string]any{"url": "http://bad"}, 400)
				callAPI(t, a, "PUT", path, map[string]any{"url": url, "enabled": true, "api_key": "secret"}, 200)
				w := callAPI(t, a, "GET", path, nil, 200)
				if strings.Contains(w.Body.String(), "secret") {
					t.Fatal("key leaked")
				}
				callAPI(t, a, "PUT", path, map[string]any{"url": url, "enabled": true}, 200)
				callAPI(t, a, "PUT", path, map[string]any{"url": url, "clear_key": true}, 200)
			}
			path := "/api/intercom/auth/config"
			callAPI(t, a, "GET", path, nil, 200)
			callAPI(t, a, "POST", path, nil, 405)
			callAPI(t, a, "PUT", path, "{", 400)
			callAPI(t, a, "PUT", path, map[string]any{"enabled": true}, 400)
			callAPI(t, a, "PUT", path, map[string]any{"enabled": true, "api_key": "local-ut-key"}, 200)
			if db {
				a.s.loadASRConfig()
				a.s.loadHostingConfig()
				a.s.loadTTSConfig()
				a.s.loadIntercomAuthConfig()
				if a.s.authKey != "local-ut-key" {
					t.Fatal("auth config not loaded")
				}
			}
			for _, service := range []string{"asr", "hosting", "tts"} {
				p := "/api/" + service + "/test"
				callAPI(t, a, "GET", p, nil, 405)
				callAPI(t, a, "POST", p, "{", 400)
				if service != "asr" {
					callAPI(t, a, "POST", p, map[string]string{"text": "hello"}, 400)
				}
			}
		})
	}
}
func TestConsoleRoutes(t *testing.T) {
	a := coverageAPI(t, false)
	s := a.s
	s.store.RegisterDevice("one", "")
	s.store.RegisterDevice("two", "")
	callAPI(t, a, "GET", "/api/rooms", nil, 200)
	callAPI(t, a, "POST", "/api/rooms", "{", 400)
	callAPI(t, a, "DELETE", "/api/rooms", nil, 405)
	w := callAPI(t, a, "POST", "/api/rooms", map[string]any{"name": "UT room", "created_by_device_id": "one", "device_ids": []string{"two"}}, 200)
	var r room
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.ID == "" {
		t.Fatal("missing room")
	}
	callAPI(t, a, "GET", "/api/rooms/"+r.ID+"/devices", nil, 200)
	callAPI(t, a, "GET", "/api/devices", nil, 200)
	callAPI(t, a, "POST", "/api/devices", map[string]string{"device_id": "sim-ut"}, 200)
	callAPI(t, a, "GET", "/api/events", nil, 200)
	callAPI(t, a, "GET", "/api/events?device_id=one", nil, 200)
	callAPI(t, a, "PUT", "/api/events", nil, 405)
	e, err := s.events.CreateEvent("one", "two", "UT event", time.Now().Add(time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	callAPI(t, a, "GET", "/api/events/"+e.ID+"/cancel", nil, 405)
	callAPI(t, a, "POST", "/api/events/"+e.ID+"/cancel", nil, 200)
	callAPI(t, a, "POST", "/api/events/missing/cancel", nil, 400)
	callAPI(t, a, "GET", "/api/agent/skills", nil, 200)
	callAPI(t, a, "POST", "/api/agent/skills", "{", 400)
	callAPI(t, a, "PATCH", "/api/agent/skills", nil, 405)
	skill := agentSkill{ID: "test", Name: "Test", Steps: []agentStep{{Action: "broadcast"}}}
	callAPI(t, a, "PUT", "/api/agent/skills", skill, 200)
	callAPI(t, a, "GET", "/api/agent/runs", nil, 200)
	callAPI(t, a, "PUT", "/api/agent/runs", nil, 405)
	callAPI(t, a, "DELETE", "/api/agent/skills", nil, 400)
	callAPI(t, a, "DELETE", "/api/agent/skills?id=missing", nil, 404)
	callAPI(t, a, "DELETE", "/api/agent/skills?id=test", nil, 200)
	callAPI(t, a, "GET", "/api/alerts", nil, 200)
	callAPI(t, a, "GET", "/api/alerts?records=1", nil, 200)
	callAPI(t, a, "PATCH", "/api/alerts", nil, 405)
	callAPI(t, a, "PUT", "/api/alerts", "{", 400)
	n := 30.0
	for _, kind := range []string{"temperature", "humidity", "vision"} {
		rule := alertRule{ID: kind, Name: "UT", Type: kind, DeviceID: "one", RoomID: "default", Interval: 10, TempAbove: &n, HumAbove: &n, Prompt: "test"}
		callAPI(t, a, "PUT", "/api/alerts", rule, 200)
		callAPI(t, a, "GET", "/api/alerts/"+kind, nil, 200)
		callAPI(t, a, "PATCH", "/api/alerts/"+kind, nil, 405)
		callAPI(t, a, "DELETE", "/api/alerts/"+kind, nil, 200)
	}
	callAPI(t, a, "GET", "/api/alerts/missing", nil, 404)
	callAPI(t, a, "GET", "/missing", nil, 404)
}
