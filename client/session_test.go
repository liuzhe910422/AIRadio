package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type wsMsg struct {
	kind int
	data []byte
}

type side struct {
	conn   *websocket.Conn
	in     chan wsMsg
	closed chan struct{}
}

func startServer(t *testing.T) (string, func() *side) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *side, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sd := &side{conn: conn, in: make(chan wsMsg, 16), closed: make(chan struct{})}
		go func() {
			defer close(sd.closed)
			for {
				kind, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				sd.in <- wsMsg{kind, append([]byte(nil), data...)}
			}
		}()
		accepted <- sd
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	return url, func() *side {
		t.Helper()
		select {
		case sd := <-accepted:
			return sd
		case <-time.After(2 * time.Second):
			t.Fatal("没有等到连接")
			return nil
		}
	}
}

func (sd *side) next(t *testing.T) wsMsg {
	t.Helper()
	select {
	case msg := <-sd.in:
		return msg
	case <-sd.closed:
		t.Fatal("连接已关闭")
	case <-time.After(2 * time.Second):
		t.Fatal("等待消息超时")
	}
	return wsMsg{}
}

func (sd *side) none(t *testing.T) {
	t.Helper()
	select {
	case msg := <-sd.in:
		t.Fatalf("不应收到消息: %s", msg.data)
	case <-time.After(120 * time.Millisecond):
	}
}

func writeJSON(t *testing.T, sd *side, v any) {
	t.Helper()
	if err := sd.conn.WriteJSON(v); err != nil {
		t.Fatal(err)
	}
}

func asMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		t.Fatalf("无效 JSON: %s", data)
	}
	return m
}

