package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func validJoinCode(code string) bool {
	if len(code) != 4 || code[0] < '1' || code[0] > '9' {
		return false
	}
	for i := 1; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

type room struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	JoinCode  string   `json:"join_code,omitempty"`
	CreatedBy string   `json:"created_by_device_id,omitempty"`
	Default   bool     `json:"is_default"`
	Closed    bool     `json:"closed"`
	Devices   []string `json:"device_ids"`
	Invited   []string `json:"invite_device_ids,omitempty"`
}
type device struct {
	DeviceType string `json:"device_type"`
	RoomName   string `json:"room_name"`
	Hosted     bool   `json:"hosted"`
	ID         string `json:"device_id"`
	LastSeen   string `json:"last_seen_at"`
	IP         string `json:"last_ip"`
	Online     bool   `json:"online"`
	RoomID     string `json:"room_id"`
	Language   string `json:"language"`
}

type roomStore interface {
	RegisterDevice(id, ip string, deviceType ...string) room
	CurrentRoom(deviceID string) room
	Join(deviceID, code string) (room, error)
	Leave(deviceID string) room
	CreateRoom(name, creator string, invited []string) (room, error)
	UpdateRoom(id, name string, invited []string) (room, error)
	ListRooms() []room
	RoomDevices(id string) []device
	// CloseRoom returns the devices that were members when it closed.
	CloseRoom(id string) ([]device, error)
	ReturnDefault(id string) error
	CreateSimulator(id string) error
	DeleteSimulator(id string) error
	ListDevices() []device
	DeviceLanguage(id string) string
	SetDeviceLanguage(id, language string) error
}

type memoryStore struct {
	mu      sync.Mutex
	rooms   map[string]*room
	devices map[string]*device
}

func newMemoryStore() *memoryStore {
	return &memoryStore{rooms: map[string]*room{"default": {ID: "default", Name: "默认房间", Default: true}}, devices: map[string]*device{}}
}
func normalizeLanguage(language string) string {
	if language == "en-US" {
		return "en-US"
	}
	return "zh-CN"
}
func (s *memoryStore) RegisterDevice(id, ip string, deviceType ...string) room {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[id]
	if d == nil {
		d = &device{ID: id}
		s.devices[id] = d
	}
	if len(deviceType) > 0 {
		d.DeviceType = deviceType[0]
	}
	d.LastSeen = time.Now().UTC().Format(time.RFC3339)
	d.IP = ip
	d.Online = true
	if d.Language == "" {
		d.Language = "zh-CN"
	}
	return s.currentLocked(id)
}
func (s *memoryStore) currentLocked(id string) room {
	if d := s.devices[id]; d != nil && d.RoomID != "" {
		if r := s.rooms[d.RoomID]; r != nil && !r.Closed {
			return cloneRoom(r)
		}
	}
	return cloneRoom(s.rooms["default"])
}
func cloneRoom(r *room) room {
	if r == nil {
		return room{}
	}
	x := *r
	x.Devices = append([]string(nil), r.Devices...)
	x.Invited = append([]string(nil), r.Invited...)
	return x
}
func (s *memoryStore) CurrentRoom(id string) room {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLocked(id)
}
func (s *memoryStore) Join(id, code string) (room, error) {
	if !validJoinCode(code) {
		return room{}, errors.New("invalid room code")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rooms {
		if r.JoinCode == code && !r.Closed {
			if len(r.Invited) > 0 && !contains(r.Invited, id) && r.CreatedBy != id {
				return room{}, errors.New("device is not invited")
			}
			s.setRoomLocked(id, r.ID)
			return cloneRoom(r), nil
		}
	}
	return room{}, errors.New("invalid room code")
}
func (s *memoryStore) Leave(id string) room {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setRoomLocked(id, "default")
	return cloneRoom(s.rooms["default"])
}
func (s *memoryStore) setRoomLocked(id, rid string) {
	d := s.devices[id]
	if d == nil {
		d = &device{ID: id}
		s.devices[id] = d
	}
	for _, r := range s.rooms {
		r.Devices = remove(r.Devices, id)
	}
	d.RoomID = rid
	if rid != "default" {
		s.rooms[rid].Devices = append(s.rooms[rid].Devices, id)
	}
}
func remove(xs []string, id string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}
func (s *memoryStore) CreateRoom(name, creator string, invited []string) (room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(name) == "" {
		return room{}, errors.New("name is required")
	}
	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	id := "room-" + hex.EncodeToString(idb)
	code := ""
	for {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		code = fmt.Sprintf("%04d", 1000+(uint32(b[0])<<8|uint32(b[1]))%9000)
		found := false
		for _, r := range s.rooms {
			if r.JoinCode == code {
				found = true
			}
		}
		if !found {
			break
		}
	}
	r := &room{ID: id, Name: strings.TrimSpace(name), JoinCode: code, CreatedBy: creator, Invited: append([]string(nil), invited...)}
	s.rooms[id] = r
	s.setRoomLocked(creator, id)
	return cloneRoom(r), nil
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func (s *memoryStore) ListRooms() []room {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []room{}
	for _, r := range s.rooms {
		if !r.Closed {
			out = append(out, cloneRoom(r))
		}
	}
	return out
}
func (s *memoryStore) RoomDevices(id string) []device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []device{}
	for did, d := range s.devices {
		if s.currentLocked(did).ID == id {
			x := *d
			x.RoomID = id
			out = append(out, x)
		}
	}
	return out
}
func (s *memoryStore) CloseRoom(id string) ([]device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.closableLocked(id); err != nil {
		return nil, err
	}
	r := s.rooms[id]
	members := []device{}
	for did, d := range s.devices {
		if s.currentLocked(did).ID == id {
			members = append(members, *d)
			d.RoomID = "default"
		}
	}
	r.Closed = true
	r.Devices = nil
	return members, nil
}

// Requires memoryStore.mu.
func (s *memoryStore) closableLocked(id string) error {
	r := s.rooms[id]
	if r == nil {
		return errors.New("room not found")
	}
	if r.Default {
		return errors.New("cannot close default room")
	}
	return nil
}
func (s *memoryStore) ReturnDefault(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setRoomLocked(id, "default")
	return nil
}
func (s *memoryStore) ListDevices() []device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]device, 0, len(s.devices))
	for _, d := range s.devices {
		x := *d
		x.RoomID = s.currentLocked(d.ID).ID
		out = append(out, x)
	}
	return out
}

