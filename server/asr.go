package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hraban/opus"
)

const defaultASRModel = "qwen3-asr-flash"
const chatMaxBytes = 32 * 1024 * 1024

type asrConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Model   string `json:"model"`
	Key     string `json:"api_key,omitempty"`
}

func (c asrConfig) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("ASR URL must be an HTTPS chat/completions URL without credentials or query")
	}
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		return errors.New("ASR URL must end with /chat/completions")
	}
	if len(c.Key) > 4096 || len(c.URL) > 2048 || len(c.Model) > 128 {
		return errors.New("ASR configuration too long")
	}
	if c.Enabled && (c.Key == "" || c.Model == "") {
		return errors.New("API key and model required")
	}
	return nil
}
func recognize(ctx context.Context, client *http.Client, cfg asrConfig, wav []byte) (string, error) {
	body := map[string]any{"model": cfg.Model, "stream": false, "asr_options": map[string]any{"language": "zh", "enable_itn": false}, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav)}}}}}}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(b))
	if err != nil {
		return "", errors.New("cannot construct ASR request")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("ASR network request failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ASR HTTP status %d", resp.StatusCode)
	} // Never echo upstream bodies or credentials.
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&result); err != nil {
		return "", errors.New("invalid ASR response")
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", errors.New("ASR returned no text")
	}
	return result.Choices[0].Message.Content, nil
}
func translateText(ctx context.Context, client *http.Client, cfg hostingConfig, text, target string) (string, error) {
	prompt := "Translate the message into " + target + ". Return only the translation, with no explanations, labels, quotes, or extra sentences. Preserve names, numbers, locations, and operational instructions."
	body := map[string]any{"model": cfg.Model, "stream": false, "messages": []any{map[string]string{"role": "system", "content": prompt}, map[string]string{"role": "user", "content": text}}, "max_tokens": 300}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(b))
	if err != nil {
		return "", errors.New("invalid translation request")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("translation request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("translation HTTP status %d", resp.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || len(result.Choices) == 0 {
		return "", errors.New("invalid translation response")
	}
	out := strings.TrimSpace(result.Choices[0].Message.Content)
	if out == "" || len([]rune(out)) > 512 {
		return "", errors.New("invalid translation text")
	}
	return out, nil
}
func randomToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Completed messages are memory-only. Recipients are pinned to the connection
// and room-view token at start, never rediscovered after ASR completes.
type chatRecipient struct {
	peer     *peer
	token    string
	language string
}
type chatMessage struct {
	Automated     bool   `json:"automated,omitempty"`
	EventKind     string `json:"event_kind,omitempty"`
	EventRoom     string `json:"event_room_id,omitempty"`
	EventRoomName string `json:"event_room_name,omitempty"`
	Announce      bool   `json:"announce,omitempty"`
	spokenText    string
	ID            string    `json:"message_id"`
	ParentID      string    `json:"parent_message_id,omitempty"`
	Language      string    `json:"language,omitempty"`
	AudioKind     string    `json:"audio_kind,omitempty"`
	Room          string    `json:"room_id"`
	Source        string    `json:"source_id"`
	Created       string    `json:"created_at"`
	Duration      int       `json:"duration_ms"`
	Text          string    `json:"text"`
	Truncated     bool      `json:"text_truncated"`
	Status        string    `json:"asr_status"`
	Available     bool      `json:"audio_available"`
	Expires       time.Time `json:"-"`
	packets       [][]byte
	recipients    []chatRecipient
	size          int
}
type chatJob struct {
	message *chatMessage
	config  asrConfig
}
type translationJob struct {
	message   *chatMessage
	recipient chatRecipient
	config    hostingConfig
	tts       ttsConfig
}
type chatService struct {
	hostingConfig   hostingConfig
	hostingJobs     chan hostingJob
	ttsConfig       ttsConfig
	ttsJobs         chan ttsJob
	translationJobs chan translationJob
	ttsClient       *http.Client
	mu              sync.Mutex
	config          asrConfig
	messages        []*chatMessage
	bytes           int
	jobs            chan chatJob
	slots           chan struct{}
	client          *http.Client
}

