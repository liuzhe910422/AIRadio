package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type alertRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Enabled   bool     `json:"enabled"`
	RoomID    string   `json:"room_id"`
	DeviceID  string   `json:"device_id"`
	Interval  int      `json:"interval_seconds"`
	Prompt    string   `json:"prompt,omitempty"`
	TempAbove *float64 `json:"temperature_above,omitempty"`
	TempBelow *float64 `json:"temperature_below,omitempty"`
	HumAbove  *float64 `json:"humidity_above,omitempty"`
	HumBelow  *float64 `json:"humidity_below,omitempty"`
	Message   string   `json:"message,omitempty"`
	TTS       bool     `json:"tts"`
}
type alertRecord struct {
	ID        string `json:"id"`
	Created   string `json:"created_at"`
	Type      string `json:"type"`
	RoomID    string `json:"room_id"`
	DeviceID  string `json:"device_id"`
	Actual    string `json:"actual"`
	Threshold string `json:"threshold"`
	ImageURL  string `json:"image_url,omitempty"`
	Message   string `json:"message,omitempty"`
}
type alertService struct {
	s       *server
	mu      sync.Mutex
	rules   map[string]alertRule
	pending map[string]chan control
	lastRun map[string]time.Time
	records []alertRecord
}

const defaultVisionAlertPrompt = `检测图片中是否出现火焰或火苗。只要画面中能看到火焰形状或火焰内容就必须告警，包括真实世界中的打火机火苗、火柴火焰、蜡烛火焰、明火、燃烧物火焰，也包括电脑、手机、平板、电视或显示器屏幕上显示的火焰图片、火焰视频、火灾画面或火焰缩略图。为了便于测试，屏幕上的火焰图片同样算告警。烟雾、红色物体、灯光、反光、热源和没有明显火焰形状的内容不要告警。检测到火焰或屏幕火焰内容时返回 alert=true，并在 message 中简短说明位置或类型；没有火焰时返回 alert=false。只返回 JSON：{"alert":true或false,"message":"..."}。`

