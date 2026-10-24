package main

import (
	"context"
	"encoding/binary"
	"github.com/hraban/opus"
	"os"
	"regexp"
	"testing"
)

func TestTTSWAVPlaceholderAndOpus(t *testing.T) {
	wav := make([]byte, 44+24000*2)
	copy(wav, "RIFF")
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 24000)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 0x7fffff9b)
	packets, duration, err := ttsOpus(wav)
	if err != nil || duration != 1020 {
		t.Fatalf("duration=%d err=%v", duration, err)
	}
	dec, _ := opus.NewDecoder(16000, 1)
	for _, p := range packets {
		pcm := make([]int16, 960)
		n, e := dec.Decode(p, pcm)
		if e != nil || n != 960 {
			t.Fatalf("decode %d %v", n, e)
		}
	}
}
func TestRoomNoticeRecipients(t *testing.T) {
	s := newServer()
	a := &peer{id: "a", chatEnabled: true, out: make(chan outgoing, 16)}
	b := &peer{id: "b", chatEnabled: true, out: make(chan outgoing, 16)}
	s.register(a, "a")
	s.register(b, "b")
	r, _ := s.store.CreateRoom("调度组", "a", []string{"b"})
	s.roomInvitations(r)
	if len(b.out) != 2 || len(a.out) != 0 {
		t.Fatal("invitation delivery scope incorrect")
	}
	if s.store.CurrentRoom("b").ID != "default" {
		t.Fatal("invite moved recipient")
	}
	old := s.store.CurrentRoom("b").ID
	s.store.Join("b", r.JoinCode)
	s.resetChatView(b)
	s.roomJoined(b, r, old)
	if len(a.out) != 1 {
		t.Fatal("member notification missing")
	}
	s.roomJoined(b, r, r.ID)
	if len(a.out) != 1 {
		t.Fatal("duplicate join notification")
	}
}
func TestAliyunTTSLive(t *testing.T) {
	if os.Getenv("AIRADIO_TTS_LIVE_TEST") != "1" {
		t.Skip("explicit live only")
	}
	b, e := os.ReadFile("../count.tx")
	if e != nil {
		t.Fatal(e)
	}
	key := regexp.MustCompile(`sk-[A-Za-z0-9_-]+`).FindString(string(b))
	host := regexp.MustCompile(`[A-Za-z0-9.-]+\.cn-beijing\.maas\.aliyuncs\.com`).FindString(string(b))
	if key == "" || host == "" {
		t.Fatal("credentials missing")
	}
	cfg := defaultTTSConfig()
	cfg.Key = key
	cfg.URL = "https://" + host + "/api/v1/services/audio/tts/SpeechSynthesizer"
	c := newChatService()
	wav, e := synthesize(context.Background(), c.ttsClient, cfg, "设备零零三邀请你加入调度组。")
	if e != nil {
		t.Fatal(e)
	}
	p, d, e := ttsOpus(wav)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("Opus packets=%d duration_ms=%d", len(p), d)
}

func TestOnlineNoticeUsesCurrentRoom(t *testing.T) {
	s := newServer()
	a := &peer{chatEnabled: true, out: make(chan outgoing, 16)}
	b := &peer{chatEnabled: true, out: make(chan outgoing, 16)}
	c := &peer{chatEnabled: true, out: make(chan outgoing, 16)}
	s.register(a, "a")
	s.register(b, "b")
	s.register(c, "c")
	r, _ := s.store.CreateRoom("调度组", "a", nil)
	s.store.Join("b", r.JoinCode)
	s.roomOnline(b, r)
	if len(a.out) != 1 || len(b.out) != 1 || len(c.out) != 0 {
		t.Fatal("online notice leaked across rooms")
	}
	s.roomOnline(b, r)
	if len(a.out) != 2 {
		t.Fatal("reconnect should announce again")
	}
}
