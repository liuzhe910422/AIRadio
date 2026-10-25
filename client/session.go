package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const maxFrameBytes = 4096

var validDeviceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

const (
	voiceIdle = iota
	voiceStarting
	voiceAccepted
)

// ChatLine 是一条聊天文字，更新时按 MessageID 原位替换。
type ChatLine struct {
	MessageID string
	SourceID  string
	Text      string
}

// ForwardLine 是一次语音转发在结束后的展示。
type ForwardLine struct {
	MessageID string
	SourceID  string
	Text      string
	Received  int
	Declared  int
}

// Snapshot 是会话的可观察状态。
type Snapshot struct {
	Registered    bool
	ChatSessionID string
	Voice         int
	Frames        int
	Chats         []ChatLine
	Forwards      []ForwardLine
	Status        []string
	Incoming      bool
}

// Event 通知界面刷新。Kind 为 status、replace 或 unregistered。
type Event struct {
	Kind string
}

type incomingVoice struct {
	messageID string
	sourceID  string
	frames    [][]byte
}

// Session 保存连接、语音会话和消息区。读循环把结果放进状态，测试与界面都只观察快照。
type Session struct {
	mu         sync.Mutex
	writeMu    sync.Mutex
	conn       *websocket.Conn
	generation uint64
	deviceID   string
	registered bool
	viewID     string

	voice   int
	voiceID string
	seq     int
	frames  int

	chats    []ChatLine
	forwards []ForwardLine
	incoming *incomingVoice
	status   []string

	events chan Event
}

func NewSession() *Session {
	return &Session{events: make(chan Event, 32)}
}

func (s *Session) Events() <-chan Event { return s.events }

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Session) snapshotLocked() Snapshot {
	snap := Snapshot{
		Registered:    s.registered,
		ChatSessionID: s.viewID,
		Voice:         s.voice,
		Frames:        s.frames,
		Chats:         append([]ChatLine(nil), s.chats...),
		Forwards:      append([]ForwardLine(nil), s.forwards...),
		Status:        append([]string(nil), s.status...),
		Incoming:      s.incoming != nil,
	}
	return snap
}

func (s *Session) Execute(line string) {
	cmd := ParseCommand(line)
	switch cmd.Name {
	case "":
		return
	case "connect":
		if len(cmd.Args) != 2 && len(cmd.Args) != 3 {
			s.addStatus("用法：connect <url> <device_id> [api_key]")
			return
		}
		key := ""
		if len(cmd.Args) == 3 {
			key = cmd.Args[2]
		}
		s.connect(cmd.Args[0], cmd.Args[1], key)
	case "join":
		if len(cmd.Args) != 1 {
			s.addStatus("用法：join <code>")
			return
		}
		s.join(cmd.Args[0])
	case "sendstart":
		s.sendStart()
	case "sendtext":
		s.sendText(cmd.Text)
	case "sendend":
		s.sendEnd()
	default:
		s.addStatus("未知命令")
	}
}

func (s *Session) connect(url, id, key string) {
	if !validDeviceID.MatchString(id) {
		s.addStatus("设备 ID 非法")
		return
	}
	s.mu.Lock()
	s.generation++
	gen := s.generation
	old := s.conn
	s.conn = nil
	s.registered = false
	s.deviceID = id
	s.voice = voiceIdle
	s.voiceID = ""
	s.frames = 0
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		s.addStatus("连接失败：" + err.Error())
		return
	}
	conn.SetReadLimit(64 * 1024)
	s.mu.Lock()
	if s.generation != gen {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	s.conn = conn
	s.mu.Unlock()
	hello := map[string]any{
		"type":         "hello",
		"version":      1,
		"device_id":    id,
		"device_type":  "simulator",
		"chat_enabled": true,
	}
	if key != "" {
		hello["api_key"] = key
	}
	if err := s.writeConn(conn, gen, websocket.TextMessage, mustJSON(hello)); err != nil {
		s.dropConn(gen)
		s.addStatus("连接失败：" + err.Error())
		return
	}
	go s.readLoop(conn, gen)
}

func (s *Session) join(code string) {
	if !s.isRegistered() {
		s.addStatus("请先连接")
		return
	}
	body := map[string]any{"type": "join_room", "code": code}
	if err := s.writeCurrent(websocket.TextMessage, mustJSON(body)); err != nil {
		s.addStatus("发送失败")
	}
}

func (s *Session) sendStart() {
	s.mu.Lock()
	if !s.registered {
		s.mu.Unlock()
		s.addStatus("请先连接")
		return
	}
	if s.voice != voiceIdle {
		s.mu.Unlock()
		s.addStatus("当前语音会话尚未结束")
		return
	}
	s.seq++
	id := fmt.Sprintf("%s-%d", s.deviceID, s.seq)
	s.voice = voiceStarting
	s.voiceID = id
	s.frames = 0
	s.mu.Unlock()
	body := map[string]any{
		"type":        "start",
		"message_id":  id,
		"frame_ms":    60,
		"sample_rate": 16000,
	}
	if err := s.writeCurrent(websocket.TextMessage, mustJSON(body)); err != nil {
		s.mu.Lock()
		if s.voiceID == id {
			s.voice = voiceIdle
		}
		s.mu.Unlock()
		s.addStatus("发送失败")
		return
	}
	s.addStatus("已发起语音会话 " + id)
}