func newAlertService(s *server) *alertService {
	return &alertService{s: s, rules: map[string]alertRule{"temperature": {ID: "temperature", Type: "temperature"}, "humidity": {ID: "humidity", Type: "humidity"}, "vision": {ID: "vision", Type: "vision"}}, pending: map[string]chan control{}, lastRun: map[string]time.Time{}, records: []alertRecord{}}
}
func (a *alertService) load() {
	if db, ok := a.s.store.(*sqliteStore); ok {
		b, e := db.exec("SELECT value FROM service_config WHERE name='alerts'")
		if e == nil {
			var rows []struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(b, &rows) == nil && len(rows) > 0 {
				var saved struct {
					Rules   []alertRule   `json:"rules"`
					Records []alertRecord `json:"records"`
				}
				if json.Unmarshal([]byte(rows[0].Value), &saved) == nil {
					for _, r := range saved.Rules {
						a.rules[r.Type] = r
					}
					if v := a.rules["vision"]; v.Prompt == "" {
						v.Prompt = defaultVisionAlertPrompt
						a.rules["vision"] = v
						_ = a.saveLocked()
					}
					a.records = saved.Records
				}
			}
		}
	}
	if v := a.rules["vision"]; v.Prompt == "" {
		v := a.rules["vision"]
		v.Prompt = defaultVisionAlertPrompt
		a.rules["vision"] = v
	}
}
func (a *alertService) saveLocked() error {
	db, ok := a.s.store.(*sqliteStore)
	if !ok {
		return nil
	}
	rs := make([]alertRule, 0, len(a.rules))
	for _, r := range a.rules {
		rs = append(rs, r)
	}
	b, _ := json.Marshal(struct {
		Rules   []alertRule   `json:"rules"`
		Records []alertRecord `json:"records"`
	}{rs, a.records})
	_, e := db.exec("INSERT INTO service_config(name,value) VALUES('alerts'," + sqliteString(string(b)) + ") ON CONFLICT(name) DO UPDATE SET value=excluded.value")
	return e
}
func (a *alertService) response(p *peer, c control) {
	a.mu.Lock()
	ch := a.pending[c.RequestID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- c:
		default:
		}
	}
	_ = p
}
func (a *alertService) request(p *peer, typ string) (control, error) {
	id := randomToken()
	ch := make(chan control, 1)
	a.mu.Lock()
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.pending, id); a.mu.Unlock() }()
	if !p.send(control{Type: "alert_capture_request", RequestID: id, Content: typ}) {
		return control{}, errors.New("device unavailable")
	}
	select {
	case c := <-ch:
		return c, nil
	case <-time.After(15 * time.Second):
		return control{}, errors.New("capture timeout")
	}
}
func (a *alertService) run(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.tick(ctx, now)
		}
	}
}
func (a *alertService) tick(ctx context.Context, now time.Time) {
	a.mu.Lock()
	rs := make([]alertRule, 0, len(a.rules))
	for _, r := range a.rules {
		last := a.lastRun[r.ID]
		if r.Enabled && r.Interval > 0 && (last.IsZero() || now.Sub(last) >= time.Duration(r.Interval)*time.Second) {
			a.lastRun[r.ID] = now
			rs = append(rs, r)
		}
	}
	a.mu.Unlock()
	for _, r := range rs {
		go a.evaluate(ctx, r)
	}
}
func (a *alertService) evaluate(ctx context.Context, r alertRule) {
	p := a.s.eventPeer(r.DeviceID)
	if p == nil || a.s.store.CurrentRoom(p.id).ID != r.RoomID {
		return
	}
	typ := "sensor"
	if r.Type == "vision" {
		typ = "image"
	}
	c, e := a.request(p, typ)
	if e != nil || c.OK != nil && !(*c.OK) {
		return
	}
	if r.Type == "vision" {
		if c.ImageURL == "" {
			return
		}
		data, e := a.fetch(c.ImageURL)
		if e != nil {
			log.Printf("alert vision image fetch failed alert=%s device=%s url=%s err=%v", r.ID, r.DeviceID, c.ImageURL, e)
			return
		}
		a.saveImage(r, data)
		hit, msg := a.vision(ctx, r, data)
		if hit {
			a.broadcastImage(r, data, c.ImageURL)
			a.record(r, "", "", c.ImageURL, msg)
			a.notify(r, msg)
		}
	} else {
		if c.Temperature == nil && c.Humidity == nil {
			return
		}
		temp, hum := 0.0, 0.0
		if c.Temperature != nil {
			temp = *c.Temperature
		}
		if c.Humidity != nil {
			hum = *c.Humidity
		}
		hit := (r.Type == "temperature" && ((r.TempAbove != nil && c.Temperature != nil && temp > *r.TempAbove) || (r.TempBelow != nil && c.Temperature != nil && temp < *r.TempBelow))) || (r.Type == "humidity" && ((r.HumAbove != nil && c.Humidity != nil && hum > *r.HumAbove) || (r.HumBelow != nil && c.Humidity != nil && hum < *r.HumBelow)))
		if hit {
			msg := fmt.Sprintf("【环境告警】温度 %.1f°C，湿度 %.1f%%", temp, hum)
			actual, threshold := fmt.Sprintf("%.1f", temp), ""
			if r.Type == "humidity" {
				actual = fmt.Sprintf("%.1f", hum)
			}
			a.record(r, actual, threshold, "", msg)
			a.notify(r, msg)
		}
	}
}
func (a *alertService) fetch(u string) ([]byte, error) {
	if strings.HasPrefix(u, "/") {
		if strings.HasPrefix(u, "/api/intercom/images/") {
			id := strings.TrimPrefix(u, "/api/intercom/images/")
			a.s.imageMu.RLock()
			data, ok := a.s.images[id]
			a.s.imageMu.RUnlock()
			if ok {
				return data, nil
			}
		}
		u = a.s.publicBaseURL + u
	}
	resp, e := http.Get(u)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 600*1024))
}

