package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hraban/opus"
)

type ttsConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Model   string `json:"model"`
	Voice   string `json:"voice"`
	Key     string `json:"api_key,omitempty"`
}
type ttsJob struct {
	message *chatMessage
	config  ttsConfig
}

func defaultTTSConfig() ttsConfig {
	return ttsConfig{Model: "qwen-audio-3.1-tts-flash", Voice: "longanhuan_v3.1"}
}
func (c ttsConfig) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/services/audio/tts/SpeechSynthesizer") {
		return errors.New("TTS URL must be an HTTPS SpeechSynthesizer endpoint")
	}
	if len(c.URL) > 2048 || len(c.Key) > 4096 || len(c.Model) > 128 || len(c.Voice) > 128 {
		return errors.New("TTS config too long")
	}
	if c.Enabled && (c.Key == "" || c.Model == "" || c.Voice == "") {
		return errors.New("TTS key, model and voice required")
	}
	return nil
}
func synthesize(ctx context.Context, client *http.Client, cfg ttsConfig, text string) ([]byte, error) {
	body := map[string]any{"model": cfg.Model, "input": map[string]any{"text": text, "voice": cfg.Voice, "format": "wav", "sample_rate": 24000}}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.URL, bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("invalid TTS request")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("TTS request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("TTS HTTP status %d", resp.StatusCode)
	}
	var result struct {
		Output struct {
			Audio struct {
				URL string `json:"url"`
			} `json:"audio"`
		} `json:"output"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil {
		return nil, errors.New("invalid TTS response")
	}
	u, err := url.Parse(result.Output.Audio.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid TTS audio URL")
	}
	req, err = http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid audio request")
	}
	audio, err := client.Do(req)
	if err != nil {
		return nil, errors.New("TTS audio download failed")
	}
	defer audio.Body.Close()
	if audio.StatusCode != 200 {
		return nil, errors.New("TTS audio download rejected")
	}
	wav, err := io.ReadAll(io.LimitReader(audio.Body, 1440045))
	if err != nil || len(wav) > 1440044 {
		return nil, errors.New("TTS audio too large")
	}
	return wav, nil
}

// Read actual chunk bytes, not the vendor's placeholder RIFF/data length.
func ttsOpus(wav []byte) ([][]byte, int, error) {
	if len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, 0, errors.New("invalid WAV")
	}
	var pcm []byte
	valid := false
	for off := 12; off+8 <= len(wav); {
		size := uint64(binary.LittleEndian.Uint32(wav[off+4:]))
		tag := string(wav[off : off+4])
		off += 8
		if tag == "data" {
			available := len(wav) - off
			if size < uint64(available) {
				available = int(size)
			}
			pcm = wav[off : off+available]
			break
		}
		if size > uint64(len(wav)-off) {
			return nil, 0, errors.New("invalid WAV chunk")
		}
		if tag == "fmt " && size >= 16 {
			valid = binary.LittleEndian.Uint16(wav[off:]) == 1 && binary.LittleEndian.Uint16(wav[off+2:]) == 1 && binary.LittleEndian.Uint32(wav[off+4:]) == 24000 && binary.LittleEndian.Uint16(wav[off+14:]) == 16
		}
		off += int(size) + int(size%2)
	}
	if !valid || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > 1440000 {
		return nil, 0, errors.New("expected <=30s mono PCM16 24kHz")
	}
	enc, err := opus.NewEncoder(16000, 1, opus.AppVoIP)
	if err != nil {
		return nil, 0, err
	}
	enc.SetBitrate(24000)
	// 24k ->16k linear resampling; one packet represents exactly60ms.
	count := len(pcm) / 2
	out := make([]int16, count*2/3)
	sample := func(i int) int32 { return int32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) }
	for i := range out {
		pos := i * 3
		j := pos / 2
		v := sample(j)
		if pos%2 != 0 && j+1 < count {
			v = (v + sample(j+1)) / 2
		}
		out[i] = int16(v)
	}
	packets := [][]byte{}
	buf := make([]byte, 4096)
	for off := 0; off < len(out); off += 960 {
		frame := make([]int16, 960)
		copy(frame, out[off:min(off+960, len(out))])
		n, e := enc.Encode(frame, buf)
		if e != nil {
			return nil, 0, e
		}
		packets = append(packets, append([]byte(nil), buf[:n]...))
	}
	return packets, len(packets) * 60, nil
}
func spokenDevice(id string) string {
	var b strings.Builder
	digits := []rune("零一二三四五六七八九")
	for _, r := range id {
		if r >= '0' && r <= '9' {
			b.WriteRune(digits[r-'0'])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// server.mu must be held; text notification is immediate, TTS is queued.
func (s *server) roomNotice(target *peer, r room, actor, kind string) {
	if !target.chatEnabled {
		log.Printf("room notice skipped target=%s actor=%s kind=%s reason=chat_disabled", target.id, actor, kind)
		return
	}
	view := s.store.CurrentRoom(target.id).ID
	english := s.store.DeviceLanguage(target.id) == "en-US"
	text := "设备" + actor + "加入了" + r.Name + "。"
	spoken := "设备" + spokenDevice(actor) + "加入了" + r.Name + "。"
	if english {
		text = "Device " + actor + " joined " + r.Name + "."
		spoken = text
	}
	if kind == "room_invite" {
		if english {
			text = "Device " + actor + " invited you to join " + r.Name + "."
		} else {
			text = "设备" + actor + "邀请你加入" + r.Name + "。"
		}
		spoken = text
	} else if kind == "member_online" {
		if english {
			text = "Device " + actor + " is online in " + r.Name + "."
		} else {
			text = "设备" + actor + "已上线，当前在" + r.Name + "。"
		}
		spoken = text
		if target.id == actor {
			if english {
				text = "You are online in " + r.Name + "."
			} else {
				text = "你已上线，当前在" + r.Name + "。"
			}
			spoken = text
		}
	} else if target.id == actor {
		if english {
			text = "You joined " + r.Name + "."
		} else {
			text = "你已加入" + r.Name + "。"
		}
		spoken = text
	}
	m := &chatMessage{ID: randomToken(), Room: view, Source: actor, Created: time.Now().UTC().Format(time.RFC3339), Text: text, Status: "completed", Expires: time.Now().Add(30 * time.Minute), recipients: []chatRecipient{{target, target.playbackToken(), s.store.DeviceLanguage(target.id)}}, EventKind: kind, EventRoom: r.ID, EventRoomName: r.Name, spokenText: spoken}
	c := s.chat
	c.mu.Lock()
	cfg := c.ttsConfig
	c.messages = append(c.messages, m)
	c.pruneLocked(time.Now())
	c.mu.Unlock()
	log.Printf("room notice created event=%s target=%s actor=%s kind=%s room=%s tts_enabled=%t", m.ID, target.id, actor, kind, r.ID, cfg.Enabled)
	s.sendChat(m, "chat_message")
	if cfg.Enabled {
		select {
		case c.ttsJobs <- ttsJob{m, cfg}:
			log.Printf("TTS queued event=%s", m.ID)
		default:
			log.Printf("TTS queue full event=%s", m.ID)
		}
	}
}
func (s *server) roomOnline(p *peer, r room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range s.peers {
		if s.store.CurrentRoom(other.id).ID == r.ID {
			s.roomNotice(other, r, p.id, "member_online")
		}
	}
}
func (s *server) roomJoined(p *peer, r room, old string) {
	if old == r.ID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range s.peers {
		if s.store.CurrentRoom(other.id).ID == r.ID {
			s.roomNotice(other, r, p.id, "member_joined")
		}
	}
}
func (s *server) roomInvitations(r room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range r.Invited {
		if other := s.peers[id]; other != nil && id != r.CreatedBy {
			other.send(control{Type: "room_invite", RoomID: r.ID, RoomName: r.Name, Code: r.JoinCode, SourceID: r.CreatedBy})
			s.roomNotice(other, r, r.CreatedBy, "room_invite")
		}
	}
}
func (c *chatService) runTTS(ctx context.Context, s *server) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.ttsJobs:
			log.Printf("TTS started event=%s", job.message.ID)
			task, cancel := context.WithTimeout(ctx, 45*time.Second)
			c.mu.Lock()
			text := job.message.spokenText
			c.mu.Unlock()
			wav, err := synthesize(task, c.ttsClient, job.config, text)
			cancel()
			var packets [][]byte
			duration := 0
			if err == nil {
				packets, duration, err = ttsOpus(wav)
			}
			s.mu.Lock()
			c.mu.Lock()
			m := job.message
			if err == nil && time.Now().Before(m.Expires) {
				m.packets = packets
				m.Duration = duration
				m.Available = true
				m.Announce = true
				for _, p := range packets {
					m.size += len(p)
				}
				c.pruneLocked(time.Now())
			}
			c.mu.Unlock()
			if err == nil {
				log.Printf("TTS completed event=%s duration_ms=%d packets=%d", m.ID, duration, len(packets))
				s.sendChat(m, "chat_message_update")
			} else {
				log.Printf("TTS failed event=%s reason=%v", m.ID, err)
			}
			s.mu.Unlock()
		}
	}
}