func (s *Session) sendText(text string) {
	if text == "" {
		s.addStatus("缺少文本")
		return
	}
	s.mu.Lock()
	if !s.registered {
		s.mu.Unlock()
		s.addStatus("请先连接")
		return
	}
	if s.voice != voiceAccepted {
		s.mu.Unlock()
		s.addStatus("语音会话尚未接受")
		return
	}
	id := s.voiceID
	s.mu.Unlock()
	for _, frame := range splitFrames(text) {
		if err := s.writeCurrent(websocket.BinaryMessage, frame); err != nil {
			s.abortVoice(id)
			s.addStatus("发送失败")
			return
		}
		s.mu.Lock()
		if s.voice == voiceAccepted && s.voiceID == id {
			s.frames++
		}
		n := s.frames
		s.mu.Unlock()
		s.addStatus(fmt.Sprintf("已发送，当前帧数 %d", n))
	}
}

func (s *Session) sendEnd() {
	s.mu.Lock()
	if !s.registered {
		s.mu.Unlock()
		s.addStatus("请先连接")
		return
	}
	if s.voice != voiceAccepted {
		s.mu.Unlock()
		s.addStatus("语音会话尚未接受")
		return
	}
	id := s.voiceID
	n := s.frames
	s.voice = voiceIdle
	s.voiceID = ""
	s.mu.Unlock()
	body := map[string]any{"type": "end", "message_id": id, "frame_count": n}
	if err := s.writeCurrent(websocket.TextMessage, mustJSON(body)); err != nil {
		s.abortVoice(id)
		s.addStatus("发送失败")
		return
	}
	s.addStatus(fmt.Sprintf("语音会话结束，帧数 %d", n))
}

func (s *Session) abortVoice(id string) {
	_ = s.writeCurrent(websocket.TextMessage, mustJSON(map[string]any{"type": "abort", "message_id": id}))
	s.mu.Lock()
	if s.voiceID == id || s.voiceID == "" {
		s.voice = voiceIdle
		s.voiceID = ""
	}
	s.mu.Unlock()
}

func (s *Session) readLoop(conn *websocket.Conn, gen uint64) {
	defer func() {
		s.mu.Lock()
		current := s.generation == gen && s.conn == conn
		if current {
			s.conn = nil
			s.registered = false
			s.voice = voiceIdle
			s.voiceID = ""
			s.frames = 0
			s.chats = nil
			s.forwards = nil
			s.incoming = nil
			s.status = []string{"已断开"}
		}
		s.mu.Unlock()
		_ = conn.Close()
		if current {
			s.emit(Event{Kind: "unregistered"})
		}
	}()
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.generation != gen || s.conn != conn {
			s.mu.Unlock()
			return
		}
		if kind == websocket.BinaryMessage {
			s.onBinaryLocked(data)
			s.mu.Unlock()
			continue
		}
		if kind == websocket.TextMessage {
			s.onTextLocked(data)
		}
		s.mu.Unlock()
	}
}

func (s *Session) onBinaryLocked(data []byte) {
	if s.incoming == nil {
		return
	}
	s.incoming.frames = append(s.incoming.frames, append([]byte(nil), data...))
	s.emit(Event{Kind: "replace"})
}

