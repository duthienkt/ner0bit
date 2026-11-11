package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const threeSlots = "18900:900,22500:900,62100:900"

// today của newUIServer là 2026-11-12 (giờ VN).
const testToday = "2026-11-12"

func TestRenderLineSortsAndDefaults(t *testing.T) {
	if got := renderLine(parseLineEntries("61200,18000:900")); got != "18000:900,61200:300" {
		t.Errorf("got %q", got)
	}
}

func TestDeviceBodyAutoOffNoManual(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	body, err := s.deviceBody(*now)
	if err != nil || body != threeSlots || strings.Contains(body, "\n") {
		t.Errorf("body %q err %v", body, err)
	}
}

func TestDeviceBodyAutoOffWithManual(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	if _, err := s.Store.AddManualSession(testToday, 34560, 300, ""); err != nil {
		t.Fatal(err)
	}
	body, err := s.deviceBody(*now)
	want := threeSlots + "\n18900:900,22500:900,34560:300,62100:900"
	if err != nil || body != want {
		t.Errorf("body %q err %v", body, err)
	}
	// Heartbeat trả đúng body đó.
	if rec := post(s, "Bearer "+testToken, sampleHeartbeat); rec.Body.String() != want {
		t.Errorf("heartbeat trả %q", rec.Body.String())
	}
}

func TestDeviceBodyManualOtherDayIgnored(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	if _, err := s.Store.AddManualSession("2026-11-13", 34560, 300, ""); err != nil {
		t.Fatal(err)
	}
	if body, _ := s.deviceBody(*now); body != threeSlots {
		t.Errorf("body %q", body)
	}
}