func waitSnap(t *testing.T, s *Session, ok func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last Snapshot
	for time.Now().Before(deadline) {
		last = s.Snapshot()
		if ok(last) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("状态超时: %+v", last)
	return last
}

func TestConnectHello(t *testing.T) {
	url, accept := startServer(t)
	s := NewSession()

	s.Execute("connect " + url + " bad!id")
	if snap := s.Snapshot(); len(snap.Status) == 0 || !strings.Contains(snap.Status[len(snap.Status)-1], "设备 ID 非法") {
		t.Fatalf("非法设备 ID: %+v", snap.Status)
	}

	var hits atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer blocked.Close()
	s.Execute("connect ws" + strings.TrimPrefix(blocked.URL, "http") + " bad!id")
	if hits.Load() != 0 {
		t.Fatal("非法设备 ID 不应拨号")
	}

	s.Execute("connect " + url + " device-1")
	sd := accept()
	hello := asMap(t, sd.next(t).data)
	if hello["type"] != "hello" || hello["version"] != float64(1) || hello["device_id"] != "device-1" || hello["device_type"] != "simulator" || hello["chat_enabled"] != true {
		t.Fatalf("hello: %#v", hello)
	}
	if _, ok := hello["api_key"]; ok {
		t.Fatal("未提供 api_key 时不应写入该字段")
	}
	writeJSON(t, sd, map[string]any{"type": "hello_ack", "ok": false, "error": "拒绝"})
	snap := waitSnap(t, s, func(sn Snapshot) bool {
		return len(sn.Status) > 0 && strings.Contains(sn.Status[len(sn.Status)-1], "注册失败：拒绝")
	})
	if snap.Registered {
		t.Fatal("失败后仍注册")
	}
	s.Execute("join 1234")
	sd.none(t)

	s.Execute("connect " + url + " device-2 secret")
	select {
	case <-sd.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("重复连接没有关闭旧连接")
	}
	sd = accept()
	hello = asMap(t, sd.next(t).data)
	if hello["device_id"] != "device-2" || hello["api_key"] != "secret" {
		t.Fatalf("第二次 hello: %#v", hello)
	}
	writeJSON(t, sd, map[string]any{"type": "hello_ack", "ok": true, "room_name": "默认房间", "chat_session_id": "view-1"})
	snap = waitSnap(t, s, func(sn Snapshot) bool { return sn.Registered && sn.ChatSessionID == "view-1" })
	if !strings.Contains(strings.Join(snap.Status, "\n"), "注册成功：默认房间") {
		t.Fatalf("注册成功状态: %+v", snap.Status)
	}

	s.Execute("connect ws://127.0.0.1:1/ws device-3")
	snap = waitSnap(t, s, func(sn Snapshot) bool {
		return len(sn.Status) > 0 && strings.Contains(sn.Status[len(sn.Status)-1], "连接失败")
	})
	if snap.Registered {
		t.Fatal("连接失败后仍注册")
	}
}

func TestJoin(t *testing.T) {
	s := NewSession()
	s.Execute("join 1234")
	snap := s.Snapshot()
	if snap.Registered || !strings.Contains(strings.Join(snap.Status, "\n"), "请先连接") {
		t.Fatalf("未注册 join: %+v", snap)
	}

	url, accept := startServer(t)
	s = NewSession()
	s.Execute("connect " + url + " device-1")
	sd := accept()
	_ = sd.next(t)
	writeJSON(t, sd, map[string]any{"type": "hello_ack", "ok": true, "room_name": "旧房间", "chat_session_id": "view-1"})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Registered })
	writeJSON(t, sd, map[string]any{"type": "chat_message", "message_id": "m1", "source_id": "a", "text": "原来", "chat_session_id": "view-1"})
	waitSnap(t, s, func(sn Snapshot) bool { return len(sn.Chats) == 1 })

	s.Execute("join 9999")
	got := asMap(t, sd.next(t).data)
	if got["type"] != "join_room" || got["code"] != "9999" {
		t.Fatalf("join: %#v", got)
	}
	writeJSON(t, sd, map[string]any{"type": "join_room_ack", "ok": false, "error": "无效加入码"})
	snap = waitSnap(t, s, func(sn Snapshot) bool {
		return strings.Contains(strings.Join(sn.Status, "\n"), "加入失败：无效加入码")
	})
	if snap.ChatSessionID != "view-1" || len(snap.Chats) != 1 || snap.Chats[0].Text != "原来" {
		t.Fatalf("失败后视图被改变: %+v", snap)
	}

	s.Execute("join 1234")
	_ = sd.next(t)
	writeJSON(t, sd, map[string]any{"type": "join_room_ack", "ok": true, "room_name": "新房间", "chat_session_id": "view-2"})
	snap = waitSnap(t, s, func(sn Snapshot) bool { return sn.ChatSessionID == "view-2" })
	if len(snap.Chats) != 0 || !strings.Contains(strings.Join(snap.Status, "\n"), "已加入：新房间") {
		t.Fatalf("加入成功: %+v", snap)
	}
}

func TestChatAndRoomState(t *testing.T) {
	url, accept := startServer(t)
	s := NewSession()
	s.Execute("connect " + url + " device-1")
	sd := accept()
	_ = sd.next(t)
	writeJSON(t, sd, map[string]any{"type": "hello_ack", "ok": true, "room_name": "房间", "chat_session_id": "view-1"})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.ChatSessionID == "view-1" })

	writeJSON(t, sd, map[string]any{"type": "start", "message_id": "b-1", "source_id": "b", "frame_ms": 60, "sample_rate": 16000})
	writeJSON(t, sd, map[string]any{"type": "end", "message_id": "b-1", "frame_count": 0})
	waitSnap(t, s, func(sn Snapshot) bool { return len(sn.Forwards) == 1 })

	cases := []map[string]any{
		{"type": "chat_message", "message_id": "m1", "source_id": "a", "text": "一", "chat_session_id": "view-1"},
		{"type": "chat_message_update", "message_id": "m1", "source_id": "a", "text": "二", "chat_session_id": "view-1"},
		{"type": "chat_message", "message_id": "old", "source_id": "z", "text": "忽略", "chat_session_id": "view-0"},
		{"type": "chat_message", "message_id": "m2", "source_id": "b", "text": "三", "chat_session_id": "view-1"},
	}
	for _, c := range cases {
		writeJSON(t, sd, c)
	}
	snap := waitSnap(t, s, func(sn Snapshot) bool { return len(sn.Chats) == 2 && sn.Chats[0].Text == "二" })
	if snap.Chats[1].SourceID != "b" || snap.Chats[1].Text != "三" || len(snap.Forwards) != 1 {
		t.Fatalf("聊天文字: %+v", snap)
	}

	writeJSON(t, sd, map[string]any{"type": "room_state", "chat_session_id": "view-1", "room_name": "房间"})
	time.Sleep(50 * time.Millisecond)
	snap = s.Snapshot()
	if len(snap.Chats) != 2 || snap.ChatSessionID != "view-1" {
		t.Fatalf("相同视图不应清空: %+v", snap)
	}

	writeJSON(t, sd, map[string]any{"type": "start", "message_id": "c-1", "source_id": "c"})
	if err := sd.conn.WriteMessage(websocket.BinaryMessage, []byte("未完成")); err != nil {
		t.Fatal(err)
	}
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Incoming })
	sd.none(t)
	writeJSON(t, sd, map[string]any{"type": "room_state", "chat_session_id": "view-2"})
	writeJSON(t, sd, map[string]any{"type": "end", "message_id": "c-1", "frame_count": 1})
	snap = waitSnap(t, s, func(sn Snapshot) bool { return sn.ChatSessionID == "view-2" && !sn.Incoming })
	if len(snap.Chats) != 0 || len(snap.Forwards) != 0 || snap.Voice != voiceIdle {
		t.Fatalf("新视图: %+v", snap)
	}
}

