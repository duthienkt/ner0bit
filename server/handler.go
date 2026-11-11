package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Heartbeat lớn nhất của firmware ~2,3 KB (20 events); 64 KB là dư dả.
const maxBodyBytes = 64 << 10

// Giờ Việt Nam (UTC+7, không DST) — không phụ thuộc tzdata trong image.
var vietnam = time.FixedZone("ICT", 7*3600)

// Server xử lý POST heartbeat từ thiết bị (docs/system-design.md, "Interface ESP ↔ Server")
// và giao diện web (ui.go).
type Server struct {
	Token      string // so với header "Authorization: Bearer <token>"
	DeviceID   string // MAC 12 hex của thiết bị; rỗng ⇒ nhận mọi id
	DataDir    string // chứa schedule.json, heartbeats.jsonl, events.jsonl
	UIPassword string // mật khẩu giao diện web; rỗng ⇒ tắt UI
	// TrustCFHeader: lấy IP client từ header CF-Connecting-IP (giới hạn đăng nhập sai theo IP).
	// Chỉ bật khi server CHỈ nhận request qua Cloudflare Tunnel — mở port trực tiếp thì client tự
	// đặt được header này.
	TrustCFHeader bool
	Now           func() time.Time // nil ⇒ time.Now

	mu sync.Mutex // tuần tự hoá ghi/đọc file log

	routesOnce sync.Once
	handler    http.Handler

	lastMu sync.Mutex
	last   *lastHeartbeat // heartbeat cuối nhận được (nil ⇒ chưa có)

	scheduleMu sync.Mutex // tuần tự hoá PUT /api/schedule

	loginMu       sync.Mutex
	loginFails    map[string]*loginFailures // theo IP client
	loginSlowdown func()                    // nil ⇒ ngủ 1 s (xem slowdown); test thay bằng hàm rỗng
	slowMu        sync.Mutex                // tuần tự hoá lần sai không ghi nhận được (xem handleLogin)

	schedMu   sync.Mutex        // giữ lastSched
	lastSched map[string]string // body lịch trả gần nhất theo id, để nhận ra lịch mới
}

type lastHeartbeat struct {
	at  time.Time
	raw json.RawMessage
}

type heartbeat struct {
	ID     string            `json:"id"`
	Events []json.RawMessage `json:"events"`
}

// Event do firmware gửi (lib/core/event.h): một event mỗi lần bơm đổi trạng thái, cộng mốc bị bỏ.
type deviceEvent struct {
	Type   string `json:"type"`
	At     string `json:"at"`
	Source string `json:"source"`
	Reason string `json:"reason"`
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.routesOnce.Do(func() { s.handler = s.routes() })
	s.handler.ServeHTTP(w, r)
}

