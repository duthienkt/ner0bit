package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testToken = "s3cret"
	testID    = "a4cf12b8e901"
)

const sampleHeartbeat = `{"id":"a4cf12b8e901","fw":"0.1.0","uptime_s":123456,"rssi":-58,"pump_on":false,` +
	`"rtc":"2026-11-10T15:05:00+07:00","events":[` +
	`{"start":"2026-11-10T07:00:00+07:00","stop":"2026-11-10T07:05:00+07:00","source":"today"},` +
	`{"start":"2026-11-10T11:35:00+07:00","source":"today","skipped":"rest"}]}`

func newTestServer(t *testing.T, schedule string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	if schedule != "" {
		if err := os.WriteFile(filepath.Join(dir, "schedule.json"), []byte(schedule), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
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

const goodSchedule = `{"daily":"25200:900,61200","days":{"2026-11-12":"28800"}}`

func TestHeartbeatOK(t *testing.T) {
	s, dir := newTestServer(t, goodSchedule)
	rec := post(s, "Bearer "+testToken, sampleHeartbeat)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "25200:900,61200\n28800" {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}

	hb := readLines(t, filepath.Join(dir, "heartbeats.jsonl"))
	if len(hb) != 1 {
		t.Fatalf("heartbeats.jsonl có %d dòng", len(hb))
	}
	var rec1 map[string]any
	if err := json.Unmarshal([]byte(hb[0]), &rec1); err != nil {
		t.Fatal(err)
	}
	if rec1["received_at"] == nil || rec1["heartbeat"] == nil {
		t.Errorf("thiếu trường: %s", hb[0])
	}

	ev := readLines(t, filepath.Join(dir, "events.jsonl"))
	if len(ev) != 2 {
		t.Fatalf("events.jsonl có %d dòng", len(ev))
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(ev[1]), &e); err != nil {
		t.Fatal(err)
	}
	if e["id"] != testID || e["skipped"] != "rest" || e["received_at"] == nil {
		t.Errorf("event sai: %s", ev[1])
	}
}

func TestHeartbeatAuth(t *testing.T) {
	s, dir := newTestServer(t, goodSchedule)
	for _, auth := range []string{"", "Bearer wrong", "s3cret", "Basic s3cret", "Bearer "} {
		if rec := post(s, auth, sampleHeartbeat); rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: code %d, want 401", auth, rec.Code)
		}
	}
	other := strings.Replace(sampleHeartbeat, testID, "ffffffffffff", 1)
	if rec := post(s, "Bearer "+testToken, other); rec.Code != http.StatusForbidden {
		t.Errorf("id lạ: code %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "heartbeats.jsonl")); err == nil {
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
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code %d", rec.Code)
	}
}

func TestScheduleBrokenReturns503AndLogsNothing(t *testing.T) {
	for name, sched := range map[string]string{"missing": "", "invalid": `{"daily":"abc"}`} {
		t.Run(name, func(t *testing.T) {
			s, dir := newTestServer(t, sched)
			rec := post(s, "Bearer "+testToken, sampleHeartbeat)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("code %d, want 503", rec.Code)
			}
			// ESP không xoá events khi ≠ 200 và sẽ gửi lại ⇒ server không được ghi để tránh trùng.
			if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err == nil {
				t.Error("không được ghi events khi trả 503")
			}
		})
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
	if resp.StatusCode != 200 || string(body) != "25200:900,61200\n28800" {
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

func readLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