func newChatService() *chatService {
	return &chatService{hostingConfig: defaultHostingConfig(), hostingJobs: make(chan hostingJob, 16), ttsConfig: defaultTTSConfig(), ttsJobs: make(chan ttsJob, 16), translationJobs: make(chan translationJob, 16), ttsClient: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, config: asrConfig{Model: defaultASRModel}, jobs: make(chan chatJob, 16), slots: make(chan struct{}, 2), client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// One dispatcher per running HTTP service, stopped along with its context.
func (c *chatService) run(ctx context.Context, s *server) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			c.mu.Lock()
			c.pruneLocked(time.Now())
			c.mu.Unlock()
			s.mu.Unlock()
		case job := <-c.jobs:
			select {
			case c.slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func() { defer func() { <-c.slots }(); c.process(ctx, s, job) }()
		case job := <-c.translationJobs:
			go c.processTranslation(ctx, s, job)
		}
	}
}
func (c *chatService) pruneLocked(now time.Time) {
	kept := c.messages[:0]
	c.bytes = 0
	for _, m := range c.messages {
		if now.Before(m.Expires) {
			kept = append(kept, m)
			c.bytes += m.size
		}
	}
	c.messages = kept
	for (c.bytes > chatMaxBytes || len(c.messages) > 512) && len(c.messages) > 0 {
		c.bytes -= c.messages[0].size
		c.messages = c.messages[1:]
	}
}
func opusWAV(packets [][]byte) ([]byte, error) {
	dec, err := opus.NewDecoder(16000, 1)
	if err != nil {
		return nil, err
	}
	var pcm bytes.Buffer
	samples := make([]int16, 1920) // Opus packets can represent up to 120ms.
	for _, packet := range packets {
		n, e := dec.Decode(packet, samples)
		if e != nil {
			return nil, e
		}
		if pcm.Len()+n*2 > 960000 {
			return nil, errors.New("decoded audio exceeds 30 seconds")
		}
		binary.Write(&pcm, binary.LittleEndian, samples[:n])
	}
	if pcm.Len() == 0 {
		return nil, errors.New("empty audio")
	}
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(pcm.Len()+36))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], 16000)
	binary.LittleEndian.PutUint32(h[28:], 32000)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(pcm.Len()))
	return append(h, pcm.Bytes()...), nil
}

// Called under server.mu. Never decode or call ASR on the realtime path.
func (s *server) completeChat(p *peer) {
	if len(p.chatPackets) == 0 {
		return
	}
	m := &chatMessage{ID: randomToken(), Room: p.chatRoom, Source: p.id, Language: s.store.DeviceLanguage(p.id), AudioKind: "original", Created: time.Now().UTC().Format(time.RFC3339), Duration: p.frames * 60, Status: "disabled", Available: true, Expires: time.Now().Add(30 * time.Minute), packets: p.chatPackets, recipients: p.chatRecipients, size: p.bytes}
	p.chatPackets = nil
	p.chatRecipients = nil
	c := s.chat
	c.mu.Lock()
	cfg := c.config
	if cfg.Enabled {
		m.Status = "pending"
	}
	counts := map[string]int{}
	for i := len(c.messages) - 1; i >= 0; i-- {
		old := c.messages[i]
		kept := old.recipients[:0]
		for _, r := range old.recipients {
			if s.peers[r.peer.id] != r.peer || r.peer.playbackToken() != r.token {
				continue
			}
			counts[r.token]++
			if counts[r.token] < 20 {
				kept = append(kept, r)
			}
		}
		old.recipients = kept
		if len(kept) == 0 {
			old.Expires = time.Now()
		}
	}
	c.messages = append(c.messages, m)
	c.bytes += m.size
	c.pruneLocked(time.Now())
	c.mu.Unlock()
	s.sendChat(m, "chat_message")
	if cfg.Enabled {
		select {
		case c.jobs <- chatJob{m, cfg}:
		default:
			c.mu.Lock()
			m.Status = "failed"
			c.mu.Unlock()
			s.sendChat(m, "chat_message_update")
			log.Printf("ASR queue full message=%s", m.ID)
		}
	}
}

