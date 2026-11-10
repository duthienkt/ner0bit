package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Giao diện web: xem trạng thái thiết bị, sửa lịch tưới. Bật khi có NER0BIT_UI_PASSWORD.
//
//	GET  /              trang UI (chưa đăng nhập ⇒ form đăng nhập)
//	POST /login         form "password" ⇒ cookie phiên
//	POST /logout
//	GET  /api/state     heartbeat cuối + online + lịch hiện hành + body lịch trả hôm nay
//	GET  /api/schedule
//	PUT  /api/schedule  JSON {"daily": "...", "days": {...}}, ghi atomic vào schedule.json
//	GET  /api/events    ?limit=N (mặc định 100, tối đa 1000), mới nhất trước
//
// Mọi request POST khác vẫn là heartbeat của thiết bị (Bearer token), như trước.

const (
	sessionCookieName = "ner0bit_ui"
	sessionTTL        = 30 * 24 * time.Hour

	loginWindow      = 10 * time.Minute
	maxLoginFailures = 5     // sai đủ số lần này trong cửa sổ ⇒ 429 tới hết cửa sổ
	maxLoginIPs      = 10000 // chặn map phình vô hạn; đầy ⇒ bỏ mục cũ nhất

	onlineWindow      = 15 * time.Minute // 3 lần heartbeat định kỳ (300 s)
	defaultEventLimit = 100
	maxEventLimit     = 1000
	tailChunk         = 64 << 10
)

//go:embed ui/index.html
var pageSource string

var pageTemplate = template.Must(template.New("page").Parse(pageSource))

var errScheduleMissing = errors.New("chưa có schedule.json — lưu lịch để tạo")

type loginFailures struct {
	first time.Time
	count int
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /", s.handleHeartbeat)
	mux.HandleFunc("GET /{$}", s.handlePage)
	mux.HandleFunc("POST /login", s.uiOnly(s.handleLogin))
	mux.HandleFunc("POST /logout", s.uiOnly(s.handleLogout))
	mux.HandleFunc("GET /api/state", s.api(s.handleState))
	mux.HandleFunc("GET /api/schedule", s.api(s.handleGetSchedule))
	mux.HandleFunc("PUT /api/schedule", s.api(s.handlePutSchedule))
	mux.HandleFunc("GET /api/events", s.api(s.handleEvents))
	return mux
}

func (s *Server) uiEnabled() bool { return s.UIPassword != "" }

// uiOnly: UI tắt ⇒ 404 như thể route không tồn tại.
func (s *Server) uiOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.uiEnabled() {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}

// api: UI tắt ⇒ 404; chưa đăng nhập ⇒ 401.
func (s *Server) api(h http.HandlerFunc) http.HandlerFunc {
	return s.uiOnly(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.validSession(r) {
			writeJSONError(w, http.StatusUnauthorized, "Chưa đăng nhập hoặc phiên đã hết hạn.")
			return
		}
		h(w, r)
	})
}

// ---- Trang ----

type pageData struct {
	Mode  string // "disabled" | "login" | "app"
	Error string
	Nonce string
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	switch {
	case !s.uiEnabled():
		s.renderPage(w, http.StatusOK, pageData{Mode: "disabled"})
	case s.validSession(r):
		s.renderPage(w, http.StatusOK, pageData{Mode: "app"})
	default:
		s.renderPage(w, http.StatusOK, pageData{Mode: "login"})
	}
}

