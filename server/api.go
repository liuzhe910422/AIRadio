package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
)

type apiServer struct {
	mu       sync.Mutex
	s        *server
	sessions map[string]bool
}

func (a *apiServer) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (a *apiServer) auth(w http.ResponseWriter, r *http.Request) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, e := r.Cookie("airadio_admin")
	if e != nil || !a.sessions[c.Value] {
		http.Error(w, "unauthorized", 401)
		return false
	}
	return true
}
func (a *apiServer) login(w http.ResponseWriter, r *http.Request) {
	var x struct{ Username, Password string }
	if json.NewDecoder(r.Body).Decode(&x) != nil || !a.s.checkAdmin(x.Username, x.Password) {
		http.Error(w, "invalid credentials", 401)
		return
	}
	a.mu.Lock()
	token := randomToken()
	a.sessions[token] = true
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "airadio_admin", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	a.json(w, map[string]bool{"ok": true})
}
func (a *apiServer) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie("airadio_admin"); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "airadio_admin", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	a.json(w, map[string]bool{"ok": true})
}
func (a *apiServer) rooms(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.Method == "GET" {
		a.json(w, legacyRooms(a.s.store.ListRooms()))
		return
	}
	if r.Method == "POST" {
		var x struct {
			Name              string   `json:"name"`
			CreatedByDeviceID string   `json:"created_by_device_id"`
			DeviceIDs         []string `json:"device_ids"`
		}
		if json.NewDecoder(r.Body).Decode(&x) != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		rr, e := a.s.store.CreateRoom(x.Name, x.CreatedByDeviceID, x.DeviceIDs)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		a.s.roomInvitations(rr)
		if x.CreatedByDeviceID != "" {
			a.s.mu.Lock()
			p := a.s.peers[x.CreatedByDeviceID]
			if p != nil {
				a.s.resetChatView(p)
				p.send(control{Type: "room_state", RoomID: rr.ID, RoomName: rr.Name, ChatSessionID: p.playbackToken()})
				a.s.roomNotice(p, rr, p.id, "member_joined")
			}
			a.s.mu.Unlock()
		}
		a.json(w, legacyRoom(rr))
		return
	}
	http.Error(w, "method not allowed", 405)
}

