package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/hraban/opus"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func samplePacket(t *testing.T) []byte {
	t.Helper()
	e, err := opus.NewEncoder(16000, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 400)
	n, err := e.Encode(make([]int16, 960), b)
	if err != nil {
		t.Fatal(err)
	}
	return b[:n]
}
func sampleTTSWAV() []byte {
	b := make([]byte, 44+2880)
	copy(b, "RIFF")
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 2880)
	return b
}
func mockSpeech(t *testing.T) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/audio":
			w.Write(sampleTTSWAV())
		case "/tts":
			json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"audio": map[string]string{"url": ts.URL + "/audio"}}})
		case "/hosting":
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"should_reply":true,"text":"reply"}`}}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": strings.Repeat("字", 100)}}}})
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}
func TestASRProcessingAndTranslation(t *testing.T) {
	ts := mockSpeech(t)
	s := newServer()
	c := s.chat
	packet := samplePacket(t)
	if _, err := opusWAV(nil); err == nil {
		t.Fatal("empty audio accepted")
	}
	wav, err := opusWAV([][]byte{packet})
	if err != nil || len(wav) != 1964 {
		t.Fatalf("wav size=%d err=%v", len(wav), err)
	}
	m := &chatMessage{ID: "human", packets: [][]byte{packet}, Expires: time.Now().Add(time.Hour)}
	c.process(context.Background(), s, chatJob{m, asrConfig{URL: ts.URL}})
	if m.Status != "completed" || !m.Truncated || len([]rune(m.Text)) != 81 {
		t.Fatalf("message=%+v", m)
	}
	failed := &chatMessage{}
	c.process(context.Background(), s, chatJob{message: failed})
	if failed.Status != "failed" {
		t.Fatal("decode failure not marked")
	}
	p := testPeer(t, s, "translated")
	p.chatEnabled = true
	c.hostingConfig = hostingConfig{URL: ts.URL, Key: "ut", Model: "m"}
	c.ttsConfig = ttsConfig{Enabled: true, URL: ts.URL + "/tts", Key: "ut"}
	recipient := chatRecipient{p, p.playbackToken(), "en-US"}
	m.Source = "other"
	m.Room = "default"
	m.recipients = []chatRecipient{recipient}
	c.queueTranslations(s, m)
	if len(c.translationJobs) != 1 {
		t.Fatal("translation not queued")
	}
	job := <-c.translationJobs
	c.processTranslation(context.Background(), s, job)
	if len(c.messages) != 1 || c.messages[0].ParentID != m.ID || c.messages[0].Language != "en-US" {
		t.Fatal("translation not stored")
	}
	// Wait for the synthetic stream end before reading peer fields or returning.
	deadline := time.After(2 * time.Second)
	ended := false
	for !ended {
		select {
		case out := <-p.out:
			var ctrl control
			json.Unmarshal(out.data, &ctrl)
			ended = ctrl.Type == "end"
		case <-deadline:
			t.Fatal("translated stream did not end")
		}
	}
}
func TestHostedAudioFlow(t *testing.T) {
	ts := mockSpeech(t)
	s := newServer()
	p := testPeer(t, s, "host")
	p.hosted.Store(true)
	p.hostingBusy = true
	j := hostingJob{p: p, token: p.playbackToken(), epoch: p.hostingEpoch.Load(), room: "default", created: time.Now(), config: hostingConfig{URL: ts.URL + "/hosting"}, tts: ttsConfig{URL: ts.URL + "/tts"}}
	s.chat.processHosting(context.Background(), s, j)
	if p.hostingBusy || s.active != nil || len(s.chat.messages) != 1 || !s.chat.messages[0].Automated {
		t.Fatal("hosting did not finish")
	}
	if p.lastHostedReply.IsZero() {
		t.Fatal("reply timestamp missing")
	}
	j.created = time.Now().Add(-time.Minute)
	p.hostingBusy = true
	s.chat.processHosting(context.Background(), s, j)
	if p.hostingBusy {
		t.Fatal("stale job left busy")
	}
}
func TestTTSDispatcher(t *testing.T) {
	ts := mockSpeech(t)
	s := newServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testPeer(t, s, "tts-listener")
	p.chatEnabled = true
	m := &chatMessage{ID: "tts", Room: "default", spokenText: "test", Expires: time.Now().Add(time.Hour), recipients: []chatRecipient{{p, p.playbackToken(), "zh-CN"}}}
	s.chat.messages = []*chatMessage{m}
	done := make(chan struct{})
	go func() { defer close(done); s.chat.runTTS(ctx, s) }()
	s.chat.ttsJobs <- ttsJob{m, ttsConfig{URL: ts.URL + "/tts"}}
	select {
	case <-p.out:
	case <-time.After(time.Second):
		t.Fatal("TTS update missing")
	}
	cancel()
	<-done
	if !m.Available || !m.Announce || len(m.packets) == 0 {
		t.Fatal("audio not populated")
	}
}
func TestDispatchCancellationAndPruning(t *testing.T) {
	s := newServer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.chat.run(ctx, s)
	s.chat.runHosting(ctx, s)
	now := time.Now()
	s.chat.messages = []*chatMessage{{Expires: now.Add(-time.Second), size: 1}, {Expires: now.Add(time.Hour), size: chatMaxBytes + 1}}
	s.chat.pruneLocked(now)
	if len(s.chat.messages) != 0 || s.chat.bytes != 0 {
		t.Fatal("expired or oversized retained")
	}
	for i := 0; i < 514; i++ {
		s.chat.messages = append(s.chat.messages, &chatMessage{Expires: now.Add(time.Hour), size: 1})
	}
	s.chat.pruneLocked(now)
	if len(s.chat.messages) != 512 || s.chat.bytes != 512 {
		t.Fatal("message count unbounded")
	}
}
