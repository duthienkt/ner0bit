package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testPassword = "mat-khau-dai-cho-ui"

// newUIServer tạo server có bật UI với đồng hồ chỉnh được.
func newUIServer(t *testing.T, schedule string) (*Server, string, *time.Time) {
	t.Helper()
	s, dir := newTestServer(t, schedule)
	now := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC) // 08:00 giờ VN
	s.Now = func() time.Time { return now }
	s.UIPassword = testPassword
	return s, dir, &now
}

type reqOpt func(*http.Request)

func withCookie(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }
func withHeader(k, v string) reqOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}
func withRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }

func do(s *Server, method, target, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func login(s *Server, password string, opts ...reqOpt) *httptest.ResponseRecorder {
	form := url.Values{"password": {password}}.Encode()
	opts = append([]reqOpt{withHeader("Content-Type", "application/x-www-form-urlencoded")}, opts...)
	return do(s, http.MethodPost, "/login", form, opts...)
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("không có cookie %s (code %d)", sessionCookieName, rec.Code)
	return nil
}

func mustLogin(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	rec := login(s, testPassword)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login: code %d", rec.Code)
	}
	return sessionCookie(t, rec)
}

var apiRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/state"},
	{http.MethodGet, "/api/schedule"},
	{http.MethodPut, "/api/schedule"},
	{http.MethodGet, "/api/events"},
}

func TestUIDisabledWithoutPassword(t *testing.T) {
	s, _ := newTestServer(t, goodSchedule)
	rec := do(s, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "UI chưa bật") {
		t.Fatalf("GET /: code %d body %q", rec.Code, rec.Body.String())
	}
	for _, r := range apiRoutes {
		if rec := do(s, r.method, r.path, "{}", withHeader("Content-Type", "application/json")); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: code %d, want 404", r.method, r.path, rec.Code)
		}
	}
	if rec := login(s, ""); rec.Code != http.StatusNotFound {
		t.Errorf("POST /login: code %d, want 404", rec.Code)
	}
}