func (s *Session) onTextLocked(data []byte) {
	var msg struct {
		Type          string `json:"type"`
		OK            *bool  `json:"ok"`
		Error         string `json:"error"`
		MessageID     string `json:"message_id"`
		SourceID      string `json:"source_id"`
		FrameCount    int    `json:"frame_count"`
		Receivers     int    `json:"receivers"`
		RoomName      string `json:"room_name"`
		ChatSessionID string `json:"chat_session_id"`
		Text          string `json:"text"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "hello_ack":
		if msg.OK != nil && *msg.OK {
			s.registered = true
			s.switchView(msg.ChatSessionID)
			s.status = append(s.status, "注册成功："+msg.RoomName)
			s.emit(Event{Kind: "status"})
			return
		}
		s.registered = false
		reason := msg.Error
		if reason == "" {
			reason = "注册失败"
		}
		s.status = append(s.status, "注册失败："+reason)
		s.emit(Event{Kind: "unregistered"})
	case "join_room_ack":
		if msg.OK != nil && *msg.OK {
			s.switchView(msg.ChatSessionID)
			s.status = append(s.status, "已加入："+msg.RoomName)
			s.emit(Event{Kind: "status"})
			return
		}
		s.status = append(s.status, "加入失败："+msg.Error)
		s.emit(Event{Kind: "status"})
	case "room_state":
		if msg.ChatSessionID != "" && msg.ChatSessionID != s.viewID {
			s.switchView(msg.ChatSessionID)
			s.emit(Event{Kind: "replace"})
		}
	case "start_ack":
		if msg.MessageID != s.voiceID || s.voice != voiceStarting {
			return
		}
		if msg.OK != nil && *msg.OK {
			s.voice = voiceAccepted
			s.frames = 0
			s.status = append(s.status, "语音会话已接受")
		} else {
			s.voice = voiceIdle
			s.voiceID = ""
			reason := msg.Error
			if reason == "" {
				reason = "被拒绝"
			}
			s.status = append(s.status, "语音会话被拒绝："+reason)
		}
		s.emit(Event{Kind: "status"})
	case "end_ack":
		s.status = append(s.status, fmt.Sprintf("结束确认，接收端 %d", msg.Receivers))
		s.emit(Event{Kind: "status"})
	case "start":
		s.incoming = &incomingVoice{messageID: msg.MessageID, sourceID: msg.SourceID}
		s.emit(Event{Kind: "replace"})
	case "end":
		if s.incoming == nil || s.incoming.messageID != msg.MessageID {
			return
		}
		in := s.incoming
		s.incoming = nil
		s.forwards = append(s.forwards, ForwardLine{
			MessageID: in.messageID,
			SourceID:  in.sourceID,
			Text:      decodeVoice(in.frames),
			Received:  len(in.frames),
			Declared:  msg.FrameCount,
		})
		s.emit(Event{Kind: "replace"})
	case "abort":
		if s.incoming != nil && s.incoming.messageID == msg.MessageID {
			s.incoming = nil
			s.status = append(s.status, "会话已中止 "+msg.MessageID)
			s.emit(Event{Kind: "status"})
		}
		if s.voiceID == msg.MessageID && s.voice != voiceIdle {
			s.voice = voiceIdle
			s.voiceID = ""
			s.emit(Event{Kind: "status"})
		}
	case "error":
		s.status = append(s.status, msg.Error)
		if s.voice != voiceIdle {
			s.voice = voiceIdle
			s.voiceID = ""
		}
		s.emit(Event{Kind: "status"})
	case "chat_message", "chat_message_update":
		if msg.ChatSessionID != s.viewID {
			return
		}
		updated := false
		for i := range s.chats {
			if s.chats[i].MessageID == msg.MessageID {
				s.chats[i].SourceID = msg.SourceID
				s.chats[i].Text = msg.Text
				updated = true
				break
			}
		}
		if !updated {
			s.chats = append(s.chats, ChatLine{MessageID: msg.MessageID, SourceID: msg.SourceID, Text: msg.Text})
		}
		s.emit(Event{Kind: "replace"})
	}
}

func (s *Session) switchView(id string) {
	s.viewID = id
	s.chats = nil
	s.forwards = nil
	s.incoming = nil
	s.voice = voiceIdle
	s.voiceID = ""
	s.frames = 0
}

func (s *Session) isRegistered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registered
}

func (s *Session) addStatus(text string) {
	s.mu.Lock()
	s.status = append(s.status, text)
	s.mu.Unlock()
	s.emit(Event{Kind: "status"})
}

func (s *Session) emit(ev Event) {
	select {
	case s.events <- ev:
	default:
	}
}

func (s *Session) writeCurrent(kind int, data []byte) error {
	s.mu.Lock()
	conn := s.conn
	gen := s.generation
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("未连接")
	}
	return s.writeConn(conn, gen, kind, data)
}

func (s *Session) writeConn(conn *websocket.Conn, gen uint64, kind int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	current := s.generation == gen && s.conn == conn
	s.mu.Unlock()
	if !current {
		return fmt.Errorf("连接已更换")
	}
	return conn.WriteMessage(kind, data)
}

func (s *Session) dropConn(gen uint64) {
	s.mu.Lock()
	conn := s.conn
	if s.generation == gen {
		s.conn = nil
	} else {
		conn = nil
	}
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func splitFrames(text string) [][]byte {
	b := []byte(text)
	var out [][]byte
	for len(b) > 0 {
		n := maxFrameBytes
		if n > len(b) {
			n = len(b)
		} else {
			for n > 0 && !utf8.RuneStart(b[n]) {
				n--
			}
			if n == 0 {
				n = maxFrameBytes
				if n > len(b) {
					n = len(b)
				}
			}
		}
		out = append(out, append([]byte(nil), b[:n]...))
		b = b[n:]
	}
	return out
}

func decodeVoice(frames [][]byte) string {
	var all []byte
	for _, f := range frames {
		all = append(all, f...)
	}
	if utf8.Valid(all) {
		return string(all)
	}
	return fmt.Sprintf("（无法按 UTF-8 解码，%d 字节）", len(all))
}

// Render 把快照画成消息区文本，供界面使用。
func Render(snap Snapshot) string {
	var b strings.Builder
	for _, line := range snap.Status {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, chat := range snap.Chats {
		b.WriteString(chat.SourceID)
		b.WriteByte(' ')
		b.WriteString(chat.Text)
		b.WriteByte('\n')
	}
	for _, fw := range snap.Forwards {
		fmt.Fprintf(&b, "[转发] %s\n%s\n[结束] 帧数 %d/%d\n", fw.SourceID, fw.Text, fw.Received, fw.Declared)
	}
	if b.Len() == 0 {
		return "就绪"
	}
	return b.String()
}
