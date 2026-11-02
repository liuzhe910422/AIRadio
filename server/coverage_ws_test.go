package main

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebsocketControlPaths(t *testing.T) {
	s := newServer()
	ts := httptest.NewServer(httpHandler(s))
	defer ts.Close()
	c := connect(t, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", "ws-ut")
	defer c.Close()
	exchange := func(in control, kind string) control {
		t.Helper()
		if err := c.WriteJSON(in); err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			var out control
			if err := c.ReadJSON(&out); err != nil {
				t.Fatal(err)
			}
			if out.Type == kind {
				return out
			}
		}
	}
	for _, lang := range []string{"bad", "en-US", "zh-CN"} {
		out := exchange(control{Type: "set_language", Language: lang}, "language_state")
		if out.OK == nil || *out.OK != (lang != "bad") {
			t.Fatal(out)
		}
	}
	exchange(control{Type: "image_message"}, "error")
	exchange(control{Type: "set_hosting"}, "hosting_state")
	exchange(control{Type: "set_hosting", Enabled: boolPtr(true)}, "hosting_state")
	if out := exchange(control{Type: "set_hosting", Enabled: boolPtr(false)}, "hosting_state"); out.OK == nil || !*out.OK {
		t.Fatal(out)
	}
	exchange(control{Type: "list_rooms"}, "list_rooms_ack")
	exchange(control{Type: "event_create", TargetDeviceID: "bad!"}, "event_create_ack")
	e := exchange(control{Type: "event_create", TargetDeviceID: "ws-ut", Content: "event", ScheduledAt: time.Now().Add(time.Hour).Format(time.RFC3339)}, "event_create_ack")
	if e.EventID == "" {
		t.Fatal(e)
	}
	exchange(control{Type: "event_list"}, "event_list_ack")
	exchange(control{Type: "event_complete", EventID: e.EventID}, "event_complete_ack")
	exchange(control{Type: "event_cancel", EventID: e.EventID}, "event_cancel_ack")
	e = exchange(control{Type: "event_create", TargetDeviceID: "offline", Content: "cancel", ScheduledAt: time.Now().Add(time.Hour).Format(time.RFC3339)}, "event_create_ack")
	exchange(control{Type: "event_cancel", EventID: e.EventID}, "event_cancel_ack")
	exchange(control{Type: "event_execute_ack", EventID: e.EventID}, "event_execute_ack")
	exchange(control{Type: "join_room", Code: "bad"}, "join_room_ack")
	exchange(control{Type: "accept_room_invite", Code: "bad"}, "accept_room_invite_ack")
	exchange(control{Type: "create_room", Name: ""}, "create_room_ack")
	r, err := s.store.CreateRoom("invite", "offline", []string{"ws-ut"})
	if err != nil {
		t.Fatal(err)
	}
	if out := exchange(control{Type: "accept_room_invite", Code: r.JoinCode}, "accept_room_invite_ack"); out.OK == nil || !*out.OK {
		t.Fatal(out)
	}
	exchange(control{Type: "leave_room"}, "room_state")
	exchange(control{Type: "end", MessageID: "missing"}, "error")
	exchange(control{Type: "unknown"}, "error")
}
func TestWebsocketHelloRejections(t *testing.T) {
	for _, tc := range []struct {
		hello control
		auth  bool
	}{
		{control{Type: "hello", Version: 1, DeviceID: "ut", DeviceType: "unknown"}, false},
		{control{Type: "hello", Version: 2, DeviceID: "ut"}, false},
		{control{Type: "hello", Version: 1, DeviceID: "bad!"}, false},
		{control{Type: "hello", Version: 1, DeviceID: "ut"}, true},
	} {
		s := newServer()
		s.authEnabled = tc.auth
		s.authKey = "key"
		ts := httptest.NewServer(httpHandler(s))
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		c.WriteJSON(tc.hello)
		c.SetReadDeadline(time.Now().Add(time.Second))
		var out control
		err = c.ReadJSON(&out)
		c.Close()
		ts.Close()
		if err != nil || out.OK == nil || *out.OK {
			t.Fatalf("rejection=%+v %v", out, err)
		}
	}
}
func TestWebsocketChatImagesAndHosting(t *testing.T) {
	s := newServer()
	s.chat.config.Enabled = true
	s.chat.hostingConfig.Enabled = true
	s.chat.ttsConfig.Enabled = true
	ts := httptest.NewServer(httpHandler(s))
	defer ts.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.WriteJSON(control{Type: "hello", Version: 1, DeviceID: "chat-ut", ChatEnabled: true})
	readControl(t, c)
	c.WriteJSON(control{Type: "set_hosting", Enabled: boolPtr(true)})
	c.SetReadDeadline(time.Now().Add(time.Second))
	for {
		var out control
		if err = c.ReadJSON(&out); err != nil {
			t.Fatal(err)
		}
		if out.Type == "hosting_state" {
			if out.OK == nil || !*out.OK {
				t.Fatal(out)
			}
			break
		}
	}
	c.WriteJSON(control{Type: "image_message", ImageURL: "/image", Text: "photo"})
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var out control
		json.Unmarshal(raw, &out)
		if out.Type == "image_message" {
			if out.ImageURL != "/image" {
				t.Fatal(out)
			}
			break
		}
	}
}
