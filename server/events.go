package main

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

type event struct {
	ID              string `json:"event_id"`
	TargetDeviceID  string `json:"target_device_id"`
	CreatorDeviceID string `json:"creator_device_id"`
	Content         string `json:"content"`
	ScheduledAt     string `json:"scheduled_at"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	CompletedAt     string `json:"completed_at,omitempty"`
	CancelledAt     string `json:"cancelled_at,omitempty"`
}

type eventStore interface {
	CreateEvent(target, creator, content, scheduled string) (event, error)
	ListEvents(device string) []event
	SetEventStatus(id, device, status string) (event, error)
	DueEvents(now time.Time) []event
	MarkOverdue(id string) (event, error)
	AllEvents() []event
}

type memoryEventStore struct {
	mu     sync.Mutex
	events map[string]*event
}

var eventBeijing = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}()

func newMemoryEventStore() *memoryEventStore { return &memoryEventStore{events: map[string]*event{}} }
func cloneEvent(e *event) event {
	x := *e
	if t, err := time.Parse(time.RFC3339, x.ScheduledAt); err == nil {
		x.ScheduledAt = t.In(eventBeijing).Format(time.RFC3339)
	}
	return x
}
func (s *memoryEventStore) CreateEvent(target, creator, content, scheduled string) (event, error) {
	if !validID.MatchString(target) || !validID.MatchString(creator) {
		return event{}, errors.New("invalid device id")
	}
	if strings.TrimSpace(content) == "" || len([]rune(content)) > 200 {
		return event{}, errors.New("content must contain 1-200 characters")
	}
	t, err := time.Parse(time.RFC3339, scheduled)
	if err != nil {
		return event{}, errors.New("scheduled_at must be RFC3339")
	}
	if t.Before(time.Now().Add(-time.Minute)) {
		return event{}, errors.New("scheduled_at is in the past")
	}
	e := &event{ID: "evt-" + randomToken()[:16], TargetDeviceID: target, CreatorDeviceID: creator, Content: strings.TrimSpace(content), ScheduledAt: t.In(eventBeijing).Format(time.RFC3339), Status: "pending", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	s.mu.Lock()
	s.events[e.ID] = e
	s.mu.Unlock()
	return cloneEvent(e), nil
}
func (s *memoryEventStore) ListEvents(device string) []event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []event{}
	for _, e := range s.events {
		if e.TargetDeviceID == device && e.Status == "pending" {
			out = append(out, cloneEvent(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ScheduledAt < out[j].ScheduledAt })
	return out
}
func (s *memoryEventStore) AllEvents() []event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]event, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, cloneEvent(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ScheduledAt < out[j].ScheduledAt })
	return out
}
func (s *memoryEventStore) SetEventStatus(id, device, status string) (event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[id]
	if e == nil || (device != "" && e.TargetDeviceID != device && e.CreatorDeviceID != device) {
		return event{}, errors.New("event not found")
	}
	if e.Status == "completed" || e.Status == "cancelled" || e.Status == "overdue" {
		return event{}, errors.New("event already ended")
	}
	if status != "completed" && status != "cancelled" {
		return event{}, errors.New("invalid event status")
	}
	e.Status = status
	now := time.Now().UTC().Format(time.RFC3339)
	if status == "completed" {
		e.CompletedAt = now
	} else {
		e.CancelledAt = now
	}
	return cloneEvent(e), nil
}
func (s *memoryEventStore) DueEvents(now time.Time) []event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []event{}
	for _, e := range s.events {
		if e.Status == "pending" {
			t, _ := time.Parse(time.RFC3339, e.ScheduledAt)
			if !t.After(now.Add(30 * time.Minute)) {
				out = append(out, cloneEvent(e))
			}
		}
	}
	return out
}
func (s *memoryEventStore) MarkOverdue(id string) (event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[id]
	if e == nil {
		return event{}, errors.New("event not found")
	}
	e.Status = "overdue"
	return cloneEvent(e), nil
}
func eventText(e event, prefix string) string { return fmt.Sprintf("%s：%s", prefix, e.Content) }

func (s *server) eventPeer(id string) *peer { s.mu.Lock(); defer s.mu.Unlock(); return s.peers[id] }
func (s *server) notifyEvent(e event, target, kind, text string) {
	p := s.eventPeer(target)
	if p == nil {
		return
	}
	p.send(control{Type: "event_notification", EventID: e.ID, Content: text, Status: e.Status})
	if !p.chatEnabled {
		return
	}
	m := &chatMessage{ID: randomToken(), Room: s.store.CurrentRoom(target).ID, Source: e.CreatorDeviceID, Created: time.Now().UTC().Format(time.RFC3339), Text: text, Status: "completed", Expires: time.Now().Add(30 * time.Minute), recipients: []chatRecipient{{p, p.playbackToken(), s.store.DeviceLanguage(p.id)}}, EventKind: "event_" + kind, spokenText: text}
	s.chat.mu.Lock()
	cfg := s.chat.ttsConfig
	s.chat.messages = append(s.chat.messages, m)
	s.chat.pruneLocked(time.Now())
	s.chat.mu.Unlock()
	s.mu.Lock()
	s.sendChat(m, "chat_message")
	s.mu.Unlock()
	if cfg.Enabled {
		select {
		case s.chat.ttsJobs <- ttsJob{m, cfg}:
		default:
			log.Printf("event TTS queue full event=%s", e.ID)
		}
	}
}

func (s *server) runEvents() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for now := range t.C {
		for _, e := range s.events.DueEvents(now) {
			due, _ := time.Parse(time.RFC3339, e.ScheduledAt)
			delta := due.Sub(now)
			if delta <= 0 {
				if target := s.eventPeer(e.TargetDeviceID); target != nil {
					target.send(control{Type: "event_execute", EventID: e.ID, Content: e.Content, ScheduledAt: e.ScheduledAt})
				}
				if updated, err := s.events.MarkOverdue(e.ID); err == nil {
					s.notifyEvent(updated, updated.TargetDeviceID, "overdue", eventText(updated, "事件已逾期"))
					s.notifyEvent(updated, updated.CreatorDeviceID, "overdue", fmt.Sprintf("设备%s的事件%s已逾期", spokenDevice(updated.TargetDeviceID), updated.Content))
				}
				continue
			}
			mins := int(delta.Minutes())
			key := e.ID + ":" + now.UTC().Format("200601021504")
			s.eventMu.Lock()
			_, sent := s.eventReminders[key]
			if !sent {
				s.eventReminders[key] = now.UTC().Format(time.RFC3339)
			}
			s.eventMu.Unlock()
			if mins <= 30 && !sent {
				s.notifyEvent(e, e.TargetDeviceID, "reminder", fmt.Sprintf("还有%d分钟需要处理：%s", mins, e.Content))
			}
		}
	}
}
