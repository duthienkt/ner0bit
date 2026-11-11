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
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Giao diện web: xem trạng thái thiết bị, sửa lịch tưới. Bật khi có NER0BIT_UI_PASSWORD.
//
//	GET  /              trang UI (chưa đăng nhập ⇒ form đăng nhập)
//	POST /login         form "password" ⇒ cookie phiên
//	POST /logout
//	GET  /api/state     heartbeat cuối + online + lịch hiện hành + body lịch trả hôm nay + "day"
//	                    (lịch tưới hôm nay, cùng dạng /api/day)
//	GET  /api/schedule
//	PUT  /api/schedule  JSON {"daily": "..."}, lưu vào SQLite
//	POST /api/sessions  JSON {"date","start_s","run_s","note"} ⇒ thêm phiên bơm thử (manual)
//	DELETE /api/sessions/{id}  xoá phiên manual
//	GET  /api/events    ?limit=N (mặc định 100, tối đa 1000), mới nhất trước
//	Lịch tưới tự động, thời tiết, tham số: xem api_auto.go.
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
)

//go:embed ui/index.html
var pageSource string

var pageTemplate = template.Must(template.New("page").Parse(pageSource))

var errScheduleMissing = errors.New("chưa có lịch dự phòng — lưu lịch để tạo")

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
	mux.HandleFunc("POST /api/sessions", s.api(s.handleAddSession))
	mux.HandleFunc("DELETE /api/sessions/{id}", s.api(s.handleDeleteSession))
	mux.HandleFunc("GET /api/events", s.api(s.handleEvents))
	mux.HandleFunc("GET /api/day", s.api(s.handleDay))
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.api(s.handleCancelSession))
	mux.HandleFunc("GET /api/weather", s.api(s.handleWeather))
	mux.HandleFunc("GET /api/settings", s.api(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.api(s.handlePutSettings))
	mux.HandleFunc("POST /api/settings/daily-from-slots", s.api(s.handleDailyFromSlots))
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
	if s.cannotRecord(ip) {
		// Map đầy toàn mục bị khoá (đang bị dò hàng loạt) và nguồn này chưa được ghi nhận: phải giành
		// lượt toàn cục TRƯỚC khi kiểm tra mật khẩu — tối đa ~1 lần thử/giây cho cả server, và 429
		// không lộ mật khẩu đúng hay sai.
		if !s.slowMu.TryLock() {
			log.Printf("UI: 429 cho %s (đang bị dò mật khẩu hàng loạt)", ip)
			s.renderPage(w, http.StatusTooManyRequests, pageData{Mode: "login",
				Error: "Đang có quá nhiều lần đăng nhập sai. Thử lại sau ít giây."})
			return
		}
		defer s.slowMu.Unlock()
		if !s.passwordMatches(r.PostFormValue("password")) {
			s.slowdown()
			log.Printf("UI: đăng nhập sai từ %s (không ghi nhận được, đã làm chậm)", ip)
			s.renderPage(w, http.StatusUnauthorized, pageData{Mode: "login", Error: "Sai mật khẩu."})
			return
		}
	} else if !s.passwordMatches(r.PostFormValue("password")) {
		if !s.loginFailed(ip, now) {
			s.slowdown() // map vừa đầy giữa chừng: hiếm, chỉ làm chậm
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

// cannotRecord: nguồn chưa được ghi nhận và map đầy toàn mục đang bị khoá (không còn chỗ để đếm).
func (s *Server) cannotRecord(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if _, ok := s.loginFails[ip]; ok {
		return false
	}
	return len(s.loginFails) >= maxLoginIPs && s.oldestUnlockedLocked() == ""
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

// currentSchedule đọc lịch dự phòng (dòng 1) từ DB; chưa đặt ⇒ errScheduleMissing.
func (s *Server) currentSchedule() (*Schedule, error) {
	daily, err := s.Store.Daily()
	if err != nil {
		return nil, err
	}
	if daily == "" {
		return nil, errScheduleMissing
	}
	return &Schedule{Daily: daily}, nil
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
	Day           *dayResponse    `json:"day"` // lịch tưới hôm nay; null nếu đọc lỗi
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
	}
	// today_body lấy từ cùng lần đọc với "day" để hai phần không lệch nhau.
	if day, err := s.dayView(resp.Today, now); err != nil {
		log.Printf("UI: lỗi đọc lịch hôm nay: %v", err)
		if resp.ScheduleError == "" {
			resp.ScheduleError = "Không đọc được lịch hôm nay."
		}
	} else {
		resp.Day = day
		if resp.Schedule != nil {
			resp.TodayBody = day.DeviceBody
		}
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
	sched, hasDays, err := parseSchedule(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Lịch không hợp lệ: "+err.Error())
		return
	}
	if hasDays {
		writeJSONError(w, http.StatusBadRequest, "Lịch riêng theo ngày đã chuyển sang /api/sessions.")
		return
	}
	// Dòng 2 = daily ∪ phiên manual: daily mới không được làm ngày nào từ hôm nay vượt giới hạn mốc.
	counts, err := s.Store.manualCountsFrom(s.now().In(vietnam).Format(time.DateOnly))
	if err != nil {
		log.Printf("UI: lỗi đọc phiên: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được phiên.")
		return
	}
	nDaily := len(parseLineEntries(sched.Daily))
	for date, n := range counts {
		if nDaily+n > maxEntriesPerLine {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf(
				"Lịch ngày %s sẽ vượt %d mốc (%d mốc daily + %d phiên bơm thử). Xoá bớt phiên bơm thử trước.",
				date, maxEntriesPerLine, nDaily, n))
			return
		}
	}
	if err := s.Store.SetDaily(sched.Daily); err != nil {
		log.Printf("UI: lỗi ghi lịch: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không ghi được lịch.")
		return
	}
	log.Printf("UI: %s lưu lịch: daily=%q", s.clientIP(r), sched.Daily)
	writeJSON(w, http.StatusOK, &Schedule{Daily: sched.Daily})
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
	events, err := s.Store.Events(limit)
	if err != nil {
		log.Printf("UI: lỗi đọc events: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được events.")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// ---- Heartbeat cuối ----

func (s *Server) setLastHeartbeat(at time.Time, raw []byte) {
	s.lastMu.Lock()
	s.last = &lastHeartbeat{at: at, raw: json.RawMessage(raw)}
	s.lastMu.Unlock()
}

// LoadLastHeartbeat nạp heartbeat cuối từ DB (gọi lúc khởi động). Chưa có ⇒ không lỗi.
func (s *Server) LoadLastHeartbeat() error {
	at, raw, ok, err := s.Store.LastHeartbeat()
	if err != nil || !ok {
		return err
	}
	s.setLastHeartbeat(at, raw)
	return nil
}