func (s *memoryStore) DeviceLanguage(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.devices[id]; d != nil {
		return normalizeLanguage(d.Language)
	}
	return "zh-CN"
}
func (s *memoryStore) SetDeviceLanguage(id, language string) error {
	language = normalizeLanguage(language)
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[id]
	if d == nil {
		d = &device{ID: id}
		s.devices[id] = d
	}
	d.Language = language
	return nil
}

type sqliteStore struct {
	// Serialize each mutation with its persistence to preserve write order.
	persistMu sync.Mutex
	path      string
	*memoryStore
	eventData *memoryEventStore
}

func newSQLiteStore(path string) (*sqliteStore, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	s := &sqliteStore{path: path, memoryStore: newMemoryStore(), eventData: newMemoryEventStore()}
	if err := s.initAdmin(); err != nil {
		return nil, err
	}
	if _, err := s.exec(`PRAGMA journal_mode=WAL; CREATE TABLE IF NOT EXISTS devices(device_id TEXT PRIMARY KEY,last_seen_at TEXT,ip TEXT,room_id TEXT,language TEXT NOT NULL DEFAULT 'zh-CN'); CREATE TABLE IF NOT EXISTS rooms(id TEXT PRIMARY KEY,name TEXT,join_code TEXT UNIQUE,is_default INTEGER,status TEXT,created_by TEXT,invite_device_ids TEXT DEFAULT ''); CREATE TABLE IF NOT EXISTS events(event_id TEXT PRIMARY KEY,target_device_id TEXT NOT NULL,creator_device_id TEXT NOT NULL,content TEXT NOT NULL,scheduled_at TEXT NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL,completed_at TEXT,cancelled_at TEXT);`); err != nil {
		return nil, err
	}
	_, _ = s.exec(`ALTER TABLE devices ADD COLUMN device_type TEXT NOT NULL DEFAULT ''`)
	_, _ = s.exec(`ALTER TABLE devices ADD COLUMN language TEXT NOT NULL DEFAULT 'zh-CN'`)
	_, _ = s.exec(`ALTER TABLE rooms ADD COLUMN invite_device_ids TEXT DEFAULT ''`)
	_, err := s.exec(`INSERT OR IGNORE INTO rooms(id,name,join_code,is_default,status,created_by) VALUES('default','默认房间','',1,'active','')`)
	if err == nil {
		s.load()
		s.loadEventData()
	}
	return s, err
}