func (a *alertService) saveImage(r alertRule, data []byte) {
	dir := "/Users/liuzhe/new-work/AIRadio/server/recordings/pic"
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Printf("alert vision image mkdir failed dir=%s err=%v", dir, err)
		return
	}
	name := fmt.Sprintf("%s_%s_%s.jpg", time.Now().UTC().Format("20060102T150405.000000000Z"), r.ID, randomToken()[:12])
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Printf("alert vision image save failed path=%s err=%v", path, err)
		return
	}
	log.Printf("alert vision image saved path=%s bytes=%d", path, len(data))
}
func (a *alertService) broadcastImage(r alertRule, data []byte, url string) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	room := r.RoomID
	sent := 0
	for _, p := range a.s.peers {
		if p.chatEnabled && a.s.store.CurrentRoom(p.id).ID == room {
			if p.send(control{Type: "image_message", MessageID: randomToken(), SourceID: r.DeviceID, ImageURL: url, Text: "告警采集图片", ChatSessionID: p.playbackToken()}) {
				sent++
			} else {
				log.Printf("alert image broadcast enqueue failed device=%s room=%s", p.id, room)
			}
			log.Printf("alert image broadcast device=%s room=%s url=%s", p.id, room, url)
		}
	}
	log.Printf("alert image broadcast complete alert=%s room=%s source=%s recipients=%d bytes=%d", r.ID, room, r.DeviceID, sent, len(data))
}
func (a *alertService) vision(ctx context.Context, r alertRule, data []byte) (bool, string) {
	a.s.chat.mu.Lock()
	cfg := a.s.chat.hostingConfig
	a.s.chat.mu.Unlock()
	if cfg.Key == "" || cfg.URL == "" {
		return false, ""
	}
	prompt := r.Prompt
	if prompt == "" {
		prompt = "请判断图片是否存在需要告警的情况"
	}
	log.Printf("alert vllm prompt alert=%s prompt=%s", r.ID, prompt)
	body := map[string]any{"model": cfg.Model, "messages": []any{map[string]any{"role": "system", "content": prompt + "\n只返回JSON：alert布尔值、message字符串。"}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "分析这张图片"}, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)}}}}}, "stream": false, "response_format": map[string]string{"type": "json_object"}}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", cfg.URL, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, e := a.s.chat.client.Do(req)
	if e != nil {
		log.Printf("alert vllm request failed alert=%s err=%v", r.ID, e)
		return false, ""
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	log.Printf("alert vllm response alert=%s status=%d body=%s", r.ID, resp.StatusCode, strings.TrimSpace(string(raw)))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || readErr != nil {
		return false, ""
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Choices) == 0 {
		return false, ""
	}
	var v struct {
		Alert   bool   `json:"alert"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(out.Choices[0].Message.Content), &v) != nil {
		return false, ""
	}
	log.Printf("alert vllm decision alert=%s result=%t message=%s", r.ID, v.Alert, strings.TrimSpace(v.Message))
	return v.Alert, strings.TrimSpace(v.Message)
}
func (a *alertService) notify(r alertRule, text string) {
	if text == "" {
		text = r.Message
	}
	if text == "" {
		text = "系统告警"
	}
	a.s.mu.Lock()
	room := r.RoomID
	rec := []chatRecipient{}
	for _, p := range a.s.peers {
		if p.chatEnabled && a.s.store.CurrentRoom(p.id).ID == room {
			rec = append(rec, chatRecipient{p, p.playbackToken(), a.s.store.DeviceLanguage(p.id)})
		}
	}
	m := &chatMessage{ID: randomToken(), Room: room, Source: "system", Created: time.Now().UTC().Format(time.RFC3339), Text: text, Status: "completed", Available: true, Automated: true, EventKind: "alert", Expires: time.Now().Add(30 * time.Minute), recipients: rec, spokenText: text}
	a.chatAddLocked(m)
	a.s.sendChat(m, "chat_message")
	a.s.mu.Unlock()
	if r.TTS && a.s.chat.ttsConfig.Enabled {
		a.s.chat.mu.Lock()
		cfg := a.s.chat.ttsConfig
		a.s.chat.mu.Unlock()
		select {
		case a.s.chat.ttsJobs <- ttsJob{m, cfg}:
		default:
			log.Printf("alert tts queue full")
		}
	}
}
func (a *alertService) record(r alertRule, actual, threshold, image, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, alertRecord{ID: randomToken(), Created: time.Now().UTC().Format(time.RFC3339), Type: r.Type, RoomID: r.RoomID, DeviceID: r.DeviceID, Actual: actual, Threshold: threshold, ImageURL: image, Message: message})
	if len(a.records) > 500 {
		a.records = a.records[len(a.records)-500:]
	}
	_ = a.saveLocked()
}
func (s *server) alertList() []alertRule {
	s.alerts.mu.Lock()
	defer s.alerts.mu.Unlock()
	o := []alertRule{}
	for _, r := range s.alerts.rules {
		o = append(o, r)
	}
	return o
}
func (a *alertService) chatAddLocked(m *chatMessage) {
	a.s.chat.mu.Lock()
	a.s.chat.messages = append(a.s.chat.messages, m)
	a.s.chat.pruneLocked(time.Now())
	a.s.chat.mu.Unlock()
}