// handleHeartbeat: POST từ thiết bị. Nhận ở mọi path (như trước khi có UI) trừ các route UI.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r.Header.Get("Authorization")) {
		log.Printf("từ chối %s: token sai/thiếu", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var hb heartbeat
	if err := json.Unmarshal(raw, &hb); err != nil || hb.ID == "" {
		log.Printf("từ chối %s: heartbeat JSON sai hoặc thiếu id", r.RemoteAddr)
		http.Error(w, "bad heartbeat", http.StatusBadRequest)
		return
	}
	if s.DeviceID != "" && hb.ID != s.DeviceID {
		log.Printf("từ chối %s: id lạ %q", r.RemoteAddr, hb.ID)
		http.Error(w, "unknown device", http.StatusForbidden)
		return
	}

	// Nạp lịch TRƯỚC khi ghi log: lịch lỗi ⇒ 503, ESP giữ events và gửi lại sau,
	// nên lúc này không ghi để tránh trùng.
	sched, err := loadSchedule(filepath.Join(s.DataDir, "schedule.json"))
	if err != nil {
		log.Printf("lỗi lịch: %v", err)
		http.Error(w, "schedule unavailable", http.StatusServiceUnavailable)
		return
	}

	now := s.now()
	if err := s.record(now, hb.ID, raw, hb.Events); err != nil {
		log.Printf("lỗi ghi log: %v", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	s.setLastHeartbeat(now, raw)

	body := sched.render(now.In(vietnam).Format(time.DateOnly))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
	logEvents(hb.ID, hb.Events)
	if s.schedNew(hb.ID, body) {
		log.Printf("🗓️ lịch mới trả cho %s: %q", hb.ID, body)
	}
	log.Printf("heartbeat %s: %d events, trả lịch %q", hb.ID, len(hb.Events), body)
}

func (s *Server) authorized(header string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) == 1
}

// schedNew trả true khi body lịch trả cho id khác lần gần nhất (hoặc id lần đầu).
func (s *Server) schedNew(id, body string) bool {
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	if s.lastSched == nil {
		s.lastSched = make(map[string]string)
	}
	if prev, ok := s.lastSched[id]; ok && prev == body {
		return false
	}
	s.lastSched[id] = body
	return true
}

// logEvents in event của thiết bị ra console — cùng bộ emoji như log Serial của
// firmware (src/shared.cpp): 💦 bắt đầu tưới, 🛑 ngừng tưới, ⏭️ bỏ mốc.
func logEvents(id string, events []json.RawMessage) {
	for _, raw := range events {
		var ev deviceEvent
		if err := json.Unmarshal(raw, &ev); err != nil || ev.Type == "" {
			log.Printf("event %s: không đọc được: %s", id, raw)
			continue
		}
		switch ev.Type {
		case "start":
			log.Printf("event %s: 💦 Bắt đầu tưới (nguồn %s, lúc %s)", id, sourceText(ev.Source), atHM(ev.At))
		case "stop":
			log.Printf("event %s: 🛑 Ngừng tưới (%s, lúc %s)", id, reasonText(ev.Reason), atHM(ev.At))
		case "skipped":
			log.Printf("event %s: ⏭️ Bỏ mốc tưới (%s, lúc %s)", id, reasonText(ev.Reason), atHM(ev.At))
		default:
			log.Printf("event %s: type lạ %q (%s)", id, ev.Type, raw)
		}
	}
}

// atHM rút "2026-11-11T06:00:00+07:00" còn "06:00"; sai định dạng ⇒ trả nguyên bản.
func atHM(at string) string {
	if len(at) >= 16 && at[4] == '-' && at[10] == 'T' {
		return at[11:16]
	}
	return at
}

func sourceText(s string) string {
	switch s {
	case "today":
		return "lịch hôm nay"
	case "failsafe":
		return "failsafe"
	default:
		return "lịch thường"
	}
}

func reasonText(r string) string {
	switch r {
	case "capped":
		return "bị cắt bởi hard cap"
	case "abort":
		return "hủy giữa chừng"
	case "rest":
		return "nghỉ giữa 2 phiên"
	case "busy":
		return "bận — phiên khác đang chạy"
	case "done":
		return "hết phiên"
	default:
		return "lý do không rõ"
	}
}

// record ghi heartbeat nguyên bản vào heartbeats.jsonl và từng event vào events.jsonl.
func (s *Server) record(now time.Time, id string, raw []byte, events []json.RawMessage) error {
	at := now.In(vietnam).Format(time.RFC3339)
	hbLine, err := json.Marshal(struct {
		ReceivedAt string          `json:"received_at"`
		Heartbeat  json.RawMessage `json:"heartbeat"`
	}{at, raw})
	if err != nil {
		return err
	}

	var evLines []byte
	for _, ev := range events {
		var fields map[string]any
		if err := json.Unmarshal(ev, &fields); err != nil {
			fields = map[string]any{"raw": string(ev)}
		}
		fields["id"] = id
		fields["received_at"] = at
		line, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		evLines = append(append(evLines, line...), '\n')
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := appendFile(filepath.Join(s.DataDir, "heartbeats.jsonl"), append(hbLine, '\n')); err != nil {
		return err
	}
	if len(evLines) > 0 {
		return appendFile(filepath.Join(s.DataDir, "events.jsonl"), evLines)
	}
	return nil
}

func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
