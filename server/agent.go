package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

type agentStep struct {
	ID              string `json:"id"`
	Action          string `json:"action"`
	DeviceID        string `json:"device_id,omitempty"`
	Content         string `json:"content,omitempty"`
	Attempts        int    `json:"attempts,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
}
type agentSkill struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Enabled bool        `json:"enabled"`
	Trigger string      `json:"trigger"`
	Steps   []agentStep `json:"steps"`
}
type agentRun struct {
	ID          string `json:"id"`
	SkillID     string `json:"skill_id"`
	AlertID     string `json:"alert_id"`
	Status      string `json:"status"`
	CurrentStep int    `json:"current_step"`
	Message     string `json:"message,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type agentService struct {
	s        *server
	mu       sync.Mutex
	skills   map[string]agentSkill
	runs     map[string]*agentRun
	waits    map[string]chan string
	captures map[string]*agentCapture
	cancels  map[string]context.CancelFunc
	tasks    map[string]string
}

type agentCapture struct {
	eventID string
	runID   string
	image   chan string
}

const defaultAgentVisionPrompt = "检查设备1的红色指示灯是否亮起。只要红色指示灯亮起就返回alert=true，否则返回alert=false；message简要说明判断依据。"

func newAgentService(s *server) *agentService {
	return &agentService{s: s, skills: map[string]agentSkill{}, runs: map[string]*agentRun{}, waits: map[string]chan string{}, captures: map[string]*agentCapture{}, cancels: map[string]context.CancelFunc{}, tasks: map[string]string{}}
}
func (a *agentService) load() {
	if d, ok := a.s.store.(*sqliteStore); ok {
		b, e := d.exec("SELECT value FROM service_config WHERE name='agent_skills'")
		if e == nil {
			var rows []struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(b, &rows) == nil && len(rows) > 0 {
				var xs []agentSkill
				if json.Unmarshal([]byte(rows[0].Value), &xs) == nil {
					for _, x := range xs {
						a.skills[x.ID] = x
					}
				}
			}
		}
	}
	if len(a.skills) == 0 {
		a.skills["high_temperature"] = agentSkill{ID: "high_temperature", Name: "高温处理", Enabled: true, Trigger: "temperature", Steps: []agentStep{{ID: "capture", Action: "capture_image", DeviceID: "001"}, {ID: "analyze", Action: "vision_check", Prompt: defaultAgentVisionPrompt}, {ID: "replace", Action: "create_task", DeviceID: "002", Content: "请更换设备001的传感器"}, {ID: "check", Action: "check_temperature", DeviceID: "001", Attempts: 3, IntervalSeconds: 10}, {ID: "done", Action: "broadcast", Content: "告警已恢复正常"}}}
	}
	// Older demo skills used the generic alert prompt; give them an explicit, editable red-light check.
	for id, skill := range a.skills {
		for i := range skill.Steps {
			if skill.Steps[i].Action == "vision_check" && skill.Steps[i].Prompt == "" {
				skill.Steps[i].Prompt = defaultAgentVisionPrompt
			}
		}
		a.skills[id] = skill
	}

}
func (a *agentService) saveLocked() error {
	d, ok := a.s.store.(*sqliteStore)
	if !ok {
		return nil
	}
	xs := make([]agentSkill, 0, len(a.skills))
	for _, x := range a.skills {
		xs = append(xs, x)
	}
	b, _ := json.Marshal(xs)
	_, e := d.exec("INSERT INTO service_config(name,value) VALUES('agent_skills'," + sqliteString(string(b)) + ") ON CONFLICT(name) DO UPDATE SET value=excluded.value")
	return e
}
func (a *agentService) listSkills() []agentSkill {
	a.mu.Lock()
	defer a.mu.Unlock()
	o := make([]agentSkill, 0, len(a.skills))
	for _, x := range a.skills {
		o = append(o, x)
	}
	return o
}
func (a *agentService) trigger(r alertRule, text string) {
	a.mu.Lock()
	var sk agentSkill
	for _, x := range a.skills {
		if x.Enabled && (x.Trigger == r.Type || x.ID == r.AgentSkillID) {
			sk = x
			break
		}
	}
	if sk.ID == "" {
		a.mu.Unlock()
		return
	}
	for _, run := range a.runs {
		if run.AlertID == r.ID && (run.Status == "waiting_image" || run.Status == "running" || run.Status == "waiting_task" || run.Status == "checking") {
			a.mu.Unlock()
			return
		}
	}
	id := "run-" + randomToken()[:12]
	run := &agentRun{ID: id, SkillID: sk.ID, AlertID: r.ID, Status: "running", CreatedAt: time.Now().UTC().Format(time.RFC3339), UpdatedAt: time.Now().UTC().Format(time.RFC3339), Message: text}
	a.runs[id] = run
	a.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancels[id] = cancel
	a.mu.Unlock()
	go a.execute(ctx, run, sk, r)
}
func (a *agentService) execute(ctx context.Context, run *agentRun, sk agentSkill, r alertRule) {
	defer func() {
		a.mu.Lock()
		if cancel := a.cancels[run.ID]; cancel != nil {
			cancel()
		}
		delete(a.cancels, run.ID)
		a.mu.Unlock()
	}()
	var image string
	for i, st := range sk.Steps {
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		if ctx.Err() != nil {
			a.mu.Unlock()
			return
		}
		run.Status = "running"
		run.CurrentStep = i
		run.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		a.mu.Unlock()
		switch st.Action {
		case "capture_image":
			target := st.DeviceID
			if target == "" {
				target = r.DeviceID
			}
			content := st.Content
			if content == "" {
				content = "请拍摄设备1的状态照片，调用拍照工具上传图片"
			}
			e, err := a.s.events.CreateEvent(target, r.DeviceID, content, time.Now().Add(24*time.Hour).Format(time.RFC3339))
			if err != nil {
				a.finish(run, "failed", err.Error())
				return
			}
			capture := &agentCapture{eventID: e.ID, runID: run.ID, image: make(chan string, 1)}
			a.mu.Lock()
			if ctx.Err() != nil {
				a.mu.Unlock()
				a.s.events.SetEventStatus(e.ID, "", "cancelled")
				return
			}
			a.tasks[e.ID] = run.ID
			a.captures[target] = capture
			run.Status = "waiting_image"
			a.mu.Unlock()
			a.s.dispatchEvent(e)
			select {
			case image = <-capture.image:
			case <-ctx.Done():
				return
			}
		case "vision_check":
			data, e := a.s.alerts.fetch(image)
			if e != nil {
				a.finish(run, "failed", "image unavailable")
				return
			}
			visionRule := r
			visionRule.Prompt = st.Prompt
			hit, msg := a.s.alerts.vision(ctx, visionRule, data)
			if ctx.Err() != nil {
				return
			}
			if !hit {
				a.finish(run, "completed", "未检测到异常")
				return
			}
			a.mu.Lock()
			run.Message = msg
			a.mu.Unlock()
		case "create_task":
			target := st.DeviceID
			if target == "" {
				target = r.DeviceID
			}
			when := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
			e, err := a.s.events.CreateEvent(target, r.DeviceID, st.Content, when)
			if err != nil {
				a.finish(run, "failed", err.Error())
				return
			}
			a.mu.Lock()
			run.Status = "waiting_task"
			wait := make(chan string, 1)
			if ctx.Err() != nil {
				a.mu.Unlock()
				a.s.events.SetEventStatus(e.ID, "", "cancelled")
				return
			}
			a.waits[e.ID] = wait
			a.tasks[e.ID] = run.ID
			a.mu.Unlock()
			a.s.dispatchEvent(e)
			var status string
			select {
			case status = <-wait:
			case <-ctx.Done():
				return
			}
			a.mu.Lock()
			delete(a.waits, e.ID)
			delete(a.tasks, e.ID)
			a.mu.Unlock()
			if status != "completed" {
				a.finish(run, "failed", "task not completed")
				return
			}
			a.mu.Lock()
			run.Status = "running"
			a.mu.Unlock()
		case "check_temperature":
			attempts := st.Attempts
			if attempts <= 0 {
				attempts = 3
			}
			interval := st.IntervalSeconds
			if interval <= 0 {
				interval = 10
			}
			ok := false
			for n := 0; n < attempts; n++ {
				p := a.s.eventPeer(st.DeviceID)
				if p != nil {
					c, e := a.s.alerts.request(p, "sensor")
					if e == nil && c.Temperature != nil {
						a.s.alerts.mu.Lock()
						if current, exists := a.s.alerts.rules[r.ID]; exists {
							r = current
						}
						a.s.alerts.mu.Unlock()
						if (r.TempAbove != nil && *c.Temperature <= *r.TempAbove) || (r.TempBelow != nil && *c.Temperature >= *r.TempBelow) {
							ok = true
							break
						}
					}
				}
				if n+1 < attempts {
					select {
					case <-time.After(time.Duration(interval) * time.Second):
					case <-ctx.Done():
						return
					}
				}
			}
			if !ok {
				a.finish(run, "failed", "temperature remains abnormal")
				return
			}
		case "broadcast":
			if ctx.Err() != nil {
				return
			}
			a.s.alerts.recoverRule(r, st.Content)
			a.finish(run, "completed", st.Content)
			return
		}
	}
	a.finish(run, "completed", "completed")
}

