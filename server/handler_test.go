package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testToken = "s3cret"
	testID    = "a4cf12b8e901"
)

// Đúng định dạng firmware gửi (không khoảng trắng, thứ tự khoá như docs/system-design.md).
const sampleHeartbeat = `{"id":"a4cf12b8e901","fw":"0.1.0","uptime_s":123456,"rssi":-58,"pump_on":false,` +
	`"heap_free":182344,"heap_min":151020,"ds3231":"ok","at24c32":"ok","temp_c":27.25,"queue":3,` +
	`"rtc":"2026-11-10T15:05:00+07:00","events":[` +
	`{"type":"start","at":"2026-11-10T07:00:00+07:00","source":"today"},` +
	`{"type":"stop","at":"2026-11-10T07:05:00+07:00","source":"today","reason":"done"},` +
	`{"type":"skipped","at":"2026-11-10T07:08:00+07:00","source":"today","reason":"rest"}]}`

func newTestServer(t *testing.T, schedule string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if schedule != "" {
		if err := st.SetDaily(schedule); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		Store:    st,
		Token:    testToken,
		DeviceID: testID,
		DataDir:  dir,
		// 2026-11-12 08:00 giờ VN
		Now: func() time.Time { return time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC) },
	}
	return s, dir
}

func post(s *Server, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// Dòng 1 (lịch dự phòng) cho newTestServer; rỗng ⇒ chưa đặt lịch.
const goodSchedule = "25200:900,61200"

func TestHeartbeatOK(t *testing.T) {
	s, _ := newTestServer(t, goodSchedule)
	rec := post(s, "Bearer "+testToken, sampleHeartbeat)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "25200:900,61200" {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}

	_, raw, ok, err := s.Store.LastHeartbeat()
	if err != nil || !ok || string(raw) != sampleHeartbeat {
		t.Errorf("heartbeat lưu = %s ok=%v err=%v", raw, ok, err)
	}
	ev, err := s.Store.Events(10)
	if err != nil || len(ev) != 3 {
		t.Fatalf("events = %d, err %v", len(ev), err)
	}
	var e map[string]any
	if err := json.Unmarshal(ev[0], &e); err != nil {
		t.Fatal(err)
	}
	if e["id"] != testID || e["type"] != "skipped" || e["reason"] != "rest" || e["received_at"] == nil {
		t.Errorf("event sai: %s", ev[0])
	}
}

func TestHeartbeatAuth(t *testing.T) {
	s, _ := newTestServer(t, goodSchedule)
	for _, auth := range []string{"", "Bearer wrong", "s3cret", "Basic s3cret", "Bearer "} {
		if rec := post(s, auth, sampleHeartbeat); rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: code %d, want 401", auth, rec.Code)
		}
	}
	other := strings.Replace(sampleHeartbeat, testID, "ffffffffffff", 1)
	if rec := post(s, "Bearer "+testToken, other); rec.Code != http.StatusForbidden {
		t.Errorf("id lạ: code %d, want 403", rec.Code)
	}
	if _, _, ok, _ := s.Store.LastHeartbeat(); ok {
		t.Error("request bị từ chối không được ghi log")
	}
}

func TestHeartbeatBadRequests(t *testing.T) {
	s, _ := newTestServer(t, goodSchedule)
	if rec := post(s, "Bearer "+testToken, "{not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("json hỏng: code %d", rec.Code)
	}
	if rec := post(s, "Bearer "+testToken, `{"fw":"0.1.0"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("thiếu id: code %d", rec.Code)
	}
	big := `{"id":"` + testID + `","x":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	if rec := post(s, "Bearer "+testToken, big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("body quá lớn: code %d", rec.Code)
	}
	// GET / giờ là trang UI; method lạ vẫn 405.
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: code %d", rec.Code)
	}
}

func TestScheduleMissingReturns503AndLogsNothing(t *testing.T) {
	s, _ := newTestServer(t, "")
	rec := post(s, "Bearer "+testToken, sampleHeartbeat)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d, want 503", rec.Code)
	}
	// ESP không xoá events khi ≠ 200 và sẽ gửi lại ⇒ server không được ghi để tránh trùng.
	if _, _, ok, _ := s.Store.LastHeartbeat(); ok {
		t.Error("không được ghi heartbeat khi trả 503")
	}
	if ev, _ := s.Store.Events(10); len(ev) != 0 {
		t.Error("không được ghi events khi trả 503")
	}
}

// Lỗi DB (đọc lịch hoặc ghi) ⇒ 503 để ESP giữ events và gửi lại; không panic.
func TestHeartbeatDBErrorReturns503(t *testing.T) {
	s, _ := newTestServer(t, goodSchedule)
	s.Store.Close()
	rec := post(s, "Bearer "+testToken, sampleHeartbeat)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d, want 503", rec.Code)
	}
}

// Firmware gửi HTTP/1.0 và đọc body thô: response phải có Content-Length, không chunked, có Date.
func TestHTTP10RawRequest(t *testing.T) {
	s, _ := newTestServer(t, goodSchedule)
	ts := httptest.NewServer(s)
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var req bytes.Buffer
	req.WriteString("POST / HTTP/1.0\r\n")
	req.WriteString("Host: example\r\n")
	req.WriteString("Content-Type: application/json\r\n")
	req.WriteString("Authorization: Bearer " + testToken + "\r\n")
	req.WriteString("Content-Length: " + itoa(len(sampleHeartbeat)) + "\r\n\r\n")
	req.WriteString(sampleHeartbeat)
	if _, err := conn.Write(req.Bytes()); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "25200:900,61200" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if len(resp.TransferEncoding) != 0 {
		t.Errorf("không được chunked: %v", resp.TransferEncoding)
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length = %d", resp.ContentLength)
	}
	if _, err := http.ParseTime(resp.Header.Get("Date")); err != nil {
		t.Errorf("Date header %q: %v", resp.Header.Get("Date"), err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
