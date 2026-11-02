package main

import (
	"testing"
	"time"
)

func TestConfigurationStorageFailures(t *testing.T) {
	a := coverageAPI(t, true)
	db := a.s.store.(*sqliteStore)
	if _, err := db.exec("DROP TABLE service_config"); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"asr", "hosting", "tts"} {
		url := "https://example.test/chat/completions"
		if service == "tts" {
			url = "https://example.test/services/audio/tts/SpeechSynthesizer"
		}
		callAPI(t, a, "PUT", "/api/"+service+"/config", map[string]any{"url": url}, 500)
	}
	callAPI(t, a, "PUT", "/api/intercom/auth/config", map[string]any{"enabled": false}, 500)
	callAPI(t, a, "PUT", "/api/agent/skills", agentSkill{ID: "broken", Name: "broken", Steps: []agentStep{{Action: "broadcast"}}}, 500)
	callAPI(t, a, "DELETE", "/api/agent/skills?id=broken", nil, 500)
	a.s.store.RegisterDevice("sensor", "")
	n := 30.0
	callAPI(t, a, "PUT", "/api/alerts", alertRule{ID: "temperature", Type: "temperature", Name: "rule", DeviceID: "sensor", RoomID: "default", Interval: 10, TempAbove: &n}, 500)
	callAPI(t, a, "DELETE", "/api/alerts/temperature", nil, 500)
	a.s.loadHostingConfig()
	a.s.loadTTSConfig()
	a.s.loadIntercomAuthConfig()
	a.s.agent.load()
	a.s.alerts.load()
	// A non-directory parent causes SQLite exec to fail without changing any shared files.
	db.path = db.path + "/missing.db"
	a.s.loadASRConfig()
	if _, err := db.CreateEvent("dev", "creator", "event", time.Now().Add(time.Hour).Format(time.RFC3339)); err == nil {
		t.Fatal("unwritable event persisted")
	}
}
func TestSQLiteEventValidationFailures(t *testing.T) {
	db := newSQLiteTestStore(t)
	if _, err := db.CreateEvent("bad!", "creator", "event", time.Now().Add(time.Hour).Format(time.RFC3339)); err == nil {
		t.Fatal("bad event")
	}
	if _, err := db.SetEventStatus("missing", "dev", "completed"); err == nil {
		t.Fatal("missing event")
	}
	if _, err := db.MarkOverdue("missing"); err == nil {
		t.Fatal("missing event overdue")
	}
	if err := db.SetDeviceLanguage("missing", "en-US"); err != nil {
		t.Fatal(err)
	}
	if db.DeviceLanguage("missing") != "en-US" {
		t.Fatal("new device language not saved")
	}
}
