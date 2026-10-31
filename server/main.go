package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxFrame       = 4096
	maxFrames      = 500
	maxBytes       = 512 * 1024
	maxControl     = 1024
	maxConnections = 128
	writeTimeout   = 3 * time.Second
	readyTimeout   = 2 * time.Second
)

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type control struct {
	APIKey          string        `json:"api_key,omitempty"`
	DeviceType      string        `json:"device_type,omitempty"`
	Enabled         *bool         `json:"enabled,omitempty"`
	ChatEnabled     bool          `json:"chat_enabled,omitempty"`
	ChatSessionID   string        `json:"chat_session_id,omitempty"`
	PlaybackToken   string        `json:"playback_token,omitempty"`
	RequestID       string        `json:"request_id,omitempty"`
	Rooms           *[]listedRoom `json:"rooms,omitempty"`
	Type            string        `json:"type"`
	Version         int           `json:"version,omitempty"`
	DeviceID        string        `json:"device_id,omitempty"`
	SourceID        string        `json:"source_id,omitempty"`
	MessageID       string        `json:"message_id,omitempty"`
	FrameMS         int           `json:"frame_ms,omitempty"`
	SampleRate      int           `json:"sample_rate,omitempty"`
	FrameCount      int           `json:"frame_count,omitempty"`
	OK              *bool         `json:"ok,omitempty"`
	Error           string        `json:"error,omitempty"`
	Receivers       int           `json:"receivers,omitempty"`
	RoomID          string        `json:"room_id,omitempty"`
	RoomName        string        `json:"room_name,omitempty"`
	Code            string        `json:"code,omitempty"`
	Name            string        `json:"name,omitempty"`
	DeviceIDs       []string      `json:"device_ids,omitempty"`
	InviteDeviceIDs []string      `json:"invite_device_ids,omitempty"`
	EventID         string        `json:"event_id,omitempty"`
	Content         string        `json:"content,omitempty"`
	ScheduledAt     string        `json:"scheduled_at,omitempty"`
	TargetDeviceID  string        `json:"target_device_id,omitempty"`
	CreatorDeviceID string        `json:"creator_device_id,omitempty"`
	Status          string        `json:"status,omitempty"`
	Events          *[]event      `json:"events,omitempty"`
	Index           int           `json:"index,omitempty"`
}

type listedRoom struct {
	ID      string `json:"room_id"`
	Name    string `json:"room_name"`
	Default bool   `json:"is_default"`
	Current bool   `json:"is_current"`
	CanJoin bool   `json:"can_join"`
	Code    string `json:"code"`
}

func (s *server) listDeviceRooms(id string) []listedRoom {
	current := s.store.CurrentRoom(id).ID
	out := []listedRoom{}
	for _, r := range s.store.ListRooms() {
		if r.Default || len(r.Invited) == 0 || r.CreatedBy == id || contains(r.Invited, id) {
			out = append(out, listedRoom{r.ID, r.Name, r.Default, r.ID == current, true, r.JoinCode})
		}
	}
	return out
}

func answer(t, id string, ok bool, reason string) control {
	return control{Type: t, MessageID: id, OK: &ok, Error: reason}
}

type outgoing struct {
	kind int
	data []byte
}

type peer struct {
	deleted         atomic.Bool
	remoteAddress   string
	deviceType      string
	hosted          atomic.Bool
	hostingEpoch    atomic.Uint64
	hostingBusy     bool
	lastHostedReply time.Time
	automatic       bool
	chatEnabled     bool
	chatToken       string
	chatTokenMu     sync.Mutex
	chatRoom        string
	switching       bool // Guarded by server.mu.
	chatPackets     [][]byte
	chatRecipients  []chatRecipient
	id              string
	conn            *websocket.Conn
	out             chan outgoing
	stop            chan struct{}
	closeOnce       sync.Once
	srv             *server
	mu              sync.Mutex
	receiving       string
	ready           bool
	sending         string
	frames          int
	bytes           int
	recording       *wavRecording
	roomID          string
}

func (p *peer) enqueue(kind int, data []byte) bool {
	select {
	case p.out <- outgoing{kind, append([]byte(nil), data...)}:
		return true
	default:
		return false
	}
}