func TestVoiceSend(t *testing.T) {
	s := NewSession()
	s.Execute("sendstart")
	s.Execute("sendtext 集合")
	s.Execute("sendend")
	s.Execute("sendtext")
	joined := strings.Join(s.Snapshot().Status, "\n")
	if !strings.Contains(joined, "请先连接") || !strings.Contains(joined, "缺少文本") {
		t.Fatalf("未注册发送: %s", joined)
	}

	url, accept := startServer(t)
	s = NewSession()
	s.Execute("connect " + url + " device-1")
	sd := accept()
	_ = sd.next(t)
	writeJSON(t, sd, map[string]any{"type": "hello_ack", "ok": true, "room_name": "房间", "chat_session_id": "view-1"})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Registered })

	s.Execute("sendtext 早了")
	s.Execute("sendend")
	sd.none(t)

	s.Execute("sendstart")
	start := asMap(t, sd.next(t).data)
	if start["type"] != "start" || start["message_id"] != "device-1-1" || start["frame_ms"] != float64(60) || start["sample_rate"] != float64(16000) {
		t.Fatalf("start: %#v", start)
	}
	s.Execute("sendstart")
	s.Execute("sendtext 还没接受")
	sd.none(t)
	writeJSON(t, sd, map[string]any{"type": "start_ack", "ok": false, "error": "busy", "message_id": "device-1-1"})
	waitSnap(t, s, func(sn Snapshot) bool {
		return sn.Voice == voiceIdle && strings.Contains(strings.Join(sn.Status, "\n"), "语音会话被拒绝：busy")
	})

	s.Execute("sendstart")
	start = asMap(t, sd.next(t).data)
	if start["message_id"] != "device-1-2" {
		t.Fatalf("第二次 start: %#v", start)
	}
	writeJSON(t, sd, map[string]any{"type": "start_ack", "ok": true, "message_id": "device-1-2"})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Voice == voiceAccepted })
	s.Execute("sendend")
	end := asMap(t, sd.next(t).data)
	if end["type"] != "end" || end["message_id"] != "device-1-2" || end["frame_count"] != float64(0) {
		t.Fatalf("空 end: %#v", end)
	}
	if s.Snapshot().Voice != voiceIdle {
		t.Fatal("sendend 后会话应结束")
	}

	s.Execute("sendstart")
	start = asMap(t, sd.next(t).data)
	id := start["message_id"].(string)
	writeJSON(t, sd, map[string]any{"type": "start_ack", "ok": true, "message_id": id})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Voice == voiceAccepted && sn.Frames == 0 })
	s.Execute("sendtext 你好 世界")
	frame := sd.next(t)
	if frame.kind != websocket.BinaryMessage || string(frame.data) != "你好 世界" {
		t.Fatalf("文本帧: %d %q", frame.kind, frame.data)
	}
	long := strings.Repeat("你", 1366)
	s.Execute("sendtext " + long)
	first := sd.next(t)
	second := sd.next(t)
	if len(first.data) != 4095 || string(second.data) != "你" {
		t.Fatalf("拆帧: %d %d", len(first.data), len(second.data))
	}
	snap := s.Snapshot()
	if snap.Frames != 3 {
		t.Fatalf("帧数 %d", snap.Frames)
	}
	s.Execute("sendend")
	end = asMap(t, sd.next(t).data)
	if end["frame_count"] != float64(3) || end["message_id"] != id {
		t.Fatalf("end: %#v", end)
	}
	writeJSON(t, sd, map[string]any{"type": "end_ack", "message_id": id, "receivers": 2})
	waitSnap(t, s, func(sn Snapshot) bool {
		return strings.Contains(strings.Join(sn.Status, "\n"), "结束确认，接收端 2")
	})
}

