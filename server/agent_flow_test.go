package main

import (
	"context"
	"testing"
	"time"
)

func TestAgentCaptureUsesDeviceTask(t *testing.T) {
	s := newServer()
	p := &peer{id: "001", out: make(chan outgoing, 16)}
	s.peers[p.id] = p
	s.store.RegisterDevice(p.id, "")
	run := &agentRun{ID: "demo", AlertID: "temperature", Status: "running"}
	s.agent.runs[run.ID] = run
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.agent.execute(ctx, run, agentSkill{Steps: []agentStep{{Action: "capture_image", DeviceID: "001"}}}, alertRule{ID: "temperature", DeviceID: "001"})
		close(done)
	}()
	select {
	case <-p.out:
	case <-time.After(time.Second):
		t.Fatal("task not dispatched")
	}
	es := s.events.ListEvents("001")
	if len(es) != 1 {
		t.Fatalf("expected pending capture task: %v", es)
	}
	s.agent.imageUploaded("002", "wrong.jpg")
	if len(s.events.ListEvents("001")) != 1 {
		t.Fatal("wrong device completed task")
	}
	s.agent.imageUploaded("001", "photo.jpg")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("upload did not advance agent")
	}
	if s.events.AllEvents()[0].Status != "completed" {
		t.Fatal("capture task not completed")
	}
}

func TestRecoveryEndsAgentAndNotifiesOnce(t *testing.T) {
	s := newServer()
	s.store.RegisterDevice("001", "")
	p := &peer{id: "001", out: make(chan outgoing, 16)}
	s.peers[p.id] = p
	e, _ := s.events.CreateEvent("001", "001", "拍照", time.Now().Add(time.Hour).Format(time.RFC3339))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.agent.runs["run"] = &agentRun{ID: "run", AlertID: "temperature", Status: "waiting_image"}
	s.agent.cancels["run"] = cancel
	s.agent.tasks[e.ID] = "run"
	s.agent.captures["001"] = &agentCapture{eventID: e.ID, runID: "run", image: make(chan string, 1)}
	rule := alertRule{ID: "temperature", RoomID: "default", Name: "温度"}
	s.alerts.activate(rule)
	s.alerts.recoverRule(rule, "")
	if ctx.Err() == nil {
		t.Fatal("agent not cancelled")
	}
	if s.events.AllEvents()[0].Status != "cancelled" {
		t.Fatal("task still pending")
	}
	count := len(s.chat.messages)
	s.alerts.recoverRule(rule, "")
	if len(s.chat.messages) != count {
		t.Fatal("duplicate recovery notification")
	}
	if len(s.agent.captures) != 0 {
		t.Fatal("capture mapping retained")
	}
}

func TestAgentHumanCompletionCallback(t *testing.T) {
	s := newServer()
	s.store.RegisterDevice("001", "")
	p := &peer{id: "002", out: make(chan outgoing, 16)}
	s.peers[p.id] = p
	run := &agentRun{ID: "manual", Status: "running"}
	s.agent.runs[run.ID] = run
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.agent.execute(ctx, run, agentSkill{Steps: []agentStep{{Action: "create_task", DeviceID: "002", Content: "更换传感器"}}}, alertRule{DeviceID: "001"})
		close(done)
	}()
	select {
	case <-p.out:
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	e := s.events.ListEvents("002")[0]
	updated, err := s.events.SetEventStatus(e.ID, "002", "completed")
	if err != nil {
		t.Fatal(err)
	}
	s.agent.eventCompleted(updated.ID, updated.Status)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion callback did not resume agent")
	}
}