func (p *peer) send(c control) bool {
	b, _ := json.Marshal(c)
	return p.enqueue(websocket.TextMessage, b)
}

func (p *peer) close() {
	p.closeOnce.Do(func() {
		close(p.stop)
		p.conn.Close()
		p.srv.remove(p)
	})
}

func (p *peer) writer() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg := <-p.out:
			p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := p.conn.WriteMessage(msg.kind, msg.data); err != nil {
				log.Printf("write failed id=%s kind=%d: %v", p.id, msg.kind, err)
				p.close()
				return
			}
		case <-ticker.C:
			p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := p.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				log.Printf("ping failed id=%s: %v", p.id, err)
				p.close()
				return
			}
		case <-p.stop:
			return
		}
	}
}

type server struct {
	usersMu        sync.Mutex
	users          map[string]consoleUser
	publicBaseURL  string
	chat           *chatService
	mu             sync.Mutex
	peers          map[string]*peer
	active         *peer
	started        time.Time
	slots          chan struct{}
	saveWAV        bool
	recordDir      string
	store          roomStore
	events         eventStore
	eventMu        sync.Mutex
	eventReminders map[string]string
	authMu         sync.RWMutex
	authEnabled    bool
	authKey        string
}

func newServer() *server {
	return &server{chat: newChatService(), peers: make(map[string]*peer), slots: make(chan struct{}, maxConnections), store: newMemoryStore(), events: newMemoryEventStore(), eventReminders: map[string]string{}}
}