func TestDeviceBodyDeletedManualIgnored(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	sess, err := s.Store.AddManualSession(testToday, 34560, 300, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DeleteManualSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if body, _ := s.deviceBody(*now); body != threeSlots {
		t.Errorf("body %q", body)
	}
	if err := s.Store.DeleteManualSession(sess.ID); err == nil {
		t.Error("xoá lần hai phải lỗi")
	}
}

func TestAddManualSessionValidation(t *testing.T) {
	// 17 mốc daily + 3 manual = 20; mốc thứ 21 vượt.
	full := strings.TrimSuffix(strings.Repeat("100:60,", 17), ",")
	cases := []struct {
		name, daily, body string
		want              int
		warn              bool
	}{
		{"past date", threeSlots, `{"date":"2026-11-11","start_s":34560,"run_s":300}`, 400, false},
		{"bad date", threeSlots, `{"date":"x","start_s":34560,"run_s":300}`, 400, false},
		{"start negative", threeSlots, `{"date":"2026-11-12","start_s":-1,"run_s":300}`, 400, false},
		{"start 86400", threeSlots, `{"date":"2026-11-12","start_s":86400,"run_s":300}`, 400, false},
		{"run 0", threeSlots, `{"date":"2026-11-12","start_s":34560,"run_s":0}`, 400, false},
		{"run 1801", threeSlots, `{"date":"2026-11-12","start_s":34560,"run_s":1801}`, 400, false},
		{"over 20", full + ",200:60,300:60,400:60", `{"date":"2026-11-12","start_s":34560,"run_s":300}`, 400, false},
		{"ok", threeSlots, `{"date":"2026-11-12","start_s":34560,"run_s":300,"note":"thử"}`, 201, false},
		{"ok today tomorrow", threeSlots, `{"date":"2026-11-13","start_s":34560,"run_s":1800}`, 201, false},
		{"close to slot", threeSlots, `{"date":"2026-11-12","start_s":19200,"run_s":300}`, 201, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _, _ := newUIServer(t, c.daily)
			ck := mustLogin(t, s)
			rec := do(s, http.MethodPost, "/api/sessions", c.body, withCookie(ck), withHeader("Content-Type", "application/json"))
			if rec.Code != c.want {
				t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Session  Session  `json:"session"`
				Warnings []string `json:"warnings"`
				Error    string   `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if c.want == 400 && resp.Error == "" {
				t.Error("thiếu thông báo lỗi")
			}
			if c.want == 201 && (resp.Session.ID == 0 || resp.Session.Source != "manual") {
				t.Errorf("session %+v", resp.Session)
			}
			if got := len(resp.Warnings) > 0; c.want == 201 && got != c.warn {
				t.Errorf("warnings %v, want warn=%v", resp.Warnings, c.warn)
			}
		})
	}
}

func TestAddSessionOver20CountsManual(t *testing.T) {
	s, _, _ := newUIServer(t, strings.TrimSuffix(strings.Repeat("100:60,", 19), ","))
	if _, err := s.Store.AddManualSession(testToday, 5000, 60, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.AddManualSession(testToday, 6000, 60, ""); err == nil {
		t.Error("mốc thứ 21 phải bị từ chối")
	}
	if _, err := s.Store.AddManualSession("2026-11-13", 6000, 60, ""); err != nil {
		t.Errorf("ngày khác không bị ảnh hưởng: %v", err)
	}
}

func TestSessionsRoutesNeedLoginAndJSON(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	if rec := do(s, http.MethodPost, "/api/sessions", `{}`, withHeader("Content-Type", "application/json")); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST chưa đăng nhập: %d", rec.Code)
	}
	if rec := do(s, http.MethodDelete, "/api/sessions/1", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("DELETE chưa đăng nhập: %d", rec.Code)
	}
	ck := mustLogin(t, s)
	if rec := do(s, http.MethodPost, "/api/sessions", `{}`, withCookie(ck), withHeader("Content-Type", "text/plain")); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST sai content-type: %d", rec.Code)
	}
}

func TestDeleteSessionHTTP(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	ck := mustLogin(t, s)
	rec := do(s, http.MethodPost, "/api/sessions", `{"date":"2026-11-12","start_s":34560,"run_s":300}`, withCookie(ck), withHeader("Content-Type", "application/json"))
	if rec.Code != 201 {
		t.Fatalf("%d", rec.Code)
	}
	if st := getState(t, s, ck); st.TodayBody == nil || !strings.Contains(*st.TodayBody, "\n") {
		t.Errorf("today_body %v", st.TodayBody)
	}
	if rec := do(s, http.MethodDelete, "/api/sessions/1", "", withCookie(ck)); rec.Code != 200 {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(s, http.MethodDelete, "/api/sessions/1", "", withCookie(ck)); rec.Code != 404 {
		t.Errorf("delete lần hai: %d", rec.Code)
	}
	if body, _ := s.deviceBody(*now); body != threeSlots {
		t.Errorf("body %q", body)
	}
}

func TestGetScheduleShape(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	ck := mustLogin(t, s)
	rec := do(s, http.MethodGet, "/api/schedule", "", withCookie(ck))
	if strings.TrimSpace(rec.Body.String()) != `{"daily":"`+threeSlots+`"}` {
		t.Errorf("GET /api/schedule = %q", rec.Body.String())
	}
}

func TestPutScheduleRejectsOverflowWithManual(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	ck := mustLogin(t, s)
	for i := 0; i < 3; i++ {
		if _, err := s.Store.AddManualSession("2026-11-13", 40000+i*1000, 60, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Phiên ngày đã qua không tính.
	if _, err := s.Store.db.Exec(`INSERT INTO sessions(date,start_s,run_s,source,state,note,created_at,updated_at)
		VALUES('2026-11-01',1,60,'manual','final','','x','x')`); err != nil {
		t.Fatal(err)
	}
	put := func(daily string) *httptest.ResponseRecorder {
		return do(s, http.MethodPut, "/api/schedule", `{"daily":"`+daily+`"}`, withCookie(ck), withHeader("Content-Type", "application/json"))
	}
	over := strings.TrimSuffix(strings.Repeat("100:60,", 18), ",") // 18 + 3 > 20
	rec := put(over)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "2026-11-13") {
		t.Errorf("code %d body %s", rec.Code, rec.Body.String())
	}
	if d, _ := s.Store.Daily(); d != threeSlots {
		t.Errorf("lịch bị đổi: %q", d)
	}
	if rec := put(strings.TrimSuffix(strings.Repeat("100:60,", 17), ",")); rec.Code != 200 { // 17 + 3 = 20
		t.Errorf("vừa đủ 20: code %d %s", rec.Code, rec.Body.String())
	}
}
