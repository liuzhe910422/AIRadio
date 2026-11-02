package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentTriggerAndExecutionBranches(t *testing.T) {
	s := newServer()
	a := s.agent
	r := alertRule{ID: "alarm", Type: "temperature", DeviceID: "source", RoomID: "default"}
	a.trigger(r, "no matching skill")
	if len(a.runs) != 0 {
		t.Fatal("unexpected run")
	}
	a.skills["broadcast"] = agentSkill{ID: "broadcast", Enabled: true, Trigger: "temperature", Steps: []agentStep{{Action: "broadcast", Content: "done"}}}
	a.trigger(r, "start")
	deadline := time.Now().Add(time.Second)
	for {
		runs := a.runsList()
		if len(runs) == 1 && runs[0].Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("trigger did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	a.mu.Lock()
	a.runs["busy"] = &agentRun{ID: "busy", AlertID: "alarm", Status: "waiting_task"}
	a.mu.Unlock()
	a.trigger(r, "duplicate")
	if len(a.runsList()) != 2 {
		t.Fatal("duplicate run started")
	}
	for _, steps := range [][]agentStep{
		{{Action: "capture_image", DeviceID: "bad!"}},
		{{Action: "create_task", DeviceID: "bad!", Content: "task"}},
		{{Action: "vision_check"}},
		{{Action: "check_temperature", DeviceID: "offline", Attempts: 1, IntervalSeconds: 1}},
	} {
		run := &agentRun{ID: "branch", Status: "running"}
		a.execute(context.Background(), run, agentSkill{Steps: steps}, r)
		if run.Status != "failed" {
			t.Fatalf("steps=%+v status=%s", steps, run.Status)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &agentRun{Status: "running"}
	a.execute(ctx, run, agentSkill{Steps: []agentStep{{Action: "broadcast"}}}, r)
	if run.Status != "running" {
		t.Fatal("cancelled run executed")
	}
}
func TestAgentTemperatureSuccess(t *testing.T) {
	s := newServer()
	p := testPeer(t, s, "sensor-check")
	limit, temp := 30.0, 20.0
	r := alertRule{ID: "temp", DeviceID: p.id, TempAbove: &limit}
	s.alerts.rules[r.ID] = r
	run := &agentRun{ID: "check", Status: "running"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.agent.execute(context.Background(), run, agentSkill{Steps: []agentStep{{Action: "check_temperature", DeviceID: p.id, Attempts: 1}}}, r)
	}()
	msg := <-p.out
	var request control
	if err := json.Unmarshal(msg.data, &request); err != nil {
		t.Fatal(err)
	}
	s.alerts.response(p, control{RequestID: request.RequestID, Temperature: &temp})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("temperature check blocked")
	}
	if run.Status != "completed" {
		t.Fatal(run.Status)
	}
}

func TestAgentVisionAndTaskCancellation(t *testing.T) {
	s := newServer()
	s.images[""] = []byte("image")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"alert\":true,\"message\":\"abnormal\"}"}}]}`))
	}))
	defer ts.Close()
	s.chat.hostingConfig = hostingConfig{URL: ts.URL, Key: "ut"}
	// The empty initial image URL is fetched from the local test HTTP server.
	// A capture step provides a real cached image before vision executes.
	run := &agentRun{ID: "vision", Status: "running"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.agent.execute(context.Background(), run, agentSkill{Steps: []agentStep{{Action: "capture_image", DeviceID: "camera"}, {Action: "vision_check"}}}, alertRule{DeviceID: "source"})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		s.agent.mu.Lock()
		capture := s.agent.captures["camera"]
		s.agent.mu.Unlock()
		if capture != nil {
			s.images["agent"] = []byte("image")
			s.agent.imageUploaded("camera", "/api/intercom/images/agent")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture missing")
		}
		time.Sleep(time.Millisecond)
	}
	<-done
	if run.Status != "completed" || run.Message != "completed" {
		t.Fatal(run)
	}
}