// eventCompleted advances a run immediately when its linked device event changes state.
func (a *agentService) eventCompleted(eventID, status string) {
	a.mu.Lock()
	ch := a.waits[eventID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- status:
		default:
		}
	}
}
func (a *agentService) finish(run *agentRun, status, msg string) {
	a.mu.Lock()
	if run.Status == "recovered" {
		a.mu.Unlock()
		return
	}
	run.Status = status
	run.Message = msg
	run.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.mu.Unlock()
	if status == "completed" && msg != "" {
		log.Printf("agent run completed id=%s message=%s", run.ID, msg)
	}
}
func (a *agentService) runsList() []agentRun {
	a.mu.Lock()
	defer a.mu.Unlock()
	o := []agentRun{}
	for _, r := range a.runs {
		o = append(o, *r)
	}
	return o
}

func (s *server) dispatchEvent(e event) {
	if p := s.eventPeer(e.TargetDeviceID); p != nil {
		p.send(control{Type: "event_state", EventID: e.ID, Content: e.Content, ScheduledAt: e.ScheduledAt, Status: e.Status})
		s.notifyEvent(e, e.TargetDeviceID, "created", fmt.Sprintf("收到新事件：%s", e.Content))
	}
}
func (s *server) broadcastRoom(roomID, text string) {
	s.alerts.notify(alertRule{RoomID: roomID, TTS: true}, text)
}