func (s *server) remove(p *peer) {
	s.mu.Lock()
	if s.peers[p.id] == p {
		delete(s.peers, p.id)
	}
	if s.active == p {
		p.chatPackets = nil
		p.chatRecipients = nil
		s.discardRecording(p)
		s.active = nil
		for _, other := range s.peers {
			other.mu.Lock()
			if other.receiving == p.sending {
				other.send(control{Type: "abort", MessageID: p.sending})
				other.receiving = ""
				other.ready = false
			}
			other.mu.Unlock()
		}
	}
	online := len(s.peers)
	s.mu.Unlock()
	log.Printf("disconnect id=%s online=%d", p.id, online)
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

func (s *server) register(p *peer, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(id) || s.peers[id] != nil || len(s.peers) >= maxConnections {
		return errors.New("invalid, duplicate, or capacity-limited device_id")
	}
	p.id = id
	s.resetChatView(p)
	s.peers[id] = p
	p.roomID = s.store.RegisterDevice(id, p.remoteAddress, p.deviceType).ID
	log.Printf("connect id=%s online=%d", id, len(s.peers))
	return nil
}

func (s *server) syncEvents(p *peer) {
	for _, e := range s.events.ListEvents(p.id) {
		p.send(control{Type: "event_state", EventID: e.ID, Content: e.Content, ScheduledAt: e.ScheduledAt, Status: e.Status})
		if e.Status == "overdue" {
			p.send(control{Type: "event_notification", EventID: e.ID, Content: eventText(e, "事件已逾期"), Status: e.Status})
		}
	}
}

func (s *server) start(p *peer, c control) {
	if c.MessageID == "" || len(c.MessageID) > 64 || c.FrameMS != 60 || c.SampleRate != 16000 {
		log.Printf("start rejected id=%s message=%s reason=invalid_parameters frame_ms=%d sample_rate=%d", p.id, c.MessageID, c.FrameMS, c.SampleRate)
		p.send(answer("start_ack", c.MessageID, false, "invalid start"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active.automatic {
		id := s.active.sending
		for _, receiver := range s.peers {
			receiver.send(control{Type: "abort", MessageID: id})
		}
		s.active = nil
	}
	if s.active != nil {
		log.Printf("start rejected id=%s message=%s reason=busy active_id=%s", p.id, c.MessageID, s.active.id)
		p.send(answer("start_ack", c.MessageID, false, "busy"))
		return
	}
	if p.switching {
		log.Printf("start rejected id=%s message=%s reason=switching_room", p.id, c.MessageID)
		p.send(answer("start_ack", c.MessageID, false, "busy"))
		return
	}
	log.Printf("debug talk accepted sender=%s room=%s message=%s peers=%d", p.id, s.store.CurrentRoom(p.id).ID, c.MessageID, len(s.peers))
	s.active = p
	p.chatRoom = s.store.CurrentRoom(p.id).ID
	p.chatPackets = nil
	p.chatRecipients = []chatRecipient{{p, p.playbackToken()}}
	for _, other := range s.peers {
		if other != p && s.store.CurrentRoom(other.id).ID == p.chatRoom {
			p.chatRecipients = append(p.chatRecipients, chatRecipient{other, other.playbackToken()})
		}
	}
	s.started = time.Now()
	p.sending, p.frames, p.bytes = c.MessageID, 0, 0
	if s.saveWAV {
		if err := s.beginRecording(p); err != nil {
			log.Printf("debug WAV unavailable id=%s message=%s: %v", p.id, c.MessageID, err)
		}
	}
	offered := 0
	for _, other := range s.peers {
		if other == p {
			continue
		}
		if s.store.CurrentRoom(other.id).ID != s.store.CurrentRoom(p.id).ID {
			continue
		}
		other.mu.Lock()
		// Realtime talk starts forwarding immediately. A receiver that cannot
		// accept audio can still revoke the offer with a busy response.
		other.receiving, other.ready = c.MessageID, true
		if !other.send(control{Type: "start", SourceID: p.id, MessageID: c.MessageID, FrameMS: 60, SampleRate: 16000}) {
			log.Printf("receive start queue full id=%s message=%s", other.id, c.MessageID)
			other.receiving = ""
		} else {
			offered++
			log.Printf("debug talk offer sender=%s receiver=%s room=%s message=%s", p.id, other.id, s.store.CurrentRoom(p.id).ID, c.MessageID)
		}
		other.mu.Unlock()
	}
	started := s.started
	if !p.send(answer("start_ack", c.MessageID, true, "")) {
		log.Printf("start ack queue full id=%s message=%s", p.id, c.MessageID)
	}
	time.AfterFunc(35*time.Second, func() {
		s.mu.Lock()
		stalled := s.active == p && s.started.Equal(started)
		s.mu.Unlock()
		if stalled {
			log.Printf("upload timeout id=%s message=%s", p.id, c.MessageID)
			p.close()
		}
	})
	log.Printf("start id=%s message=%s offered=%d ack=immediate", p.id, c.MessageID, offered)
}

func (s *server) frame(p *peer, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != p || len(data) == 0 || len(data) > maxFrame || p.frames >= maxFrames || p.bytes+len(data) > maxBytes || time.Since(s.started) > 35*time.Second {
		return errors.New("invalid or oversized audio frame")
	}
	p.frames++
	p.bytes += len(data)
	p.chatPackets = append(p.chatPackets, append([]byte(nil), data...))
	if p.recording != nil {
		if err := p.recording.appendOpus(data); err != nil {
			log.Printf("debug WAV failed id=%s message=%s: %v", p.id, p.sending, err)
			s.discardRecording(p)
		}
	}
	forwarded := 0
	for _, other := range s.peers {
		other.mu.Lock()
		if other != p && s.store.CurrentRoom(other.id).ID == s.store.CurrentRoom(p.id).ID && other.receiving == p.sending && other.ready {
			if !other.enqueue(websocket.BinaryMessage, data) {
				other.receiving = ""
				other.ready = false
				log.Printf("slow receiver id=%s", other.id)
				go other.close()
			} else {
				forwarded++
			}
		}
		other.mu.Unlock()
	}
	if p.frames == 1 || p.frames%50 == 0 {
		log.Printf("debug audio frame sender=%s room=%s message=%s frame=%d bytes=%d forwarded=%d", p.id, s.store.CurrentRoom(p.id).ID, p.sending, p.frames, p.bytes, forwarded)
	}
	return nil
}

func (s *server) end(p *peer, c control) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != p || p.sending != c.MessageID || c.FrameCount != p.frames {
		return errors.New("invalid end or frame count")
	}
	receivers := 0
	// Room changes clear receiving, so matching it is enough to stay in-room.
	for _, other := range s.peers {
		other.mu.Lock()
		if other != p && other.receiving == c.MessageID {
			if other.ready && other.send(control{Type: "end", MessageID: c.MessageID, FrameCount: p.frames}) {
				receivers++
				log.Printf("receive end queued id=%s message=%s frames=%d", other.id, c.MessageID, p.frames)
			} else {
				log.Printf("receive end skipped id=%s message=%s ready=%t", other.id, c.MessageID, other.ready)
			}
			other.receiving, other.ready = "", false
		}
		other.mu.Unlock()
	}
	s.completeChat(p)
	p.send(control{Type: "end_ack", MessageID: c.MessageID, OK: boolPtr(true), Receivers: receivers})
	if p.recording != nil {
		if err := p.recording.finish(); err != nil {
			log.Printf("debug WAV finalize failed id=%s message=%s: %v", p.id, c.MessageID, err)
			s.discardRecording(p)
		} else {
			log.Printf("debug WAV saved path=%s", p.recording.path)
			p.recording = nil
		}
	}
	log.Printf("end id=%s message=%s frames=%d bytes=%d receivers=%d duration=%s", p.id, c.MessageID, p.frames, p.bytes, receivers, time.Since(s.started).Round(time.Millisecond))
	p.sending = ""
	s.active = nil
	return nil
}

func boolPtr(v bool) *bool { return &v }

func (s *server) abort(p *peer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.abortLocked(p)
}

// Requires server.mu.
func (s *server) abortLocked(p *peer) {
	if s.active != p {
		return
	}
	log.Printf("abort id=%s message=%s frames=%d bytes=%d", p.id, p.sending, p.frames, p.bytes)
	p.chatPackets = nil
	p.chatRecipients = nil
	s.discardRecording(p)
	for _, other := range s.peers {
		other.mu.Lock()
		if other != p && other.receiving == p.sending {
			other.send(control{Type: "abort", MessageID: p.sending})
			other.receiving, other.ready = "", false
		}
		other.mu.Unlock()
	}
	p.sending = ""
	s.active = nil
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ws" {
		http.NotFound(w, r)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "connection limit reached", http.StatusServiceUnavailable)
		return
	}
	conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		// Browsers always send Origin for the built-in simulator. Allow only
		// same-host requests (plus non-browser clients without Origin).
		return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
	}}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	p := &peer{remoteAddress: r.RemoteAddr, conn: conn, out: make(chan outgoing, 16), stop: make(chan struct{}), srv: s}
	defer p.close()
	conn.SetReadLimit(maxFrame + maxControl)
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.TextMessage || len(data) > maxControl {
		return
	}
	var hello control
	if json.Unmarshal(data, &hello) != nil {
		return
	}
	p.deviceType = hello.DeviceType
	if p.deviceType == "" {
		p.deviceType = "physical"
	}
	if p.deviceType != "physical" && p.deviceType != "simulator" {
		conn.WriteJSON(answer("hello_ack", "", false, "invalid device_type"))
		return
	}
	p.chatEnabled = hello.ChatEnabled
	s.authMu.RLock()
	authEnabled, authKey := s.authEnabled, s.authKey
	s.authMu.RUnlock()
	if authEnabled && hello.APIKey != authKey {
		log.Printf("intercom hello rejected device=%s reason=invalid api key", hello.DeviceID)
		conn.WriteJSON(answer("hello_ack", "", false, "invalid intercom api key"))
		// Give the device time to receive the diagnostic ack before closing the
		// unauthenticated connection. This is only a development-mode error path.
		time.Sleep(100 * time.Millisecond)
		return
	}
	if hello.Type != "hello" || hello.Version != 1 || s.register(p, hello.DeviceID) != nil {
		log.Printf("debug ws hello rejected remote=%s", r.RemoteAddr)
		conn.WriteJSON(answer("hello_ack", "", false, "invalid version or device_id"))
		return
	}
	log.Printf("debug ws hello accepted device=%s room=%s remote=%s", p.id, s.store.CurrentRoom(p.id).ID, r.RemoteAddr)
	go p.writer()
	current := s.store.CurrentRoom(p.id)
	ack := answer("hello_ack", "", true, "")
	ack.RoomID, ack.RoomName = current.ID, current.Name
	ack.ChatSessionID, ack.PlaybackToken = p.playbackToken(), p.playbackToken()
	p.send(ack)
	s.syncEvents(p)
	s.roomOnline(p, current)
	for {
		kind, data, err = conn.ReadMessage()
		if err != nil {
			log.Printf("read ended id=%s: %v", p.id, err)
			return
		}
		if p.deleted.Load() {
			return
		}
		if kind == websocket.BinaryMessage {
			log.Printf("debug ws binary device=%s bytes=%d", p.id, len(data))
			if err = s.frame(p, data); err != nil {
				log.Printf("audio frame rejected id=%s bytes=%d: %v", p.id, len(data), err)
				p.send(control{Type: "error", Error: err.Error()})
				s.abort(p)
			}
			continue
		}
		if kind != websocket.TextMessage || len(data) > maxControl {
			return
		}
		var c control
		if json.Unmarshal(data, &c) != nil {
			return
		}
		switch c.Type {
		case "set_hosting":
			s.mu.Lock()
			if c.Enabled == nil || s.active == p {
				p.send(answer("hosting_state", "", false, "invalid request or speaking"))
			} else {
				s.chat.mu.Lock()
				ready := s.chat.hostingConfig.Enabled && s.chat.ttsConfig.Enabled && s.chat.config.Enabled
				s.chat.mu.Unlock()
				if *c.Enabled && !ready {
					p.send(answer("hosting_state", "", false, "enable ASR, LLM and TTS first"))
				} else {
					p.hosted.Store(*c.Enabled)
					p.hostingEpoch.Add(1)
					p.send(control{Type: "hosting_state", OK: boolPtr(true), Enabled: boolPtr(p.hosted.Load())})
				}
			}
			s.mu.Unlock()

		case "list_rooms":
			if c.RequestID == "" || len(c.RequestID) > 64 {
				p.send(control{Type: "list_rooms_ack", RequestID: c.RequestID, OK: boolPtr(false), Error: "invalid request_id"})
				break
			}
			rooms := s.listDeviceRooms(p.id)
			response := control{Type: "list_rooms_ack", RequestID: c.RequestID, OK: boolPtr(true), Rooms: &rooms}
			encoded, _ := json.Marshal(response)
			if len(encoded) > 8192 {
				response = control{Type: "list_rooms_ack", RequestID: c.RequestID, OK: boolPtr(false), Error: "room list exceeds 8192 bytes"}
			}
			p.send(response)
			log.Printf("debug list rooms device=%s request=%s count=%d", p.id, c.RequestID, len(rooms))
		case "event_create":
			e, err := s.events.CreateEvent(c.TargetDeviceID, p.id, c.Content, c.ScheduledAt)
			if err != nil {
				p.send(control{Type: "event_create_ack", RequestID: c.RequestID, OK: boolPtr(false), Error: err.Error()})
				break
			}
			p.send(control{Type: "event_create_ack", RequestID: c.RequestID, OK: boolPtr(true), EventID: e.ID, TargetDeviceID: e.TargetDeviceID, CreatorDeviceID: e.CreatorDeviceID, Content: e.Content, ScheduledAt: e.ScheduledAt, Status: e.Status})
			if target := s.eventPeer(e.TargetDeviceID); target != nil {
				target.send(control{Type: "event_state", EventID: e.ID, Content: e.Content, ScheduledAt: e.ScheduledAt, Status: e.Status})
				s.notifyEvent(e, e.TargetDeviceID, "created", fmt.Sprintf("收到新事件：%s", e.Content))
			}
		case "event_list":
			es := s.events.ListEvents(p.id)
			p.send(control{Type: "event_list_ack", RequestID: c.RequestID, OK: boolPtr(true), Events: &es})
		case "event_complete", "event_cancel":
			status := "completed"
			if c.Type == "event_cancel" {
				status = "cancelled"
			}
			e, err := s.events.SetEventStatus(c.EventID, p.id, status)
			if err != nil {
				p.send(control{Type: c.Type + "_ack", RequestID: c.RequestID, OK: boolPtr(false), Error: err.Error()})
				break
			}
			p.send(control{Type: c.Type + "_ack", RequestID: c.RequestID, OK: boolPtr(true), EventID: e.ID, Status: e.Status})
			s.notifyEvent(e, e.CreatorDeviceID, status, eventText(e, "事件已"+map[string]string{"completed": "完成", "cancelled": "取消"}[status]))
			if e.CreatorDeviceID != p.id {
				s.notifyEvent(e, p.id, status, eventText(e, "事件已"+map[string]string{"completed": "完成", "cancelled": "取消"}[status]))
			}
		case "event_execute_ack":
			if c.EventID != "" {
				p.send(control{Type: "event_execute_ack", EventID: c.EventID, OK: boolPtr(true)})
			}
		case "start":
			log.Printf("debug ws control device=%s type=start message=%s room=%s", p.id, c.MessageID, s.store.CurrentRoom(p.id).ID)
			s.start(p, c)
		case "join_room":
			log.Printf("debug ws control device=%s type=join_room code=%s", p.id, c.Code)
			oldRoom := s.store.CurrentRoom(p.id).ID
			r, err := s.changeRoom(p.id, func() (room, error) { return s.store.Join(p.id, c.Code) })
			if err != nil {
				log.Printf("debug room join failed device=%s code=%s error=%v", p.id, c.Code, err)
				p.send(answer("join_room_ack", "", false, err.Error()))
			} else {
				log.Printf("debug room joined device=%s room=%s name=%s", p.id, r.ID, r.Name)
				p.send(control{Type: "join_room_ack", ChatSessionID: p.playbackToken(), PlaybackToken: p.playbackToken(), OK: boolPtr(true), RoomID: r.ID, RoomName: r.Name})
				s.roomJoined(p, r, oldRoom)
			}
		case "create_room":
			invited := c.InviteDeviceIDs
			if invited == nil {
				invited = c.DeviceIDs
			}
			log.Printf("debug ws control device=%s type=create_room name=%q invite_count=%d", p.id, c.Name, len(invited))
			r, err := s.changeRoom(p.id, func() (room, error) { return s.store.CreateRoom(c.Name, p.id, invited) })
			if err != nil {
				log.Printf("debug room create failed device=%s name=%q error=%v", p.id, c.Name, err)
				p.send(answer("create_room_ack", "", false, err.Error()))
			} else {
				log.Printf("debug room created device=%s room=%s name=%s code=%s devices=%v", p.id, r.ID, r.Name, r.JoinCode, r.Devices)
				p.send(control{Type: "create_room_ack", ChatSessionID: p.playbackToken(), PlaybackToken: p.playbackToken(), OK: boolPtr(true), RoomID: r.ID, RoomName: r.Name, Code: r.JoinCode, DeviceIDs: r.Devices, InviteDeviceIDs: r.Invited})
				s.roomInvitations(r)
				s.roomJoined(p, r, "")
				// Notify devices of their confirmed room.
				s.mu.Lock()
				for _, other := range s.peers {
					if s.store.CurrentRoom(other.id).ID == r.ID {
						other.send(control{Type: "room_state", RoomID: r.ID, RoomName: r.Name})
					}
				}
				s.mu.Unlock()
			}
		case "accept_room_invite":
			oldRoom := s.store.CurrentRoom(p.id).ID
			r, err := s.changeRoom(p.id, func() (room, error) { return s.store.Join(p.id, c.Code) })
			if err != nil {
				p.send(answer("accept_room_invite_ack", "", false, err.Error()))
			} else {
				p.send(control{Type: "accept_room_invite_ack", OK: boolPtr(true), RoomID: r.ID, RoomName: r.Name})
				p.send(control{Type: "room_state", ChatSessionID: p.playbackToken(), PlaybackToken: p.playbackToken(), RoomID: r.ID, RoomName: r.Name})
				s.roomJoined(p, r, oldRoom)
			}
		case "leave_room":
			log.Printf("debug ws control device=%s type=leave_room room=%s", p.id, s.store.CurrentRoom(p.id).ID)
			oldRoom := s.store.CurrentRoom(p.id).ID
			r, err := s.changeRoom(p.id, func() (room, error) { return s.store.Leave(p.id), nil })
			if err != nil {
				p.send(control{Type: "room_state", OK: boolPtr(false), Error: err.Error()})
				break
			}
			log.Printf("debug room left device=%s room=%s", p.id, r.ID)
			p.send(control{Type: "room_state", ChatSessionID: p.playbackToken(), PlaybackToken: p.playbackToken(), RoomID: r.ID, RoomName: r.Name})
			s.roomJoined(p, r, oldRoom)
		case "receive_ready", "busy":
			log.Printf("debug ws control device=%s type=%s message=%s", p.id, c.Type, c.MessageID)
			s.mu.Lock()
			withinReadyWindow := time.Since(s.started) <= readyTimeout
			elapsed := time.Since(s.started).Round(time.Millisecond)
			s.mu.Unlock()
			p.mu.Lock()
			if p.receiving == c.MessageID {
				p.ready = c.Type == "receive_ready" && withinReadyWindow
				log.Printf("receiver response id=%s message=%s type=%s accepted=%t elapsed=%s", p.id, c.MessageID, c.Type, p.ready, elapsed)
				if !p.ready {
					p.receiving = ""
				}
			} else {
				log.Printf("receiver response ignored id=%s message=%s type=%s pending=%s", p.id, c.MessageID, c.Type, p.receiving)
			}
			p.mu.Unlock()
		case "end":
			log.Printf("debug ws control device=%s type=end message=%s frames=%d", p.id, c.MessageID, c.FrameCount)
			if err := s.end(p, c); err != nil {
				log.Printf("end rejected id=%s message=%s frames=%d: %v", p.id, c.MessageID, c.FrameCount, err)
				p.send(control{Type: "error", Error: err.Error()})
				s.abort(p)
			}
		case "abort":
			log.Printf("debug ws control device=%s type=abort message=%s", p.id, c.MessageID)
			s.abort(p)
		default:
			log.Printf("debug ws control device=%s type=%s unknown", p.id, c.Type)
			p.send(control{Type: "error", Error: "unknown message type"})
			s.abort(p)
		}
	}
}