func TestLoginPageWhenLoggedOut(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	rec := do(s, http.MethodGet, "/", "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `name="password"`) {
		t.Fatalf("code %d, thiếu form đăng nhập: %q", rec.Code, body)
	}
	if strings.Contains(body, `id="app"`) {
		t.Error("chưa đăng nhập không được trả giao diện chính")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestLoginOK(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	rec := login(s, testPassword)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("code %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("cookie thiếu cờ: %+v", c)
	}
	if want := 30 * 24 * 3600; c.MaxAge != want {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, want)
	}
	page := do(s, http.MethodGet, "/", "", withCookie(c))
	if !strings.Contains(page.Body.String(), `id="app"`) {
		t.Error("đã đăng nhập phải thấy giao diện chính")
	}
	if rec := do(s, http.MethodGet, "/api/state", "", withCookie(c)); rec.Code != http.StatusOK {
		t.Errorf("/api/state: code %d", rec.Code)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	for _, pw := range []string{"", "sai", testPassword + "x", strings.ToUpper(testPassword)} {
		rec := login(s, pw, withRemote(fmt.Sprintf("10.0.0.%d:1234", len(pw))))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("password %q: code %d, want 401", pw, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("password %q: không được cấp cookie", pw)
		}
		if !strings.Contains(rec.Body.String(), "Sai mật khẩu") {
			t.Errorf("password %q: thiếu thông báo lỗi", pw)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, _, now := newUIServer(t, goodSchedule)
	ip := withRemote("203.0.113.5:4444")
	for i := 0; i < 5; i++ {
		if rec := login(s, "sai", ip); rec.Code != http.StatusUnauthorized {
			t.Fatalf("lần sai %d: code %d", i+1, rec.Code)
		}
	}
	// Đã sai 5 lần ⇒ khoá cả mật khẩu đúng tới hết cửa sổ.
	if rec := login(s, testPassword, ip); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("lần 6: code %d, want 429", rec.Code)
	}
	// IP khác không bị ảnh hưởng.
	if rec := login(s, testPassword, withRemote("203.0.113.6:4444")); rec.Code != http.StatusSeeOther {
		t.Errorf("IP khác: code %d", rec.Code)
	}
	*now = now.Add(9 * time.Minute)
	if rec := login(s, testPassword, ip); rec.Code != http.StatusTooManyRequests {
		t.Errorf("sau 9 phút: code %d, want 429", rec.Code)
	}
	*now = now.Add(2 * time.Minute)
	if rec := login(s, testPassword, ip); rec.Code != http.StatusSeeOther {
		t.Errorf("hết cửa sổ: code %d, want 303", rec.Code)
	}
}

func TestLoginRateLimitUsesCFConnectingIP(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	s.TrustCFHeader = true
	// Sau Cloudflare Tunnel mọi request có cùng RemoteAddr; IP thật nằm ở CF-Connecting-IP.
	tunnel := withRemote("127.0.0.1:5555")
	for i := 0; i < 5; i++ {
		login(s, "sai", tunnel, withHeader("CF-Connecting-IP", "198.51.100.1"))
	}
	if rec := login(s, testPassword, tunnel, withHeader("CF-Connecting-IP", "198.51.100.1")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("cùng CF-Connecting-IP: code %d, want 429", rec.Code)
	}
	if rec := login(s, testPassword, tunnel, withHeader("CF-Connecting-IP", "198.51.100.2")); rec.Code != http.StatusSeeOther {
		t.Errorf("CF-Connecting-IP khác: code %d, want 303", rec.Code)
	}
}

func TestLoginFailuresForgottenAfterWindow(t *testing.T) {
	s, _, now := newUIServer(t, goodSchedule)
	for i := 0; i < 50; i++ {
		login(s, "sai", withRemote(fmt.Sprintf("192.0.2.%d:1", i)))
	}
	*now = now.Add(11 * time.Minute)
	login(s, "sai", withRemote("192.0.2.200:1"))
	s.loginMu.Lock()
	n := len(s.loginFails)
	s.loginMu.Unlock()
	if n != 1 {
		t.Errorf("map còn %d mục, want 1 (mục quá hạn phải được dọn)", n)
	}
}

func TestSessionRejected(t *testing.T) {
	s, _, now := newUIServer(t, goodSchedule)
	good := mustLogin(t, s)

	forged := &http.Cookie{Name: sessionCookieName, Value: "9999999999.AAAA"}
	// Kéo dài hạn dùng mà không ký lại.
	exp, sig, _ := strings.Cut(good.Value, ".")
	tampered := &http.Cookie{Name: sessionCookieName, Value: exp + "9." + sig}
	garbage := &http.Cookie{Name: sessionCookieName, Value: "abc"}
	for name, c := range map[string]*http.Cookie{"forged": forged, "tampered": tampered, "garbage": garbage} {
		if rec := do(s, http.MethodGet, "/api/state", "", withCookie(c)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: code %d, want 401", name, rec.Code)
		}
	}

	// Đổi mật khẩu ⇒ phiên cũ hết hiệu lực.
	s2, _, _ := newUIServer(t, goodSchedule)
	s2.UIPassword = "mat-khau-moi"
	if rec := do(s2, http.MethodGet, "/api/state", "", withCookie(good)); rec.Code != http.StatusUnauthorized {
		t.Errorf("đổi mật khẩu: code %d, want 401", rec.Code)
	}

	// Hết hạn sau 30 ngày.
	*now = now.Add(29 * 24 * time.Hour)
	if rec := do(s, http.MethodGet, "/api/state", "", withCookie(good)); rec.Code != http.StatusOK {
		t.Errorf("ngày 29: code %d, want 200", rec.Code)
	}
	*now = now.Add(2 * 24 * time.Hour)
	if rec := do(s, http.MethodGet, "/api/state", "", withCookie(good)); rec.Code != http.StatusUnauthorized {
		t.Errorf("ngày 31: code %d, want 401", rec.Code)
	}
	if rec := do(s, http.MethodGet, "/", "", withCookie(good)); strings.Contains(rec.Body.String(), `id="app"`) {
		t.Error("phiên hết hạn phải thấy form đăng nhập")
	}
}

func TestAPIRequiresLogin(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	for _, r := range apiRoutes {
		rec := do(s, r.method, r.path, `{"daily":"25200"}`, withHeader("Content-Type", "application/json"))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: code %d, want 401", r.method, r.path, rec.Code)
		}
	}
}

func TestLogout(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	rec := do(s, http.MethodPost, "/logout", "", withCookie(c))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("code %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	cleared := sessionCookie(t, rec)
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("cookie không bị xoá: %+v", cleared)
	}
}

func TestPutScheduleValid(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	body := `{"daily":" 21600:600 , 64800 "}`
	rec := do(s, http.MethodPut, "/api/schedule", body, withCookie(c), withHeader("Content-Type", "application/json; charset=utf-8"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
	var got Schedule
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Daily != "21600:600,64800" {
		t.Errorf("response chưa chuẩn hoá: %+v", got)
	}
	if d, err := s.Store.Daily(); err != nil || d != "21600:600,64800" {
		t.Errorf("DB = %q, %v", d, err)
	}
	// Thiết bị nhận lịch mới ngay ở heartbeat kế tiếp.
	if rec := post(s, "Bearer "+testToken, sampleHeartbeat); rec.Body.String() != "21600:600,64800" {
		t.Errorf("thiết bị nhận %q", rec.Body.String())
	}
	if rec := do(s, http.MethodGet, "/api/schedule", "", withCookie(c)); !strings.Contains(rec.Body.String(), `"21600:600,64800"`) {
		t.Errorf("GET /api/schedule = %q", rec.Body.String())
	}
}

func TestPutScheduleInvalid(t *testing.T) {
	cases := map[string]string{
		"bad token":     `{"daily":"25200,abc"}`,
		"empty daily":   `{"daily":""}`,
		"too many":      `{"daily":"` + strings.Repeat("60,", 20) + `60"}`,
		"unknown field": `{"daily":"25200","dayz":{}}`,
		"not json":      `daily=25200`,
		"trailing":      `{"daily":"25200"} {"daily":"1"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, _ := newUIServer(t, goodSchedule)
			c := mustLogin(t, s)
			rec := do(s, http.MethodPut, "/api/schedule", body, withCookie(c), withHeader("Content-Type", "application/json"))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code %d, want 400", rec.Code)
			}
			var e struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
				t.Errorf("thiếu thông báo lỗi JSON: %q", rec.Body.String())
			}
			if d, _ := s.Store.Daily(); d != goodSchedule {
				t.Errorf("lịch bị đổi: %q", d)
			}
		})
	}
}

func TestPutScheduleRejectsDays(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	rec := do(s, http.MethodPut, "/api/schedule", `{"daily":"3600","days":{"2026-11-14":"30000:120"}}`,
		withCookie(c), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "/api/sessions") {
		t.Errorf("code %d body %q", rec.Code, rec.Body.String())
	}
	if d, _ := s.Store.Daily(); d != goodSchedule {
		t.Errorf("lịch bị đổi: %q", d)
	}
	// days rỗng vẫn được chấp nhận (UI cũ gửi kèm).
	rec = do(s, http.MethodPut, "/api/schedule", `{"daily":"3600","days":{}}`,
		withCookie(c), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusOK {
		t.Errorf("days rỗng: code %d", rec.Code)
	}
}

func TestPutScheduleRequiresJSONContentType(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		rec := do(s, http.MethodPut, "/api/schedule", `{"daily":"3600"}`, withCookie(c), withHeader("Content-Type", ct))
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("content-type %q: code %d, want 415", ct, rec.Code)
		}
	}
	if d, _ := s.Store.Daily(); d != goodSchedule {
		t.Errorf("lịch bị đổi: %q", d)
	}
}

func TestPutScheduleCreatesMissing(t *testing.T) {
	s, _, _ := newUIServer(t, "")
	c := mustLogin(t, s)
	if rec := do(s, http.MethodGet, "/api/schedule", "", withCookie(c)); rec.Code != http.StatusNotFound {
		t.Errorf("GET khi chưa có lịch: code %d, want 404", rec.Code)
	}
	rec := do(s, http.MethodPut, "/api/schedule", `{"daily":"25200"}`, withCookie(c), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
	if d, _ := s.Store.Daily(); d != "25200" {
		t.Errorf("DB = %q", d)
	}
}

func heartbeatWithEvents(types ...string) string {
	evs := make([]string, len(types))
	for i, typ := range types {
		evs[i] = fmt.Sprintf(`{"type":%q,"at":"2026-11-12T07:%02d:00+07:00","source":"normal"}`, typ, i)
	}
	return `{"id":"` + testID + `","fw":"0.2.0","pump_on":false,"events":[` + strings.Join(evs, ",") + `]}`
}

func TestEvents(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	post(s, "Bearer "+testToken, heartbeatWithEvents("start", "stop"))
	post(s, "Bearer "+testToken, heartbeatWithEvents("skipped"))

	get := func(q string) (int, []map[string]any) {
		rec := do(s, http.MethodGet, "/api/events"+q, "", withCookie(c))
		var evs []map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &evs)
		return rec.Code, evs
	}
	code, evs := get("")
	if code != http.StatusOK || len(evs) != 3 {
		t.Fatalf("code %d, %d events", code, len(evs))
	}
	if evs[0]["type"] != "skipped" || evs[1]["type"] != "stop" || evs[2]["type"] != "start" {
		t.Errorf("thứ tự sai (mới nhất trước): %v", evs)
	}
	if _, evs := get("?limit=2"); len(evs) != 2 || evs[0]["type"] != "skipped" {
		t.Errorf("limit=2: %v", evs)
	}
	for _, bad := range []string{"?limit=0", "?limit=-1", "?limit=abc"} {
		if code, _ := get(bad); code != http.StatusBadRequest {
			t.Errorf("%s: code %d, want 400", bad, code)
		}
	}
}

func TestEventsDefaultAndMaxLimit(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	evs := make([]json.RawMessage, 1200)
	for i := range evs {
		evs[i] = json.RawMessage(fmt.Sprintf(`{"type":"start","at":"%d","source":"normal"}`, i))
	}
	if err := s.Store.RecordHeartbeat(s.now(), testID, []byte(`{"id":"`+testID+`"}`), evs); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (int, string) {
		rec := do(s, http.MethodGet, "/api/events"+q, "", withCookie(c))
		var got []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return len(got), got[0]["at"].(string)
	}
	if n, first := count(""); n != 100 || first != "1199" {
		t.Errorf("mặc định: %d events, đầu %v", n, first)
	}
	if n, _ := count("?limit=5000"); n != 1000 {
		t.Errorf("limit=5000: %d events, want 1000", n)
	}
}

func TestEventsEmpty(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	rec := do(s, http.MethodGet, "/api/events", "", withCookie(c))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("code %d body %q", rec.Code, rec.Body.String())
	}
}

type stateResp struct {
	Now           string          `json:"now"`
	Today         string          `json:"today"`
	Online        bool            `json:"online"`
	ReceivedAt    string          `json:"received_at"`
	Heartbeat     json.RawMessage `json:"heartbeat"`
	Schedule      *Schedule       `json:"schedule"`
	ScheduleError string          `json:"schedule_error"`
	TodayBody     *string         `json:"today_body"`
}

func getState(t *testing.T, s *Server, c *http.Cookie) stateResp {
	t.Helper()
	rec := do(s, http.MethodGet, "/api/state", "", withCookie(c))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/state: code %d", rec.Code)
	}
	var st stateResp
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return st
}

func TestStateOnlineOffline(t *testing.T) {
	s, _, now := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)

	st := getState(t, s, c)
	if st.Online || string(st.Heartbeat) != "null" || st.ReceivedAt != "" {
		t.Errorf("chưa có heartbeat: %+v", st)
	}
	if st.Today != "2026-11-12" || st.TodayBody == nil || *st.TodayBody != "25200:900,61200" {
		t.Errorf("lịch hôm nay: %+v", st)
	}
	if st.Schedule == nil || st.Schedule.Daily != "25200:900,61200" {
		t.Errorf("schedule: %+v", st.Schedule)
	}

	post(s, "Bearer "+testToken, sampleHeartbeat)
	st = getState(t, s, c)
	if !st.Online || st.ReceivedAt != "2026-11-12T08:00:00+07:00" {
		t.Errorf("sau heartbeat: online=%v received_at=%q", st.Online, st.ReceivedAt)
	}
	if string(st.Heartbeat) != sampleHeartbeat {
		t.Errorf("heartbeat phải giữ nguyên JSON: %s", st.Heartbeat)
	}

	*now = now.Add(15 * time.Minute)
	if st := getState(t, s, c); !st.Online {
		t.Error("15 phút: vẫn online")
	}
	*now = now.Add(time.Second)
	if st := getState(t, s, c); st.Online {
		t.Error("15 phút 1 giây: phải offline")
	}
}

func TestStateScheduleError(t *testing.T) {
	s, _, _ := newUIServer(t, "")
	c := mustLogin(t, s)
	st := getState(t, s, c)
	if st.Schedule != nil || st.ScheduleError == "" || st.TodayBody != nil {
		t.Errorf("lịch lỗi: %+v", st)
	}
}

func TestLoadLastHeartbeatFromDB(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	old := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 11, 12, 0, 50, 0, 0, time.UTC) // 07:50 giờ VN
	if err := s.Store.RecordHeartbeat(old, "old", []byte(`{"id":"old"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordHeartbeat(recent, testID, []byte(`{"id":"`+testID+`","fw":"0.2.0"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadLastHeartbeat(); err != nil {
		t.Fatal(err)
	}
	c := mustLogin(t, s)
	st := getState(t, s, c)
	if st.ReceivedAt != "2026-11-12T07:50:00+07:00" || !st.Online {
		t.Errorf("received_at=%q online=%v", st.ReceivedAt, st.Online)
	}
	var hb struct{ ID, FW string }
	if err := json.Unmarshal(st.Heartbeat, &hb); err != nil || hb.ID != testID || hb.FW != "0.2.0" {
		t.Errorf("heartbeat = %.80s", st.Heartbeat)
	}
}

func TestLoadLastHeartbeatEmptyDB(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	if err := s.LoadLastHeartbeat(); err != nil {
		t.Errorf("DB trống phải không lỗi: %v", err)
	}
}

func TestPageSecurityHeaders(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	c := mustLogin(t, s)
	rec := do(s, http.MethodGet, "/", "", withCookie(c))
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestOtherPathsAndMethods(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	if rec := do(s, http.MethodGet, "/nope", ""); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /nope: code %d", rec.Code)
	}
	if rec := do(s, http.MethodDelete, "/", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /: code %d, want 405", rec.Code)
	}
}

// Không chạy sau Cloudflare (TrustCFHeader tắt): header CF-Connecting-IP do client tự đặt phải bị bỏ
// qua, nếu không kẻ dò mật khẩu đổi header mỗi lần là né được giới hạn.
func TestLoginRateLimitIgnoresSpoofedCFHeaderWhenUntrusted(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	attacker := withRemote("203.0.113.9:4444")
	for i := 0; i < 5; i++ {
		login(s, "sai", attacker, withHeader("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", i)))
	}
	if rec := login(s, testPassword, attacker, withHeader("CF-Connecting-IP", "198.51.100.99")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("đổi CF-Connecting-IP khi không tin header: code %d, want 429", rec.Code)
	}
}

// Map đầy: IP mới vẫn đăng nhập được (bỏ mục cũ nhất), không khoá tất cả mọi người.
func TestLoginFailMapFullEvictsOldest(t *testing.T) {
	s, _, now := newUIServer(t, goodSchedule)
	for i := 0; i < maxLoginIPs; i++ {
		login(s, "sai", withRemote(fmt.Sprintf("10.%d.%d.%d:1", i>>16&255, i>>8&255, i&255)))
		if i == 0 {
			*now = now.Add(time.Second) // mục đầu tiên là cũ nhất
		}
	}
	if rec := login(s, testPassword, withRemote("192.0.2.77:1")); rec.Code != http.StatusSeeOther {
		t.Errorf("IP mới khi map đầy: code %d, want 303", rec.Code)
	}
	s.loginMu.Lock()
	n := len(s.loginFails)
	s.loginMu.Unlock()
	if n > maxLoginIPs {
		t.Errorf("map vượt giới hạn: %d", n)
	}
}

// IPv6 cùng /64 tính là một nguồn: đổi địa chỉ trong dải /64 không né được giới hạn.
func TestLoginRateLimitGroupsIPv6By64(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	for i := 0; i < 5; i++ {
		login(s, "sai", withRemote(fmt.Sprintf("[2001:db8:1:2::%x]:443", i+1)))
	}
	if rec := login(s, testPassword, withRemote("[2001:db8:1:2::ffff]:443")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("cùng /64: code %d, want 429", rec.Code)
	}
	if rec := login(s, testPassword, withRemote("[2001:db8:1:3::1]:443")); rec.Code != http.StatusSeeOther {
		t.Errorf("/64 khác: code %d, want 303", rec.Code)
	}
}

// Map đầy không được đẩy mục đang bị khoá ra ngoài (nếu không, lấp map bằng IP khác là gỡ được khoá).
func TestLoginFailMapFullKeepsLockedEntries(t *testing.T) {
	s, _, now := newUIServer(t, goodSchedule)
	locked := withRemote("198.51.100.7:1")
	for i := 0; i < maxLoginFailures; i++ {
		login(s, "sai", locked)
	}
	*now = now.Add(time.Second)
	for i := 0; i < maxLoginIPs+50; i++ {
		login(s, "sai", withRemote(fmt.Sprintf("10.%d.%d.%d:1", i>>16&255, i>>8&255, i&255)))
	}
	if rec := login(s, testPassword, locked); rec.Code != http.StatusTooManyRequests {
		t.Errorf("IP đã khoá sau khi lấp map: code %d, want 429", rec.Code)
	}
}

// Map đầy toàn mục bị khoá (đang bị dò hàng loạt): nguồn mới nhập ĐÚNG mật khẩu vẫn vào được —
// kẻ xấu không khoá được chủ nhà. Sai thì bị trễ (loginSlowdown) chứ không bị ghi.
func TestLoginMapFullOfLockedStillAdmitsCorrectPassword(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	s.loginSlowdown = func() {}
	slowed := 0
	for i := 0; i < maxLoginIPs; i++ {
		for j := 0; j < maxLoginFailures; j++ {
			login(s, "sai", withRemote(fmt.Sprintf("10.%d.%d.%d:1", i>>16&255, i>>8&255, i&255)))
		}
	}
	s.loginSlowdown = func() { slowed++ }
	if rec := login(s, "sai", withRemote("192.0.2.50:1")); rec.Code != http.StatusUnauthorized {
		t.Errorf("nguồn mới sai mật khẩu: code %d, want 401", rec.Code)
	}
	if slowed != 1 {
		t.Errorf("nguồn mới sai khi map đầy phải bị làm chậm, slowed=%d", slowed)
	}
	if rec := login(s, testPassword, withRemote("192.0.2.51:1")); rec.Code != http.StatusSeeOther {
		t.Errorf("chủ nhà đúng mật khẩu khi map đầy: code %d, want 303", rec.Code)
	}
}

// Map đầy: các lần sai của nguồn chưa ghi nhận bị tuần tự hoá toàn cục — đang có một lần sai chờ
// trễ thì lần sai song song khác nhận 429 ngay (không né được bằng cách gửi song song).
func TestLoginUntrackedFailuresSerialized(t *testing.T) {
	s, _, _ := newUIServer(t, goodSchedule)
	s.loginSlowdown = func() {}
	for i := 0; i < maxLoginIPs; i++ {
		for j := 0; j < maxLoginFailures; j++ {
			login(s, "sai", withRemote(fmt.Sprintf("10.%d.%d.%d:1", i>>16&255, i>>8&255, i&255)))
		}
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.loginSlowdown = func() { close(entered); <-release }
	done := make(chan int)
	go func() { done <- login(s, "sai", withRemote("192.0.2.60:1")).Code }()
	<-entered
	if rec := login(s, "sai", withRemote("192.0.2.61:1")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("lần sai song song: code %d, want 429", rec.Code)
	}
	// Không được lộ mật khẩu đúng/sai qua mã trả về: đang có lượt chờ ⇒ 429 cả khi mật khẩu đúng.
	if rec := login(s, testPassword, withRemote("192.0.2.62:1")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("đúng mật khẩu trong lúc có lượt đang chờ: code %d, want 429 (không lộ kết quả)", rec.Code)
	}
	close(release)
	if code := <-done; code != http.StatusUnauthorized {
		t.Errorf("lần sai đầu: code %d, want 401", code)
	}
}