// The first upload from a device with a capture task belongs to that task.
func (a *agentService) imageUploaded(deviceID, imageURL string) {
	a.mu.Lock()
	capture := a.captures[deviceID]
	if capture == nil {
		a.mu.Unlock()
		return
	}
	delete(a.captures, deviceID)
	delete(a.tasks, capture.eventID)
	e, err := a.s.events.SetEventStatus(capture.eventID, "", "completed")
	a.mu.Unlock()
	if err != nil {
		return
	}
	a.s.notifyEvent(e, e.TargetDeviceID, "completed", "拍照任务已完成")
	log.Printf("agent capture completed device=%s event=%s image_url=%s", deviceID, e.ID, imageURL)
	capture.image <- imageURL
}

func (a *agentService) recoverAlert(alertID string) {
	a.mu.Lock()
	ids := map[string]bool{}
	for id, run := range a.runs {
		if run.AlertID == alertID {
			ids[id] = true
			if cancel := a.cancels[id]; cancel != nil {
				cancel()
			}
			run.Status = "recovered"
			run.Message = "告警已恢复正常"
			run.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}
	pending := []string{}
	for eventID, runID := range a.tasks {
		if ids[runID] {
			pending = append(pending, eventID)
			delete(a.tasks, eventID)
			delete(a.waits, eventID)
		}
	}
	for device, c := range a.captures {
		if ids[c.runID] {
			delete(a.captures, device)
		}
	}
	a.mu.Unlock()
	for _, id := range pending {
		if e, err := a.s.events.SetEventStatus(id, "", "cancelled"); err == nil {
			a.s.notifyEvent(e, e.TargetDeviceID, "cancelled", "告警已恢复，任务自动结束")
		}
	}
}