func (s *sqliteStore) loadEventData() {
	b, err := s.exec("SELECT event_id,target_device_id,creator_device_id,content,scheduled_at,status,created_at,COALESCE(completed_at,''),COALESCE(cancelled_at,'') FROM events")
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return
	}
	var rows []struct {
		ID        string `json:"event_id"`
		Target    string `json:"target_device_id"`
		Creator   string `json:"creator_device_id"`
		Content   string `json:"content"`
		Scheduled string `json:"scheduled_at"`
		Status    string `json:"status"`
		Created   string `json:"created_at"`
		Completed string `json:"completed_at"`
		Cancelled string `json:"cancelled_at"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return
	}
	s.eventData.mu.Lock()
	defer s.eventData.mu.Unlock()
	for _, r := range rows {
		x := &event{ID: r.ID, TargetDeviceID: r.Target, CreatorDeviceID: r.Creator, Content: r.Content, ScheduledAt: r.Scheduled, Status: r.Status, CreatedAt: r.Created, CompletedAt: r.Completed, CancelledAt: r.Cancelled}
		s.eventData.events[x.ID] = x
	}
}

func (s *sqliteStore) CreateEvent(target, creator, content, scheduled string) (event, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	e, err := s.eventData.CreateEvent(target, creator, content, scheduled)
	if err == nil {
		q := fmt.Sprintf("INSERT INTO events(event_id,target_device_id,creator_device_id,content,scheduled_at,status,created_at) VALUES(%s,%s,%s,%s,%s,%s,%s)", sqliteString(e.ID), sqliteString(e.TargetDeviceID), sqliteString(e.CreatorDeviceID), sqliteString(e.Content), sqliteString(e.ScheduledAt), sqliteString(e.Status), sqliteString(e.CreatedAt))
		if _, x := s.exec(q); x != nil {
			return event{}, x
		}
	}
	return e, err
}
func (s *sqliteStore) ListEvents(device string) []event { return s.eventData.ListEvents(device) }
func (s *sqliteStore) AllEvents() []event               { return s.eventData.AllEvents() }
func (s *sqliteStore) SetEventStatus(id, device, status string) (event, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	e, err := s.eventData.SetEventStatus(id, device, status)
	if err == nil {
		q := "UPDATE events SET status=" + sqliteString(e.Status) + ",completed_at=" + sqliteString(e.CompletedAt) + ",cancelled_at=" + sqliteString(e.CancelledAt) + " WHERE event_id=" + sqliteString(e.ID)
		_, err = s.exec(q)
	}
	return e, err
}
func (s *sqliteStore) DueEvents(now time.Time) []event { return s.eventData.DueEvents(now) }
func (s *sqliteStore) MarkOverdue(id string) (event, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	e, err := s.eventData.MarkOverdue(id)
	if err == nil {
		_, err = s.exec("UPDATE events SET status='overdue' WHERE event_id=" + sqliteString(id))
	}
	return e, err
}

func (s *sqliteStore) exec(q string) ([]byte, error) {
	cmd := exec.Command("sqlite3", "-json", s.path, q)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("sqlite: %s", strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return out, err
}

// sqliteString returns a SQL string literal. Go's %q is not suitable here:
// its double quotes are parsed by SQLite as identifiers.
func sqliteString(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

func (s *sqliteStore) persistDevice(id string) {
	s.mu.Lock()
	stored := s.devices[id]
	if stored == nil {
		s.mu.Unlock()
		return
	}
	d := *stored
	s.mu.Unlock()
	q := fmt.Sprintf("INSERT INTO devices(device_id,last_seen_at,ip,room_id,device_type,language) VALUES(%s,%s,%s,%s,%s,%s) ON CONFLICT(device_id) DO UPDATE SET last_seen_at=excluded.last_seen_at,ip=excluded.ip,room_id=excluded.room_id,device_type=excluded.device_type,language=excluded.language", sqliteString(d.ID), sqliteString(d.LastSeen), sqliteString(d.IP), sqliteString(d.RoomID), sqliteString(d.DeviceType), sqliteString(normalizeLanguage(d.Language)))
	if _, err := s.exec(q); err != nil {
		log.Printf("debug sqlite persist device=%s error=%v", id, err)
	}
}
func (s *sqliteStore) load() {
	log.Printf("debug sqlite loading path=%s", s.path)
	if b, err := s.exec("SELECT id,name,join_code,is_default,status,created_by,invite_device_ids FROM rooms"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		var rows []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Code    string `json:"join_code"`
			Default int    `json:"is_default"`
			Status  string `json:"status"`
			Creator string `json:"created_by"`
			Invited string `json:"invite_device_ids"`
		}
		if e := json.Unmarshal(b, &rows); e != nil {
			log.Printf("debug sqlite load rooms error=%v output=%s", e, b)
		} else {
			log.Printf("debug sqlite loaded rooms=%d", len(rows))
			for _, row := range rows {
				if row.ID != "default" {
					s.rooms[row.ID] = &room{ID: row.ID, Name: row.Name, JoinCode: row.Code, CreatedBy: row.Creator, Invited: splitIDs(row.Invited), Closed: row.Status != "active"}
				}
			}
		}
	} else {
		log.Printf("debug sqlite load rooms error=%v", err)
	}
	if b, err := s.exec("SELECT device_id,last_seen_at,ip,COALESCE(room_id,'') AS room_id,device_type,COALESCE(language,'zh-CN') AS language FROM devices"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		var rows []struct {
			ID         string `json:"device_id"`
			Seen       string `json:"last_seen_at"`
			IP         string `json:"ip"`
			RoomID     string `json:"room_id"`
			DeviceType string `json:"device_type"`
			Language   string `json:"language"`
		}
		if e := json.Unmarshal(b, &rows); e != nil {
			log.Printf("debug sqlite load devices error=%v output=%s", e, b)
		} else {
			log.Printf("debug sqlite loaded devices=%d", len(rows))
			for _, row := range rows {
				s.devices[row.ID] = &device{ID: row.ID, LastSeen: row.Seen, IP: row.IP, RoomID: row.RoomID, DeviceType: row.DeviceType, Language: normalizeLanguage(row.Language)}
				if row.RoomID != "" && s.rooms[row.RoomID] != nil {
					s.rooms[row.RoomID].Devices = append(s.rooms[row.RoomID].Devices, row.ID)
				}
			}
		}
	} else {
		log.Printf("debug sqlite load devices error=%v", err)
	}
}
func splitIDs(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}
func (s *sqliteStore) RegisterDevice(id, ip string, deviceType ...string) room {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	r := s.memoryStore.RegisterDevice(id, ip, deviceType...)
	s.persistDevice(id)
	return r
}
func (s *sqliteStore) DeviceLanguage(id string) string { return s.memoryStore.DeviceLanguage(id) }
func (s *sqliteStore) SetDeviceLanguage(id, language string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if err := s.memoryStore.SetDeviceLanguage(id, language); err != nil {
		return err
	}
	s.persistDevice(id)
	return nil
}
func (s *sqliteStore) Join(id, code string) (room, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	r, e := s.memoryStore.Join(id, code)
	if e == nil {
		s.persistDevice(id)
	}
	return r, e
}
func (s *sqliteStore) Leave(id string) room {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	r := s.memoryStore.Leave(id)
	s.persistDevice(id)
	return r
}
func (s *sqliteStore) CreateRoom(name, creator string, invited []string) (room, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	r, e := s.memoryStore.CreateRoom(name, creator, invited)
	if e == nil {
		q := fmt.Sprintf("INSERT INTO rooms(id,name,join_code,is_default,status,created_by,invite_device_ids) VALUES(%s,%s,%s,0,'active',%s,%s)", sqliteString(r.ID), sqliteString(r.Name), sqliteString(r.JoinCode), sqliteString(r.CreatedBy), sqliteString(strings.Join(r.Invited, ",")))
		if _, err := s.exec(q); err != nil {
			log.Printf("debug sqlite create room=%s error=%v", r.ID, err)
			return room{}, err
		}
		for _, id := range r.Devices {
			s.persistDevice(id)
		}
	}
	return r, e
}
func (s *sqliteStore) CloseRoom(id string) ([]device, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	err := s.closableLocked(id)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// Commit room closure and membership together before changing memory.
	_, err = s.exec("BEGIN IMMEDIATE; UPDATE rooms SET status='closed' WHERE id=" + sqliteString(id) + "; UPDATE devices SET room_id='default' WHERE room_id=" + sqliteString(id) + "; COMMIT;")
	if err != nil {
		return nil, err
	}
	return s.memoryStore.CloseRoom(id)
}
func (s *sqliteStore) ReturnDefault(id string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	e := s.memoryStore.ReturnDefault(id)
	s.persistDevice(id)
	return e
}

var _ = sql.ErrNoRows

func (s *memoryStore) UpdateRoom(id, name string, invited []string) (room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[id]
	if r == nil || r.Closed {
		return room{}, errors.New("room not found")
	}
	if r.Default {
		return room{}, errors.New("default room cannot be edited")
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return room{}, errors.New("name must contain 1-80 characters")
	}
	clean := []string{}
	for _, id := range invited {
		id = strings.TrimSpace(id)
		if !validID.MatchString(id) {
			return room{}, errors.New("invalid invited device id")
		}
		if !contains(clean, id) {
			clean = append(clean, id)
		}
	}
	r.Name = name
	r.Invited = clean
	return cloneRoom(r), nil
}
func (s *sqliteStore) UpdateRoom(id, name string, invited []string) (room, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	// Validate against a detached snapshot before modifying memory.
	s.mu.Lock()
	original := s.rooms[id]
	old := cloneRoom(original)
	s.mu.Unlock()
	if original == nil {
		return room{}, errors.New("room not found")
	}
	temp := newMemoryStore()
	temp.rooms[id] = &old
	r, e := temp.UpdateRoom(id, name, invited)
	if e != nil {
		return room{}, e
	}
	_, e = s.exec("UPDATE rooms SET name=" + sqliteString(r.Name) + ",invite_device_ids=" + sqliteString(strings.Join(r.Invited, ",")) + " WHERE id=" + sqliteString(id))
	if e != nil {
		return room{}, e
	}
	return s.memoryStore.UpdateRoom(id, r.Name, r.Invited)
}

func (s *memoryStore) DeleteSimulator(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[id]
	if d == nil {
		return nil
	}
	if d.DeviceType != "simulator" {
		return errors.New("only simulator devices can be deleted")
	}
	delete(s.devices, id)
	for _, r := range s.rooms {
		r.Devices = remove(r.Devices, id)
	}
	return nil
}
func (s *sqliteStore) DeleteSimulator(id string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	d := s.devices[id]
	if d == nil {
		s.mu.Unlock()
		return nil
	}
	if d.DeviceType != "simulator" {
		s.mu.Unlock()
		return errors.New("only simulator devices can be deleted")
	}
	s.mu.Unlock()
	if _, err := s.exec("DELETE FROM devices WHERE device_id=" + sqliteString(id) + " AND device_type='simulator'"); err != nil {
		return err
	}
	return s.memoryStore.DeleteSimulator(id)
}

func (s *memoryStore) CreateSimulator(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(id) {
		return errors.New("invalid device id")
	}
	if s.devices[id] != nil {
		return errors.New("device id already exists")
	}
	s.devices[id] = &device{ID: id, DeviceType: "simulator", RoomID: "default"}
	return nil
}
func (s *sqliteStore) CreateSimulator(id string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(id) {
		return errors.New("invalid device id")
	}
	if s.devices[id] != nil {
		return errors.New("device id already exists")
	}
	_, err := s.exec("INSERT INTO devices(device_id,last_seen_at,ip,room_id,device_type) VALUES(" + sqliteString(id) + ",'','','default','simulator')")
	if err != nil {
		return err
	}
	s.devices[id] = &device{ID: id, DeviceType: "simulator", RoomID: "default"}
	return nil
}