// Requires server.mu. It serializes view validation and message delivery.
func (s *server) sendChat(m *chatMessage, kind string) {
	for _, r := range m.recipients {
		p := r.peer
		if !p.chatEnabled || s.peers[p.id] != p || p.playbackToken() != r.token || s.store.CurrentRoom(p.id).ID != m.Room {
			continue
		}
		b, _ := json.Marshal(m)
		var event map[string]any
		json.Unmarshal(b, &event)
		event["type"] = kind
		event["chat_session_id"] = r.token
		event["audio_path"] = s.publicBaseURL + "/api/intercom/messages/" + m.ID + "/audio"
		encoded, _ := json.Marshal(event)
		p.enqueue(websocket.TextMessage, encoded)
	}
}
func (c *chatService) process(ctx context.Context, s *server, job chatJob) {
	wav, err := opusWAV(job.message.packets)
	var text string
	if err == nil {
		taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		text, err = recognize(taskCtx, c.client, job.config, wav)
		cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.mu.Lock()
	if err != nil {
		job.message.Status = "failed"
		log.Printf("ASR failed message=%s", job.message.ID)
	} else {
		r := []rune(text)
		if len(r) > 80 {
			r = r[:80]
			job.message.Truncated = true
			text = string(r) + "…"
		}
		job.message.Text = text
		job.message.Status = "completed"
		log.Printf("ASR completed message=%s", job.message.ID)
	}
	c.mu.Unlock()
	s.sendChat(job.message, "chat_message_update")
	if err == nil {
		s.queueHosting(job.message)
		c.queueTranslations(s, job.message)
	}
}
func (c *chatService) queueTranslations(s *server, m *chatMessage) {
	c.mu.Lock()
	llm, tts := c.hostingConfig, c.ttsConfig
	c.mu.Unlock()
	if llm.Key == "" || llm.URL == "" || llm.Model == "" || !tts.Enabled || m.Text == "" {
		return
	}
	for _, r := range m.recipients {
		if r.language != "en-US" || r.peer.id == m.Source {
			continue
		}
		select {
		case c.translationJobs <- translationJob{m, r, llm, tts}:
		default:
			log.Printf("translation queue full message=%s", m.ID)
		}
	}
}
func (c *chatService) processTranslation(ctx context.Context, s *server, job translationJob) {
	task, cancel := context.WithTimeout(ctx, 30*time.Second)
	text, err := translateText(task, c.client, job.config, job.message.Text, "English")
	cancel()
	if err != nil {
		log.Printf("translation failed message=%s: %v", job.message.ID, err)
		return
	}
	task, cancel = context.WithTimeout(ctx, 45*time.Second)
	wav, err := synthesize(task, c.ttsClient, job.tts, text)
	cancel()
	if err != nil {
		return
	}
	packets, duration, err := ttsOpus(wav)
	if err != nil {
		return
	}
	m := &chatMessage{ID: randomToken(), ParentID: job.message.ID, Room: job.message.Room, Source: job.message.Source, Created: time.Now().UTC().Format(time.RFC3339), Duration: duration, Text: text, Status: "completed", Available: true, Language: "en-US", AudioKind: "translation", Expires: time.Now().Add(30 * time.Minute), packets: packets, recipients: []chatRecipient{job.recipient}}
	for _, p := range packets {
		m.size += len(p)
	}
	s.mu.Lock()
	c.mu.Lock()
	c.messages = append(c.messages, m)
	c.pruneLocked(time.Now())
	c.mu.Unlock()
	s.sendChat(m, "chat_message")
	s.mu.Unlock()
	s.sendAutomatedAudio(m, packets)
}

func (s *server) sendAutomatedAudio(m *chatMessage, packets [][]byte) {
	go func() {
		synthetic := randomToken()
		deadline := time.Now().Add(20 * time.Second)
		for {
			s.mu.Lock()
			free := s.active == nil
			s.mu.Unlock()
			if free || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		s.mu.Lock()
		p := m.recipients[0].peer
		if s.peers[p.id] != p || p.playbackToken() != m.recipients[0].token {
			s.mu.Unlock()
			return
		}
		p.mu.Lock()
		p.receiving = synthetic
		p.ready = true
		p.mu.Unlock()
		p.send(control{Type: "start", SourceID: m.Source, MessageID: synthetic, FrameMS: 60, SampleRate: 16000})
		s.mu.Unlock()
		for _, packet := range packets {
			s.mu.Lock()
			if s.peers[p.id] == p {
				p.enqueue(websocket.BinaryMessage, packet)
			}
			s.mu.Unlock()
			time.Sleep(60 * time.Millisecond)
		}
		s.mu.Lock()
		if s.peers[p.id] == p {
			p.mu.Lock()
			p.receiving = ""
			p.ready = false
			p.mu.Unlock()
			p.send(control{Type: "end", MessageID: synthetic, FrameCount: len(packets)})
		}
		s.mu.Unlock()
	}()
}
func (p *peer) playbackToken() string {
	p.chatTokenMu.Lock()
	defer p.chatTokenMu.Unlock()
	return p.chatToken
}
func (s *server) resetChatView(p *peer) {
	p.hosted.Store(false)
	p.hostingEpoch.Add(1)
	p.chatTokenMu.Lock()
	defer p.chatTokenMu.Unlock()
	p.chatToken = randomToken()
}