func (s *Server) renderPage(w http.ResponseWriter, status int, data pageData) {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	data.Nonce = base64.RawStdEncoding.EncodeToString(nonce[:])
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, data); err != nil {
		log.Printf("lỗi render trang: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+data.Nonce+"'; style-src 'nonce-"+data.Nonce+
		"'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// ---- Đăng nhập ----

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ip := limiterKey(s.clientIP(r))
	now := s.now()
	if until, blocked := s.loginBlocked(ip, now); blocked {
		mins := int(until.Sub(now).Minutes()) + 1
		log.Printf("UI: chặn đăng nhập từ %s (sai quá nhiều lần)", ip)
		s.renderPage(w, http.StatusTooManyRequests, pageData{Mode: "login",
			Error: fmt.Sprintf("Sai quá nhiều lần. Thử lại sau %d phút.", mins)})
		return
	}
	if !s.passwordMatches(r.PostFormValue("password")) {
		if !s.loginFailed(ip, now) {
			s.slowdown()
		}
		log.Printf("UI: đăng nhập sai từ %s", ip)
		s.renderPage(w, http.StatusUnauthorized, pageData{Mode: "login", Error: "Sai mật khẩu."})
		return
	}
	s.loginMu.Lock()
	delete(s.loginFails, ip)
	s.loginMu.Unlock()
	log.Printf("UI: đăng nhập từ %s", ip)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.newSession(now),
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		Expires:  now.Add(sessionTTL),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// passwordMatches so constant-time trên SHA-256 để không lộ cả độ dài mật khẩu.
func (s *Server) passwordMatches(pw string) bool {
	a := sha256.Sum256([]byte(pw))
	b := sha256.Sum256([]byte(s.UIPassword))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// loginBlocked dọn các mục quá hạn rồi cho biết IP có đang bị khoá không (và tới khi nào).
func (s *Server) loginBlocked(ip string, now time.Time) (time.Time, bool) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	for k, f := range s.loginFails {
		if now.Sub(f.first) >= loginWindow {
			delete(s.loginFails, k)
		}
	}
	f := s.loginFails[ip]
	if f == nil {
		return time.Time{}, false // nguồn chưa ghi nhận: không chặn (không khoá oan chủ nhà)
	}
	return f.first.Add(loginWindow), f.count >= maxLoginFailures
}

// loginFailed ghi một lần sai; trả false nếu không ghi được (map đầy toàn mục bị khoá).
func (s *Server) loginFailed(ip string, now time.Time) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginFails == nil {
		s.loginFails = make(map[string]*loginFailures)
	}
	f := s.loginFails[ip]
	if f == nil {
		if len(s.loginFails) >= maxLoginIPs {
			// Đầy ⇒ bỏ mục chưa bị khoá cũ nhất (không khoá oan mọi IP mới). Không bao giờ bỏ mục
			// đang bị khoá — nếu không, lấp map bằng IP khác là gỡ được khoá của chính mình.
			oldest := s.oldestUnlockedLocked()
			if oldest == "" {
				// Toàn mục bị khoá (đang bị dò hàng loạt): không ghi nguồn mới; caller làm chậm lần sai.
				return false
			}
			delete(s.loginFails, oldest)
		}
		f = &loginFailures{first: now}
		s.loginFails[ip] = f
	}
	f.count++
	return true
}

// slowdown: trễ cố định cho lần đăng nhập sai không ghi nhận được (map đầy). Test thay bằng hàm rỗng.
func (s *Server) slowdown() {
	if s.loginSlowdown != nil {
		s.loginSlowdown()
		return
	}
	time.Sleep(time.Second)
}

// oldestUnlockedLocked trả khoá của mục chưa bị khoá (count < maxLoginFailures) cũ nhất, "" nếu
// không có. Gọi khi đang giữ loginMu.
func (s *Server) oldestUnlockedLocked() string {
	var oldest string
	var at time.Time
	for k, v := range s.loginFails {
		if v.count >= maxLoginFailures {
			continue
		}
		if oldest == "" || v.first.Before(at) {
			oldest, at = k, v.first
		}
	}
	return oldest
}

// limiterKey: IPv6 gom theo /64 (một nhà/một máy thường có cả dải /64), IPv4 giữ nguyên.
func limiterKey(ip string) string {
	p := net.ParseIP(ip)
	if p == nil || p.To4() != nil {
		return ip
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// clientIP: sau Cloudflare Tunnel (TrustCFHeader), IP thật nằm ở CF-Connecting-IP — Cloudflare ghi
// đè header này nên client không giả được. Không bật ⇒ chỉ tin RemoteAddr.
func (s *Server) clientIP(r *http.Request) string {
	if s.TrustCFHeader {
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ---- Phiên: cookie "<hạn unix>.<HMAC-SHA256>", khoá suy từ mật khẩu ⇒ đổi mật khẩu là mọi phiên cũ hết hiệu lực ----

func (s *Server) sessionMAC(exp string) string {
	key := sha256.Sum256([]byte("ner0bit-ui:" + s.UIPassword))
	m := hmac.New(sha256.New, key[:])
	m.Write([]byte("session:" + exp))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) newSession(now time.Time) string {
	exp := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return exp + "." + s.sessionMAC(exp)
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	exp, mac, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(s.sessionMAC(exp))) {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && s.now().Unix() < n
}

// ---- API ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("lỗi JSON: %v", err)
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) schedulePath() string { return filepath.Join(s.DataDir, "schedule.json") }

// currentSchedule đọc lịch với thông báo lỗi không chứa đường dẫn (để hiện trên UI).
func (s *Server) currentSchedule() (*Schedule, error) {
	raw, err := os.ReadFile(s.schedulePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errScheduleMissing
	}
	if err != nil {
		return nil, fmt.Errorf("không đọc được schedule.json: %w", err)
	}
	return parseSchedule(raw)
}

type stateResponse struct {
	Now           string          `json:"now"`
	Today         string          `json:"today"`
	Online        bool            `json:"online"`
	ReceivedAt    string          `json:"received_at,omitempty"`
	Heartbeat     json.RawMessage `json:"heartbeat"`
	Schedule      *Schedule       `json:"schedule"`
	ScheduleError string          `json:"schedule_error,omitempty"`
	TodayBody     *string         `json:"today_body"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	now := s.now().In(vietnam)
	resp := stateResponse{
		Now:   now.Format(time.RFC3339),
		Today: now.Format(time.DateOnly),
	}
	s.lastMu.Lock()
	last := s.last
	s.lastMu.Unlock()
	if last != nil {
		resp.ReceivedAt = last.at.In(vietnam).Format(time.RFC3339)
		resp.Heartbeat = last.raw
		resp.Online = now.Sub(last.at) <= onlineWindow
	}
	if sched, err := s.currentSchedule(); err != nil {
		resp.ScheduleError = err.Error()
	} else {
		resp.Schedule = sched
		body := sched.render(resp.Today)
		resp.TodayBody = &body
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	sched, err := s.currentSchedule()
	switch {
	case errors.Is(err, errScheduleMissing):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeJSONError(w, http.StatusInternalServerError, "Lịch hiện tại lỗi: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, sched)
	}
}

func (s *Server) handlePutSchedule(w http.ResponseWriter, r *http.Request) {
	// Chống CSRF (cùng SameSite=Strict): form HTML không gửi được application/json.
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "Cần Content-Type: application/json.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "Lịch quá lớn.")
		return
	}
	sched, err := parseSchedule(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Lịch không hợp lệ: "+err.Error())
		return
	}
	data, err := json.MarshalIndent(sched, "", "  ")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Lỗi tạo JSON.")
		return
	}
	s.scheduleMu.Lock()
	err = writeFileAtomic(s.schedulePath(), append(data, '\n'), 0o644)
	s.scheduleMu.Unlock()
	if err != nil {
		log.Printf("UI: lỗi ghi lịch: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không ghi được schedule.json.")
		return
	}
	log.Printf("UI: %s lưu lịch: daily=%q, %d ngày riêng", s.clientIP(r), sched.Daily, len(sched.Days))
	writeJSON(w, http.StatusOK, sched)
}

// writeFileAtomic ghi file tạm cùng thư mục rồi rename: thiết bị không bao giờ đọc phải file ghi dở.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := defaultEventLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			writeJSONError(w, http.StatusBadRequest, "limit phải là số nguyên ≥ 1.")
			return
		}
		limit = min(n, maxEventLimit)
	}
	s.mu.Lock()
	lines, err := tailLines(filepath.Join(s.DataDir, "events.jsonl"), limit, tailChunk)
	s.mu.Unlock()
	if err != nil {
		log.Printf("UI: lỗi đọc events: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được events.jsonl.")
		return
	}
	events := make([]json.RawMessage, 0, len(lines))
	for _, l := range lines {
		if json.Valid(l) {
			events = append(events, l)
		}
	}
	writeJSON(w, http.StatusOK, events)
}

// ---- Heartbeat cuối ----

func (s *Server) setLastHeartbeat(at time.Time, raw []byte) {
	s.lastMu.Lock()
	s.last = &lastHeartbeat{at: at, raw: json.RawMessage(raw)}
	s.lastMu.Unlock()
}

// LoadLastHeartbeat nạp heartbeat cuối từ đuôi heartbeats.jsonl (gọi lúc khởi động).
// Chưa có file ⇒ không lỗi.
func (s *Server) LoadLastHeartbeat() error {
	// Lấy vài dòng cuối phòng dòng cuối cùng ghi dở (mất điện giữa lúc ghi).
	lines, err := tailLines(filepath.Join(s.DataDir, "heartbeats.jsonl"), 3, tailChunk)
	if err != nil {
		return err
	}
	for _, l := range lines {
		var rec struct {
			ReceivedAt string          `json:"received_at"`
			Heartbeat  json.RawMessage `json:"heartbeat"`
		}
		if json.Unmarshal(l, &rec) != nil || len(rec.Heartbeat) == 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339, rec.ReceivedAt)
		if err != nil {
			continue
		}
		s.setLastHeartbeat(at, rec.Heartbeat)
		return nil
	}
	return nil
}

// tailLines trả tối đa n dòng không rỗng cuối file, mới nhất trước, đọc ngược từng khối
// chunk byte (không đọc cả file). File chưa có ⇒ rỗng, không lỗi.
func tailLines(path string, n, chunk int) ([][]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	var rest []byte // phần đầu dòng chưa biết điểm bắt đầu
	pos := st.Size()
	for pos > 0 && len(lines) < n {
		size := int64(chunk)
		if size > pos {
			size = pos
		}
		pos -= size
		buf := make([]byte, size, int(size)+len(rest))
		if _, err := f.ReadAt(buf, pos); err != nil {
			return nil, err
		}
		buf = append(buf, rest...)
		for len(lines) < n {
			i := bytes.LastIndexByte(buf, '\n')
			if i < 0 {
				break
			}
			if line := buf[i+1:]; len(line) > 0 {
				lines = append(lines, line)
			}
			buf = buf[:i]
		}
		rest = buf
	}
	if pos == 0 && len(rest) > 0 && len(lines) < n {
		lines = append(lines, rest)
	}
	return lines, nil
}