func main() {
	addr := flag.String("listen", "127.0.0.1:8080", "listen address")
	saveWAV := flag.Bool("save-wav", true, "save complete uploads as debug WAV files")
	recordDir := flag.String("record-dir", "recordings", "debug WAV directory")
	dbPath := flag.String("db", "", "SQLite database path (optional)")
	publicBaseURL := flag.String("public-base-url", "", "public HTTP(S) base URL for playback links, e.g. http://111.62.241.117:9200")
	flag.Parse()
	s := newServer()
	s.publicBaseURL = strings.TrimRight(strings.TrimSpace(*publicBaseURL), "/")
	if s.publicBaseURL != "" {
		u, err := url.Parse(s.publicBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			log.Fatal("invalid -public-base-url: expected HTTP(S) base URL without credentials, query or fragment")
		}
	}
	if strings.TrimSpace(*dbPath) != "" {
		if db, err := newSQLiteStore(*dbPath); err == nil {
			s.store = db
			s.events = db
		} else {
			log.Printf("sqlite unavailable, using memory store: %v", err)
		}
	}
	s.saveWAV = *saveWAV
	if *saveWAV {
		absoluteDir, err := filepath.Abs(*recordDir)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(absoluteDir, 0700); err != nil {
			log.Fatal(err)
		}
		s.recordDir = absoluteDir
		log.Printf("debug WAV enabled dir=%s", absoluteDir)
	}
	log.Printf("intercom listening on %s", *addr)
	s.loadASRConfig()
	s.loadTTSConfig()
	s.loadHostingConfig()
	s.loadIntercomAuthConfig()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.chat.run(ctx, s)
	go s.chat.runTTS(ctx, s)
	go s.chat.runHosting(ctx, s)
	go s.runEvents()
	log.Fatal(http.ListenAndServe(*addr, httpHandler(s)))
}

func httpHandler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handle)
	api := &apiServer{s: s, sessions: map[string]time.Time{}}
	mux.HandleFunc("/api/", api.handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.ServeFile(w, r, "web/index.html")
			return
		}
		http.NotFound(w, r)
	})
	return mux
}