func legacyRoom(r room) map[string]any {
	return map[string]any{"id": r.ID, "name": r.Name, "join_code": r.JoinCode, "is_default": r.Default, "device_ids": r.Devices, "invite_device_ids": r.Invited, "created_by_device_id": r.CreatedBy, "ID": r.ID, "JoinCode": r.JoinCode, "Default": r.Default, "Devices": r.Devices}
}
func legacyRooms(rs []room) []map[string]any {
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, legacyRoom(r))
	}
	return out
}
func (a *apiServer) roomAction(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) < 3 {
		http.NotFound(w, r)
		return
	}
	id := p[2]
	if len(p) == 4 && p[3] == "devices" {
		a.json(w, a.s.store.RoomDevices(id))
		return
	}
	if len(p) == 3 && r.Method == "PATCH" {
		var x struct {
			Name    string   `json:"name"`
			Invited []string `json:"invite_device_ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&x) != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		var old room
		for _, rr := range a.s.store.ListRooms() {
			if rr.ID == id {
				old = rr
			}
		}
		rr, err := a.s.store.UpdateRoom(id, x.Name, x.Invited)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for _, peer := range a.s.peers {
			if a.s.store.CurrentRoom(peer.id).ID == id {
				peer.send(control{Type: "room_state", RoomID: rr.ID, RoomName: rr.Name})
			}
		}
		for _, did := range rr.Invited {
			if !contains(old.Invited, did) && did != rr.CreatedBy {
				if peer := a.s.peers[did]; peer != nil {
					peer.send(control{Type: "room_invite", RoomID: rr.ID, RoomName: rr.Name, Code: rr.JoinCode, SourceID: rr.CreatedBy})
					a.s.roomNotice(peer, rr, rr.CreatedBy, "room_invite")
				}
			}
		}
		a.json(w, legacyRoom(rr))
		return
	}
	if len(p) == 4 && p[3] == "close" {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		a.s.mu.Lock()
		defer a.s.mu.Unlock()
		members := a.s.store.RoomDevices(id)
		if a.s.active != nil && a.s.store.CurrentRoom(a.s.active.id).ID == id {
			http.Error(w, "room is speaking; try again after the conversation", 409)
			return
		}
		if err := a.s.store.CloseRoom(id); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for _, d := range members {
			if peer := a.s.peers[d.ID]; peer != nil {
				a.s.resetChatView(peer)
				rr := a.s.store.CurrentRoom(peer.id)
				peer.send(control{Type: "room_state", RoomID: rr.ID, RoomName: rr.Name, ChatSessionID: peer.playbackToken(), PlaybackToken: peer.playbackToken()})
			}
		}
		a.json(w, map[string]bool{"ok": true})
		return
	}
	http.NotFound(w, r)
}
func (a *apiServer) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.Method != "DELETE" {
		w.WriteHeader(405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	if !validID.MatchString(id) {
		http.Error(w, "invalid device id", 400)
		return
	}
	a.s.mu.Lock()
	if err := a.s.store.DeleteSimulator(id); err != nil {
		a.s.mu.Unlock()
		http.Error(w, err.Error(), 400)
		return
	}
	p := a.s.peers[id]
	if p != nil {
		p.deleted.Store(true)
		p.hosted.Store(false)
		p.hostingEpoch.Add(1)
		delete(a.s.peers, id)
	}
	a.s.mu.Unlock()
	if p != nil {
		p.close()
	}
	a.json(w, map[string]bool{"ok": true})
}
func (a *apiServer) devices(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if r.Method == http.MethodPost {
		var x struct {
			ID string `json:"device_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&x) != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		a.s.mu.Lock()
		err := a.s.store.CreateSimulator(strings.TrimSpace(x.ID))
		a.s.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.json(w, map[string]bool{"ok": true})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	out := a.s.store.ListDevices()
	log.Printf("debug api devices count=%d", len(out))
	a.s.mu.Lock()
	for i := range out {
		out[i].Online = a.s.peers[out[i].ID] != nil
		out[i].RoomID = a.s.store.CurrentRoom(out[i].ID).ID
		out[i].RoomName = a.s.store.CurrentRoom(out[i].ID).Name
		if p := a.s.peers[out[i].ID]; p != nil {
			out[i].Hosted = p.hosted.Load()
		}
	}
	a.s.mu.Unlock()
	a.json(w, out)
}
func (a *apiServer) handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("debug http request method=%s path=%s remote=%s", r.Method, r.URL.Path, r.RemoteAddr)
	log.Printf("debug http request method=%s path=%s remote=%s", r.Method, r.URL.Path, r.RemoteAddr)
	switch {
	case r.URL.Path == "/api/users" || strings.HasPrefix(r.URL.Path, "/api/users/"):
		a.usersHandler(w, r)
	case r.URL.Path == "/api/hosting/test":
		a.hostingTest(w, r)
	case r.URL.Path == "/api/hosting/config":
		a.hostingConfig(w, r)
	case r.URL.Path == "/api/tts/config":
		a.ttsConfig(w, r)
	case r.URL.Path == "/api/tts/test":
		a.ttsTest(w, r)
	case r.URL.Path == "/api/asr/config":
		a.asrConfig(w, r)
	case r.URL.Path == "/api/asr/test":
		a.asrTest(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/intercom/messages/") && strings.HasSuffix(r.URL.Path, "/audio"):
		a.chatAudio(w, r)
	case r.URL.Path == "/api/session":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		if a.auth(w, r) {
			a.json(w, map[string]bool{"ok": true})
		}
	case r.URL.Path == "/api/login":
		a.login(w, r)
	case r.URL.Path == "/api/logout":
		a.logout(w, r)
	case r.URL.Path == "/api/rooms":
		a.rooms(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/rooms/"):
		a.roomAction(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/devices/"):
		a.deleteDevice(w, r)
	case r.URL.Path == "/api/devices":
		a.devices(w, r)
	default:
		http.NotFound(w, r)
	}
}
