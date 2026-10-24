package main

import "errors"

// changeRoom serializes membership changes with audio forwarding. Failed
// requests leave the current receive session and chat view untouched. The
// store may persist through a subprocess, so it runs outside server.mu while
// the switching flag keeps the peer from starting a talk.
func (s *server) changeRoom(id string, change func() (room, error)) (room, error) {
	s.mu.Lock()
	p := s.peers[id]
	if p != nil && (s.active == p || p.switching) {
		s.mu.Unlock()
		return room{}, errors.New("busy")
	}
	if p != nil {
		p.switching = true
	}
	s.mu.Unlock()
	r, err := change()
	s.mu.Lock()
	defer s.mu.Unlock()
	if p != nil {
		p.switching = false
		if err == nil {
			s.resetRoomView(p)
		}
	}
	return r, err
}

// Requires server.mu. Notify before changing the view so clients can drain
// the previous stream even when its sender remains in another room.
func (s *server) resetRoomView(p *peer) {
	p.mu.Lock()
	if p.receiving != "" {
		p.send(control{Type: "abort", MessageID: p.receiving})
		p.receiving, p.ready = "", false
	}
	p.mu.Unlock()
	wasHosted := p.hosted.Load()
	s.resetChatView(p)
	if wasHosted {
		p.send(control{Type: "hosting_state", OK: boolPtr(true), Enabled: boolPtr(false)})
	}
}

func (s *server) closeRoom(id string) error {
	// Persist outside server.mu; members is captured atomically with closure.
	members, err := s.store.CloseRoom(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active.chatRoom == id {
		if !s.active.automatic {
			s.active.send(control{Type: "error", Error: "room closed"})
		}
		s.abortLocked(s.active)
	}
	for _, d := range members {
		if p := s.peers[d.ID]; p != nil {
			s.resetRoomView(p)
			r := s.store.CurrentRoom(p.id)
			p.send(control{Type: "room_state", RoomID: r.ID, RoomName: r.Name, ChatSessionID: p.playbackToken(), PlaybackToken: p.playbackToken()})
		}
	}
	return nil
}
