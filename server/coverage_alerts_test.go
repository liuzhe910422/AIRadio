package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The device responder uses the real request ID so correlation and pending cleanup are exercised.
func captureReply(t *testing.T, s *server, p *peer, rule alertRule, response control) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); s.alerts.evaluate(context.Background(), rule) }()
	select {
	case msg := <-p.out:
		var request control
		if err := json.Unmarshal(msg.data, &request); err != nil {
			t.Fatal(err)
		}
		response.RequestID = request.RequestID
		s.alerts.response(p, response)
	case <-time.After(time.Second):
		t.Fatal("capture request missing")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("evaluation did not finish")
	}
	if len(s.alerts.pending) != 0 {
		t.Fatal("pending request leaked")
	}
}
func TestSensorAlertEvaluationAndRecovery(t *testing.T) {
	for _, kind := range []string{"temperature", "humidity"} {
		t.Run(kind, func(t *testing.T) {
			s := newServer()
			p := testPeer(t, s, "sensor")
			limit, high, low := 30.0, 40.0, 20.0
			r := alertRule{ID: kind, Name: "Sensor", Type: kind, Enabled: true, DeviceID: p.id, RoomID: "default", TempAbove: &limit, HumAbove: &limit}
			s.alerts.rules[r.ID] = r
			c := control{OK: boolPtr(true), Temperature: &high, Humidity: &high}
			captureReply(t, s, p, r, c)
			if !s.alerts.active[r.ID] || len(s.alerts.records) != 1 {
				t.Fatal("threshold did not activate alert")
			}
			queuedControls(p)
			c.Temperature = &low
			c.Humidity = &low
			captureReply(t, s, p, r, c)
			if s.alerts.active[r.ID] {
				t.Fatal("alert not recovered")
			}
			queuedControls(p)
			captureReply(t, s, p, r, control{OK: boolPtr(false)})
			if len(s.alerts.records) != 1 {
				t.Fatal("failed capture recorded")
			}
		})
	}
}
func TestAlertRequestQueueAndResponses(t *testing.T) {
	s := newServer()
	p := &peer{id: "full", out: make(chan outgoing)}
	if _, err := s.alerts.request(p, "sensor"); err == nil {
		t.Fatal("full peer accepted request")
	}
	s.alerts.response(p, control{RequestID: "missing"})
	ch := make(chan control, 1)
	s.alerts.pending["x"] = ch
	s.alerts.response(p, control{RequestID: "x"})
	s.alerts.response(p, control{RequestID: "x"})
	if len(ch) != 1 {
		t.Fatal("response not queued")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.alerts.run(ctx)
	s.alerts.tick(ctx, time.Now())
	if len(s.alertList()) != 3 {
		t.Fatal("default rules missing")
	}
	s.alerts.evaluate(ctx, alertRule{DeviceID: "offline"})
}
func TestVisionResponsesAndFetch(t *testing.T) {
	s := newServer()
	a := s.alerts
	if hit, _ := a.vision(context.Background(), alertRule{}, nil); hit {
		t.Fatal("unconfigured vision hit")
	}
	for _, tc := range []struct {
		body   string
		status int
		hit    bool
	}{
		{`{"choices":[{"message":{"content":"{\"alert\":true,\"message\":\" fire \"}"}}]}`, 200, true},
		{`{`, 200, false}, {`{"choices":[]}`, 200, false}, {`{"choices":[{"message":{"content":"bad"}}]}`, 200, false}, {`error`, 500, false},
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
		s.chat.hostingConfig = hostingConfig{URL: ts.URL, Key: "ut", Model: "m"}
		hit, msg := a.vision(context.Background(), alertRule{}, []byte("jpg"))
		ts.Close()
		if hit != tc.hit || hit && msg != "fire" {
			t.Fatalf("hit=%v msg=%q", hit, msg)
		}
	}
	s.images["cached"] = []byte("cached-image")
	b, err := a.fetch("/api/intercom/images/cached")
	if err != nil || string(b) != "cached-image" {
		t.Fatal("cache fetch")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("remote")) }))
	defer ts.Close()
	s.publicBaseURL = ts.URL
	b, err = a.fetch("/remote")
	if err != nil || string(b) != "remote" {
		t.Fatal("remote fetch")
	}
	if _, err = a.fetch(":bad-url"); err == nil {
		t.Fatal("invalid URL accepted")
	}
	p := testPeer(t, s, "viewer")
	p.chatEnabled = true
	a.broadcastImage(alertRule{RoomID: "default"}, []byte("image"), "/image")
	if len(queuedControls(p)) != 1 {
		t.Fatal("image not delivered")
	}
	a.records = make([]alertRecord, 500)
	a.record(alertRule{Type: "vision"}, "", "", "url", "hit")
	if len(a.records) != 500 || a.records[499].Message != "hit" {
		t.Fatal("record bound")
	}
	s.broadcastRoom("default", "broadcast")
	if len(s.chat.messages) == 0 {
		t.Fatal("broadcast not stored")
	}
}
func TestServicePersistence(t *testing.T) {
	a := coverageAPI(t, true)
	s := a.s
	s.agent.load()
	if len(s.agent.listSkills()) == 0 {
		t.Fatal("default skill missing")
	}
	s.agent.skills["custom"] = agentSkill{ID: "custom", Name: "custom", Steps: []agentStep{{Action: "vision_check"}}}
	if err := s.agent.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s.agent = newAgentService(s)
	s.agent.load()
	if s.agent.skills["custom"].Steps[0].Prompt == "" {
		t.Fatal("prompt migration missing")
	}
	s.alerts.load()
	s.alerts.records = []alertRecord{{Message: "persisted"}}
	s.alerts.active["temperature"] = true
	if err := s.alerts.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s.alerts = newAlertService(s)
	s.alerts.load()
	if len(s.alerts.records) != 1 || !s.alerts.active["temperature"] {
		t.Fatal("alert state not restored")
	}
	e, err := s.events.CreateEvent("dev", "creator", "persistent", time.Now().Add(time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	_ = e
	db := s.store.(*sqliteStore)
	persisted, err := db.CreateEvent("dev", "creator", "db-event", time.Now().Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if len(db.AllEvents()) != 1 || len(db.ListEvents("dev")) != 1 || len(db.DueEvents(time.Now())) != 1 {
		t.Fatal("sqlite event views")
	}
	if _, err = db.SetEventStatus(persisted.ID, "dev", "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.MarkOverdue(persisted.ID); err != nil {
		t.Fatal(err)
	}
	db.loadEventData()
	if len(db.AllEvents()) != 1 || db.AllEvents()[0].Status != "overdue" {
		t.Fatal("event reload")
	}
}