func TestVoiceReceiveAndDisconnect(t *testing.T) {
	url, accept := startServer(t)
	s := NewSession()
	s.Execute("connect " + url + " device-1")
	sd := accept()
	_ = sd.next(t)
	writeJSON(t, sd, map[string]any{"type": "hello_ack", "ok": true, "room_name": "房间", "chat_session_id": "view-1"})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Registered })
	writeJSON(t, sd, map[string]any{"type": "chat_message", "message_id": "m1", "source_id": "a", "text": "留下", "chat_session_id": "view-1"})
	waitSnap(t, s, func(sn Snapshot) bool { return len(sn.Chats) == 1 })

	writeJSON(t, sd, map[string]any{"type": "start", "message_id": "peer-1", "source_id": "peer"})
	waitSnap(t, s, func(sn Snapshot) bool { return sn.Incoming })
	sd.none(t)
	if err := sd.conn.WriteMessage(websocket.BinaryMessage, []byte("你")); err != nil {
		t.Fatal(err)
	}
	if err := sd.conn.WriteMessage(websocket.BinaryMessage, []byte("好")); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, sd, map[string]any{"type": "end", "message_id": "peer-1", "frame_count": 2})
	snap := waitSnap(t, s, func(sn Snapshot) bool { return len(sn.Forwards) == 1 })
	if snap.Forwards[0].SourceID != "peer" || snap.Forwards[0].Text != "你好" || snap.Forwards[0].Received != 2 || snap.Forwards[0].Declared != 2 {
		t.Fatalf("转发: %+v", snap.Forwards)
	}

	writeJSON(t, sd, map[string]any{"type": "start", "message_id": "peer-2", "source_id": "peer"})
	if err := sd.conn.WriteMessage(websocket.BinaryMessage, []byte("甲")); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, sd, map[string]any{"type": "end", "message_id": "peer-2", "frame_count": 3})
	snap = waitSnap(t, s, func(sn Snapshot) bool { return len(sn.Forwards) == 2 })
	if snap.Forwards[1].Text != "甲" || snap.Forwards[1].Received != 1 || snap.Forwards[1].Declared != 3 {
		t.Fatalf("帧数不一致: %+v", snap.Forwards[1])
	}

	writeJSON(t, sd, map[string]any{"type": "start", "message_id": "peer-3", "source_id": "peer"})
	if err := sd.conn.WriteMessage(websocket.BinaryMessage, []byte("秘密")); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, sd, map[string]any{"type": "abort", "message_id": "peer-3"})
	snap = waitSnap(t, s, func(sn Snapshot) bool {
		return !sn.Incoming && strings.Contains(strings.Join(sn.Status, "\n"), "会话已中止 peer-3")
	})
	if len(snap.Forwards) != 2 {
		t.Fatalf("中止后仍展示缓冲: %+v", snap.Forwards)
	}

	_ = sd.conn.Close()
	snap = waitSnap(t, s, func(sn Snapshot) bool { return !sn.Registered && len(sn.Status) == 1 })
	if snap.Status[0] != "已断开" || len(snap.Chats) != 0 || len(snap.Forwards) != 0 || snap.Incoming {
		t.Fatalf("断开: %+v", snap)
	}
}
