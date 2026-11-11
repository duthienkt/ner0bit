package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- helper ----

func apiDo(t *testing.T, s *Server, c *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	opts := []reqOpt{withCookie(c)}
	if body == "" && method == http.MethodPost {
		body = "{}" // như UI: POST không dữ liệu vẫn gửi JSON
	}
	if body != "" {
		opts = append(opts, withHeader("Content-Type", "application/json"))
	}
	return do(s, method, path, body, opts...)
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("JSON %v: %s", err, rec.Body.String())
	}
}

type settingsResp struct {
	Params            Params  `json:"params"`
	QuotaS            int     `json:"quota_s"`
	PerMMS            float64 `json:"per_mm_s"`
	Daily             string  `json:"daily"`
	DailyMatchesSlots bool    `json:"daily_matches_slots"`
}

type dayResp struct {
	Date        string     `json:"date"`
	VDay        string     `json:"vday"`
	Sessions    []Session  `json:"sessions"`
	Provisional []Decision `json:"provisional"`
	DeviceBody  *string    `json:"device_body"`
	Ledger      Ledger     `json:"ledger"`
	SumMM       *float64   `json:"sum_mm"`
	Warnings    []string   `json:"warnings"`
}

func getSettings(t *testing.T, s *Server, c *http.Cookie) settingsResp {
	t.Helper()
	rec := apiDo(t, s, c, http.MethodGet, "/api/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings: %d %s", rec.Code, rec.Body.String())
	}
	var r settingsResp
	decodeJSON(t, rec, &r)
	return r
}

func getDay(t *testing.T, s *Server, c *http.Cookie, query string) dayResp {
	t.Helper()
	rec := apiDo(t, s, c, http.MethodGet, "/api/day"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/day%s: %d %s", query, rec.Code, rec.Body.String())
	}
	var r dayResp
	decodeJSON(t, rec, &r)
	return r
}

func paramsJSON(t *testing.T, p Params) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// decide05 chốt lần 05:15 ngày 2026-11-12 với mưa 6 mm (run 360 s, used_rain 540 s).
func decide05(t *testing.T, s *Server) {
	t.Helper()
	at := vt("2026-11-12", "05:09")
	if _, err := s.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "05:07"), FetchedAt: at, SumDepthMM: 6}); err != nil {
		t.Fatal(err)
	}
	p := DefaultParams()
	l, _, _ := s.Store.Ledger("2026-11-12")
	d, l2 := decide(l, p.Slots[0], 6, 1, p)
	if d.RunS != 360 {
		t.Fatalf("RunS %d", d.RunS)
	}
	if err := s.Store.SaveDecision("2026-11-12", d, l2, stateFinal, at); err != nil {
		t.Fatal(err)
	}
}

// ---- /api/settings ----

func TestSettingsValidation(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	mod := func(f func(p *Params)) string {
		p := DefaultParams()
		f(&p)
		return paramsJSON(t, p)
	}
	bad := []struct{ name, body string }{
		{"slot trước 04:15", mod(func(p *Params) { p.Slots[0].StartS = 4*3600 + 14*60 })},
		{"slot sau 18:45", mod(func(p *Params) { p.Slots[2].StartS = 18*3600 + 46*60 })},
		{"slot sát nhau", mod(func(p *Params) { p.Slots[1].StartS = p.Slots[0].StartS + 900 + 179 })},
		{"rain_hi = rain_lo", mod(func(p *Params) { p.RainLoMM, p.RainHiMM = 8, 8 })},
		{"rain_hi < rain_lo", mod(func(p *Params) { p.RainLoMM, p.RainHiMM = 8, 6 })},
		{"block 0", mod(func(p *Params) { p.BlockS = 0 })},
		{"block > 30 phút", mod(func(p *Params) { p.BlockS = 1801 })},
		{"buổi sai", mod(func(p *Params) { p.Slots[0].Phase = "noon" })},
		{"không có slot", mod(func(p *Params) { p.Slots = []Slot{} })},
		{"min_run > block", mod(func(p *Params) { p.MinRunS = 901 })},
		{"e_ref 0", mod(func(p *Params) { p.ERefMM = 0 })},
		{"JSON hỏng", `{"slots":`},
		{"trường lạ", `{"blok_s":900}`},
	}
	for _, b := range bad {
		rec := apiDo(t, s, c, http.MethodPut, "/api/settings", b.body)
		var e struct{ Error string }
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusBadRequest || e.Error == "" {
			t.Errorf("%s: code %d body %s, muốn 400 kèm lỗi", b.name, rec.Code, rec.Body.String())
		}
	}
	if got := getSettings(t, s, c).Params; paramsJSON(t, got) != paramsJSON(t, DefaultParams()) {
		t.Errorf("PUT lỗi không được đổi tham số: %+v", got)
	}
	if rec := do(s, http.MethodPut, "/api/settings", paramsJSON(t, DefaultParams()), withCookie(c),
		withHeader("Content-Type", "text/plain")); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("sai content-type: %d", rec.Code)
	}

	want := Params{
		Slots:     []Slot{{5 * 3600, "am"}, {6*3600 + 30*60, "am"}, {17 * 3600, "pm"}, {18*3600 + 30*60, "pm"}},
		BlockS:    600,
		MinRunS:   120,
		CarryMaxS: 1200,
		RainLoMM:  3,
		RainHiMM:  12,
		ERefMM:    4.5,
	}
	rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, want))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT hợp lệ: %d %s", rec.Code, rec.Body.String())
	}
	got := getSettings(t, s, c)
	if paramsJSON(t, got.Params) != paramsJSON(t, want) {
		t.Errorf("GET sau PUT:\n got %s\nwant %s", paramsJSON(t, got.Params), paramsJSON(t, want))
	}
	if got.QuotaS != 2400 || got.PerMMS != 2400.0/9 {
		t.Errorf("quota_s %d per_mm_s %v", got.QuotaS, got.PerMMS)
	}
	if got.Daily != threeSlots || got.DailyMatchesSlots {
		t.Errorf("daily %q matches %v", got.Daily, got.DailyMatchesSlots)
	}
}

func TestSettingsRejectsSlotsOverflowWithManual(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	// 3 mốc daily + 17 phiên bơm thử ngày mai = 20 (vừa đủ).
	for i := 0; i < 17; i++ {
		if _, err := s.Store.AddManualSession("2026-11-13", 30000+i*600, 60, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Ngày đã qua nhiều phiên cũng không chặn.
	for i := 0; i < 19; i++ {
		if _, err := insertManualSessionRaw(s.Store.db, "2026-11-10", 30000+i*600, 60, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultParams()
	p.Slots = []Slot{{18900, "am"}, {22500, "am"}, {26100, "am"}, {62100, "pm"}}
	rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "2026-11-13") {
		t.Errorf("4 slot + 17 phiên: code %d body %s, muốn 400 nêu ngày 2026-11-13", rec.Code, rec.Body.String())
	}
	if got := getSettings(t, s, c).Params; len(got.Slots) != 3 {
		t.Errorf("slots bị đổi: %+v", got.Slots)
	}
	p.Slots = p.Slots[:3]
	p.Slots[2] = Slot{62100, "pm"}
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Errorf("3 slot + 17 phiên = 20: code %d %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsEnableSetsAt(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	at10 := vt("2026-11-12", "10:00")
	*now = at10
	p := DefaultParams()
	p.AutoEnabled = true
	p.AutoEnabledAt = vt("2020-01-01", "00:00") // client gửi ⇒ bị bỏ qua
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("bật: %d %s", rec.Code, rec.Body.String())
	}
	stored, err := s.Store.Params()
	if err != nil || !stored.AutoEnabled || !stored.AutoEnabledAt.Equal(at10) {
		t.Errorf("Store.Params sau khi bật: %+v %v, muốn auto_enabled_at = %v", stored, err, at10)
	}
	if got := getSettings(t, s, c).Params; !got.AutoEnabledAt.Equal(at10) {
		t.Errorf("GET auto_enabled_at = %v, muốn %v", got.AutoEnabledAt, at10)
	}

	// Đang bật mà lưu lại (đổi tham số khác): giữ mốc bật cũ, vẫn bỏ qua giá trị client gửi.
	*now = vt("2026-11-12", "11:00")
	p.AutoEnabledAt = vt("2026-11-12", "04:00")
	p.MinRunS = 240
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("lưu lại: %d %s", rec.Code, rec.Body.String())
	}
	if got := getSettings(t, s, c).Params; !got.AutoEnabledAt.Equal(at10) || got.MinRunS != 240 {
		t.Errorf("true→true: auto_enabled_at %v min_run %d, muốn %v và 240", got.AutoEnabledAt, got.MinRunS, at10)
	}

	// Tắt rồi bật lại lúc 13:00 ⇒ mốc mới.
	p.AutoEnabled = false
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("tắt: %d", rec.Code)
	}
	if got := getSettings(t, s, c).Params; got.AutoEnabled {
		t.Error("vẫn bật sau khi tắt")
	}
	at13 := vt("2026-11-12", "13:00")
	*now = at13
	p.AutoEnabled = true
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("bật lại: %d", rec.Code)
	}
	if got := getSettings(t, s, c).Params; !got.AutoEnabledAt.Equal(at13) {
		t.Errorf("bật lại: auto_enabled_at %v, muốn %v", got.AutoEnabledAt, at13)
	}
}

func TestSettingsChangeMidDayKeepsDecided(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	*now = vt("2026-11-12", "05:30")

	p := DefaultParams()
	p.AutoEnabled = true
	p.Slots = []Slot{{18900, "am"}, {23400, "am"}, {62100, "pm"}} // 06:15 → 06:30
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	d := getDay(t, s, c, "")
	var autos []Session
	for _, x := range d.Sessions {
		if x.Source == sessionAuto {
			autos = append(autos, x)
		}
	}
	if len(autos) != 1 || autos[0].StartS != 18900 || autos[0].RunS != 360 || autos[0].State != stateFinal {
		t.Errorf("phiên đã chốt: %+v", autos)
	}
	var starts []int
	for _, x := range d.Provisional {
		starts = append(starts, x.Slot.StartS)
	}
	if fmt.Sprint(starts) != "[23400 62100]" {
		t.Errorf("provisional %v, muốn [23400 62100]", starts)
	}
	if want := threeSlots + "\n18900:360,23400:900,62100:900"; d.DeviceBody == nil || *d.DeviceBody != want {
		t.Errorf("device_body %v, muốn %q", d.DeviceBody, want)
	}
	if d.Ledger.UsedRainS != 540 {
		t.Errorf("ledger %+v", d.Ledger)
	}
}

func TestDailyFromSlots(t *testing.T) {
	s, _, _ := newUIServer(t, "25200:900,61200")
	c := mustLogin(t, s)
	if st := getSettings(t, s, c); st.DailyMatchesSlots || st.Daily != "25200:900,61200" {
		t.Errorf("trước: %+v", st)
	}
	rec := apiDo(t, s, c, http.MethodPost, "/api/settings/daily-from-slots", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("daily-from-slots: %d %s", rec.Code, rec.Body.String())
	}
	if d, _ := s.Store.Daily(); d != "18900:900,22500:900,62100:900" {
		t.Errorf("daily = %q", d)
	}
	if st := getSettings(t, s, c); !st.DailyMatchesSlots || st.Daily != "18900:900,22500:900,62100:900" {
		t.Errorf("sau: %+v", st)
	}
}

// ---- huỷ phiên auto ----

func TestCancelAutoSession(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	*now = vt("2026-11-12", "05:12") // đã chốt 05:15, chưa tới giờ tưới
	before, _, _ := s.Store.Ledger("2026-11-12")

	auto := autoSessions(t, s.Store, "2026-11-12")
	if len(auto) != 1 {
		t.Fatalf("phiên auto: %+v", auto)
	}
	if d := getDay(t, s, c, ""); d.DeviceBody == nil || !strings.Contains(*d.DeviceBody, "\n18900:360,") {
		t.Fatalf("trước khi huỷ: %v", d.DeviceBody)
	}
	path := fmt.Sprintf("/api/sessions/%d/cancel", auto[0].ID)
	if rec := apiDo(t, s, c, http.MethodPost, path, ""); rec.Code != http.StatusOK {
		t.Fatalf("huỷ: %d %s", rec.Code, rec.Body.String())
	}
	d := getDay(t, s, c, "")
	if want := threeSlots + "\n22500:900,62100:900"; d.DeviceBody == nil || *d.DeviceBody != want {
		t.Errorf("sau huỷ device_body %v, muốn %q", d.DeviceBody, want)
	}
	if got := autoSessions(t, s.Store, "2026-11-12"); got[0].State != stateCancelled || got[0].RunS != 360 {
		t.Errorf("phiên sau huỷ: %+v", got[0])
	}
	if after, _, _ := s.Store.Ledger("2026-11-12"); after != before {
		t.Errorf("sổ bị đổi: %+v → %+v", before, after)
	}
	if rec := apiDo(t, s, c, http.MethodPost, path, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("huỷ lần hai: %d, muốn 400", rec.Code)
	}

	man, err := s.Store.AddManualSession("2026-11-12", 34560, 300, "thử")
	if err != nil {
		t.Fatal(err)
	}
	if rec := apiDo(t, s, c, http.MethodPost, fmt.Sprintf("/api/sessions/%d/cancel", man.ID), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("huỷ phiên manual: %d, muốn 400", rec.Code)
	}

	// Phiên auto của ngày đã qua.
	p := DefaultParams()
	l, _, _ := s.Store.Ledger("2026-11-11")
	dd, l2 := decide(l, p.Slots[2], 0, 1, p)
	if err := s.Store.SaveDecision("2026-11-11", dd, l2, stateFinal, vt("2026-11-11", "17:09")); err != nil {
		t.Fatal(err)
	}
	past := autoSessions(t, s.Store, "2026-11-11")
	if rec := apiDo(t, s, c, http.MethodPost, fmt.Sprintf("/api/sessions/%d/cancel", past[0].ID), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("huỷ phiên ngày đã qua: %d, muốn 400", rec.Code)
	}
	if got := autoSessions(t, s.Store, "2026-11-11"); got[0].State != stateFinal {
		t.Errorf("phiên ngày đã qua bị đổi: %+v", got[0])
	}

	if rec := apiDo(t, s, c, http.MethodPost, "/api/sessions/9999/cancel", ""); rec.Code != http.StatusNotFound {
		t.Errorf("id không có: %d, muốn 404", rec.Code)
	}
	if rec := apiDo(t, s, c, http.MethodPost, "/api/sessions/abc/cancel", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("id hỏng: %d, muốn 400", rec.Code)
	}
}

// ---- /api/day, /api/state ----

func TestDayEndpointShape(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	if _, err := s.Store.AddManualSession(testToday, 34560, 300, "thử"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.AddManualSession(testToday, 40000, 300, "xoá"); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DeleteManualSession(2); err != nil {
		t.Fatal(err)
	}
	s.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "07:07"), FetchedAt: vt("2026-11-12", "07:09"), SumDepthMM: 2.4})
	s.Store.AddWarning(vt("2026-11-12", "07:11"), "vrain", "Vrain trễ: thử")
	s.Store.AddWarning(vt("2026-11-11", "07:11"), "vrain", "cảnh báo hôm qua")

	rec := apiDo(t, s, c, http.MethodGet, "/api/day", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	decodeJSON(t, rec, &raw)
	for _, k := range []string{"date", "vday", "sessions", "provisional", "device_body", "ledger", "sum_mm", "warnings"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("thiếu trường %q: %s", k, rec.Body.String())
		}
	}
	var d dayResp
	decodeJSON(t, rec, &d)
	if d.Date != testToday || d.VDay != testToday {
		t.Errorf("date %q vday %q", d.Date, d.VDay)
	}
	if len(d.Sessions) != 1 || d.Sessions[0].StartS != 34560 || d.Sessions[0].Note != "thử" {
		t.Errorf("sessions (bỏ phiên đã xoá): %+v", d.Sessions)
	}
	if d.Provisional == nil || len(d.Provisional) != 0 {
		t.Errorf("tự động tắt ⇒ provisional rỗng: %+v", d.Provisional)
	}
	if want := threeSlots + "\n18900:900,22500:900,34560:300,62100:900"; d.DeviceBody == nil || *d.DeviceBody != want {
		t.Errorf("device_body %v, muốn %q", d.DeviceBody, want)
	}
	if d.SumMM == nil || *d.SumMM != 2.4 {
		t.Errorf("sum_mm %v", d.SumMM)
	}
	if len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "Vrain trễ: thử") {
		t.Errorf("warnings %q", d.Warnings)
	}

	// Ngày khác: không có device_body; chưa có mưa ⇒ sum_mm null.
	rec = apiDo(t, s, c, http.MethodGet, "/api/day?date=2026-11-14", "")
	raw = nil
	decodeJSON(t, rec, &raw)
	if _, ok := raw["device_body"]; ok {
		t.Errorf("ngày khác không được có device_body: %s", rec.Body.String())
	}
	if string(raw["sum_mm"]) != "null" || string(raw["sessions"]) != "[]" || string(raw["warnings"]) != "[]" {
		t.Errorf("ngày trống: %s", rec.Body.String())
	}
	if rec := apiDo(t, s, c, http.MethodGet, "/api/day?date=12-11-2026", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("ngày sai dạng: %d", rec.Code)
	}

	// Phiên manual sát mốc daily ⇒ cảnh báo.
	if _, err := s.Store.AddManualSession(testToday, 18900+600, 300, "sát"); err != nil {
		t.Fatal(err)
	}
	if d := getDay(t, s, c, "?date="+testToday); len(d.Warnings) != 2 {
		t.Errorf("thiếu cảnh báo phiên sát nhau: %q", d.Warnings)
	}

	// /api/state có "day" cùng dạng.
	rec = apiDo(t, s, c, http.MethodGet, "/api/state", "")
	var st struct {
		Day *dayResp `json:"day"`
	}
	decodeJSON(t, rec, &st)
	if st.Day == nil || st.Day.Date != testToday || st.Day.DeviceBody == nil || len(st.Day.Sessions) != 2 {
		t.Errorf("/api/state day: %+v", st.Day)
	}
}

func TestDayProvisionalWhenAutoOn(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	d := getDay(t, s, c, "?date=2026-11-13")
	if len(d.Provisional) != 3 || d.Provisional[0].RunS != 900 || d.Provisional[0].Reason == "" {
		t.Errorf("ngày mai: provisional %+v", d.Provisional)
	}
	if d := getDay(t, s, c, "?date=2026-11-11"); len(d.Provisional) != 0 {
		t.Errorf("ngày đã qua không có bản tạm: %+v", d.Provisional)
	}
}

// ---- /api/weather ----

func TestWeatherHoursClamp(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	// Snapshot mỗi giờ :07 trong 200 giờ trước now (08:00): mưa 0.5 mm/giờ, về lại ở bản 20:07.
	start := now.Add(-200 * time.Hour).Truncate(time.Hour).Add(7 * time.Minute)
	sum := 0.0
	for ts := start; ts.Before(*now); ts = ts.Add(time.Hour) {
		if ts.In(vietnam).Hour() == 20 {
			sum = 0
		}
		sum += 0.5
		if _, err := s.Store.InsertRainSnapshot(RainSnapshot{DataTS: ts, FetchedAt: ts, SumDepthMM: sum}); err != nil {
			t.Fatal(err)
		}
	}
	temp := 30.0
	s.Store.InsertMetar([]MetarObs{
		{ObsTime: now.Add(-2 * time.Hour), TempC: &temp, Raw: "VVTS 120000Z ..."},
		{ObsTime: now.Add(-50 * time.Hour), TempC: &temp, Raw: "cũ"},
	})

	type weather struct {
		Rain []struct {
			DataTS string   `json:"data_ts"`
			SumMM  float64  `json:"sum_mm"`
			HourMM *float64 `json:"hour_mm"`
			VDay   string   `json:"vday"`
		} `json:"rain"`
		Metar []struct {
			ObsTime string   `json:"obs_time"`
			TempC   *float64 `json:"temp_c"`
			Raw     string   `json:"raw"`
		} `json:"metar"`
	}
	get := func(q string) weather {
		t.Helper()
		rec := apiDo(t, s, c, http.MethodGet, "/api/weather"+q, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("/api/weather%s: %d %s", q, rec.Code, rec.Body.String())
		}
		var w weather
		decodeJSON(t, rec, &w)
		return w
	}
	for _, tc := range []struct {
		q         string
		rain, obs int
	}{
		{"", 48, 1}, {"?hours=1000", 168, 2}, {"?hours=1", 1, 0}, {"?hours=0", 1, 0}, {"?hours=24", 24, 1},
	} {
		w := get(tc.q)
		if len(w.Rain) != tc.rain || len(w.Metar) != tc.obs {
			t.Errorf("%q: %d snapshot, %d METAR; muốn %d, %d", tc.q, len(w.Rain), len(w.Metar), tc.rain, tc.obs)
		}
	}
	w := get("?hours=48")
	for _, r := range w.Rain {
		if r.HourMM == nil || *r.HourMM != 0.5 {
			t.Errorf("%s: hour_mm %v, muốn 0.5 (kể cả điểm đầu và bản 20:07)", r.DataTS, r.HourMM)
		}
	}
	if r := w.Rain[len(w.Rain)-1]; r.DataTS != "2026-11-12T07:07:00+07:00" || r.VDay != "2026-11-12" {
		t.Errorf("điểm cuối: %+v", r)
	}
	if m := w.Metar[0]; m.TempC == nil || *m.TempC != 30 || m.Raw != "VVTS 120000Z ..." || m.ObsTime != "2026-11-12T06:00:00+07:00" {
		t.Errorf("metar: %+v", m)
	}
	if rec := apiDo(t, s, c, http.MethodGet, "/api/weather?hours=abc", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("hours=abc: %d", rec.Code)
	}

	// Không có snapshot nào trước cửa sổ, hoặc snapshot trước cách quá 90 phút ⇒ hour_mm = null.
	s2, _, _ := newUIServer(t, threeSlots)
	c2 := mustLogin(t, s2)
	s2.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "03:07"), FetchedAt: *now, SumDepthMM: 0.2})
	s2.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "06:07"), FetchedAt: *now, SumDepthMM: 1})
	s2.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "07:07"), FetchedAt: *now, SumDepthMM: 1.5})
	rec := apiDo(t, s2, c2, http.MethodGet, "/api/weather", "")
	var w2 weather
	decodeJSON(t, rec, &w2)
	if len(w2.Rain) != 3 || w2.Rain[0].HourMM != nil || w2.Rain[1].HourMM != nil ||
		w2.Rain[2].HourMM == nil || *w2.Rain[2].HourMM != 0.5 {
		t.Errorf("không có điểm trước / mất bản giữa chừng: %s", rec.Body.String())
	}
}

// ---- vòng sửa 1 ----

func TestCancelAfterStartRejected(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	id := autoSessions(t, s.Store, "2026-11-12")[0].ID
	for _, hm := range []string{"05:15", "05:30"} {
		*now = vt("2026-11-12", hm)
		rec := apiDo(t, s, c, http.MethodPost, fmt.Sprintf("/api/sessions/%d/cancel", id), "")
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "đã qua giờ tưới") {
			t.Errorf("%s: code %d body %s, muốn 400 \"đã qua giờ tưới\"", hm, rec.Code, rec.Body.String())
		}
	}
	if got := autoSessions(t, s.Store, "2026-11-12"); got[0].State != stateFinal {
		t.Errorf("phiên bị đổi: %+v", got[0])
	}
}

func TestStateChangingRoutesNeedJSON(t *testing.T) {
	s, _, now := newUIServer(t, "25200:900,61200")
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	*now = vt("2026-11-12", "05:12")
	id := autoSessions(t, s.Store, "2026-11-12")[0].ID
	routes := []struct{ method, path, body string }{
		{http.MethodPost, fmt.Sprintf("/api/sessions/%d/cancel", id), ""},
		{http.MethodPost, "/api/day/cancel-slot", "date=2026-11-12&start_s=62100"},
		{http.MethodPost, "/api/settings/daily-from-slots", ""},
		{http.MethodPut, "/api/settings", "auto_enabled=false"},
		{http.MethodPut, "/api/schedule", "daily=25200"},
		{http.MethodPost, "/api/sessions", "date=2026-11-12&start_s=40000&run_s=60"},
	}
	for _, r := range routes {
		rec := do(s, r.method, r.path, r.body, withCookie(c), withHeader("Content-Type", "application/x-www-form-urlencoded"))
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s %s dạng form: code %d, muốn 415", r.method, r.path, rec.Code)
		}
	}
	if got := autoSessions(t, s.Store, "2026-11-12"); got[0].State != stateFinal {
		t.Errorf("phiên auto bị huỷ: %+v", got[0])
	}
	if d, _ := s.Store.Daily(); d != "25200:900,61200" {
		t.Errorf("daily bị đổi: %q", d)
	}
	if p, _ := s.Store.Params(); !p.AutoEnabled {
		t.Error("tham số bị đổi")
	}
	if ss, _ := s.Store.Sessions("2026-11-12"); len(ss) != 1 {
		t.Errorf("thêm phiên dạng form: %+v", ss)
	}
}

func TestSettingsSlotBoundariesAccepted(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	p := DefaultParams()
	p.Slots = []Slot{{slotMinS, "am"}, {slotMinS + 900 + 180, "am"}, {slotMaxS, "pm"}} // 04:15, cách đúng block+3 phút, 18:45
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Errorf("biên hợp lệ: %d %s", rec.Code, rec.Body.String())
	}
}

// moveSlot đổi 05:15 → 05:20 lúc 05:30 (đang bật tự động).
func moveSlot(t *testing.T, s *Server, c *http.Cookie) {
	t.Helper()
	p := DefaultParams()
	p.AutoEnabled = true
	p.Slots = []Slot{{19200, "am"}, {22500, "am"}, {62100, "pm"}}
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSlotMovedAfterDecideTimeSkipped(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	before, _, _ := s.Store.Ledger("2026-11-12")
	*now = vt("2026-11-12", "05:30")
	moveSlot(t, s, c)
	sc := &Scheduler{Store: s.Store, Now: func() time.Time { return *now }}
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := autoSessions(t, s.Store, "2026-11-12")
	if len(got) != 2 || got[0].StartS != 18900 || got[0].RunS != 360 || got[0].State != stateFinal ||
		got[1].StartS != 19200 || got[1].RunS != 0 || got[1].State != stateSkipped || got[1].Reason != reasonSlotChanged {
		t.Fatalf("phiên auto: %+v", got)
	}
	if after, _, _ := s.Store.Ledger("2026-11-12"); after != before {
		t.Errorf("sổ bị đổi: %+v → %+v", before, after)
	}
	if body, _ := s.deviceBody(*now); body != threeSlots+"\n18900:360,22500:900,62100:900" {
		t.Errorf("body %q", body)
	}
}

func TestSlotMovedHeavyRainStillNoWater(t *testing.T) {
	// Mưa 12 mm (≥ rain_hi) cả ngày: đổi 05:15 → 05:20 sau khi chốt không được làm 17:15 tưới.
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	rain := &fakeRain{now: now, snaps: hourlySnaps(vt("2026-11-11", "20:07"), vt("2026-11-12", "19:07"),
		func(time.Time) float64 { return 12 })}
	sc := &Scheduler{Store: s.Store, Rain: rain, Metar: &fakeMetar{now: now, obs: sunnyObs("2026-11-12")},
		Now: func() time.Time { return *now }}
	runTicks(t, sc, now, vt("2026-11-12", "05:00"), vt("2026-11-12", "05:29"))
	*now = vt("2026-11-12", "05:30")
	moveSlot(t, s, c)
	runTicks(t, sc, now, vt("2026-11-12", "05:30"), vt("2026-11-12", "17:20"))
	for _, x := range autoSessions(t, s.Store, "2026-11-12") {
		if x.RunS != 0 {
			t.Errorf("lần %s tưới %d giây dù mưa 12 mm: %s", hms(x.StartS), x.RunS, x.Reason)
		}
	}
	if l, _, _ := s.Store.Ledger("2026-11-12"); l.UsedRainS != 2700 {
		t.Errorf("used_rain %v, muốn 2700 (đúng quota)", l.UsedRainS)
	}
}

func TestSettingsOverflowCountsTodayLine(t *testing.T) {
	// Hôm nay đã chốt 05:15; thêm 17 phiên bơm thử (3 + 17 = 20). Thêm lần thứ tư lúc 05:12
	// ⇒ dòng 2 = 05:15 đã chốt + 06:15 + 12:30 + 17:15 + 17 = 21 mốc ⇒ phải từ chối.
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	*now = vt("2026-11-12", "05:12")
	for i := 0; i < 17; i++ {
		if _, err := s.Store.AddManualSession(testToday, 30000+i*600, 60, ""); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultParams()
	p.AutoEnabled = true
	p.Slots = []Slot{{18900, "am"}, {22500, "am"}, {45000, "pm"}, {62100, "pm"}}
	rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), testToday) {
		t.Errorf("code %d body %s, muốn 400 nêu %s", rec.Code, rec.Body.String(), testToday)
	}
	// Dời 05:15 đã chốt → 05:40: 05:40 nhường cho 05:15 (R10) nên vẫn 20 mốc: được.
	p.Slots = []Slot{{20400, "am"}, {22500, "am"}, {62100, "pm"}}
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Errorf("dời slot đã chốt: %d %s", rec.Code, rec.Body.String())
	}
	body, _ := s.deviceBody(*now)
	if _, line2, _ := strings.Cut(body, "\n"); len(strings.Split(line2, ",")) != 20 ||
		!strings.HasPrefix(line2, "18900:360,22500:900,") || strings.Contains(line2, "20400:") {
		t.Errorf("dòng 2 %q, muốn 20 mốc có 05:15 đã chốt, không có 05:40", line2)
	}
	// Phiên bơm thử thứ 18 hôm nay vượt 20.
	if _, err := s.Store.AddManualSession(testToday, 50000, 60, ""); err == nil {
		t.Error("phiên thứ 18 phải bị từ chối")
	}
}

// ---- R10: dời lần đã chốt giữa ngày ----

// slotMovedDay: mưa sum mm cả ngày 12/11; chốt 05:15 lúc 05:09; lúc 05:30 đổi các lần thành
// {06:15, 07:00, 17:15}; chạy scheduler tới 17:20. Trả server và các phiên auto của ngày.
func slotMovedDay(t *testing.T, sum float64) (*Server, []Session) {
	t.Helper()
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	rain := &fakeRain{now: now, snaps: hourlySnaps(vt("2026-11-11", "20:07"), vt("2026-11-12", "19:07"),
		func(time.Time) float64 { return sum })}
	sc := &Scheduler{Store: s.Store, Rain: rain, Metar: &fakeMetar{now: now, obs: sunnyObs("2026-11-12")},
		Now: func() time.Time { return *now }}
	runTicks(t, sc, now, vt("2026-11-12", "05:00"), vt("2026-11-12", "05:29"))
	*now = vt("2026-11-12", "05:30")
	p := DefaultParams()
	p.AutoEnabled = true
	p.Slots = []Slot{{22500, "am"}, {25200, "am"}, {62100, "pm"}}
	if rec := apiDo(t, s, c, http.MethodPut, "/api/settings", paramsJSON(t, p)); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	if sum == 0 {
		if body, _ := s.deviceBody(*now); body != threeSlots+"\n18900:900,25200:900,62100:900" {
			t.Errorf("body lúc 05:30 %q: 06:15 phải nhường cho 05:15 đã chốt", body)
		}
	}
	runTicks(t, sc, now, vt("2026-11-12", "05:30"), vt("2026-11-12", "17:20"))
	return s, autoSessions(t, s.Store, "2026-11-12")
}

func TestSlotMovedDecidedPairsHeavyRain(t *testing.T) {
	// Mưa 12 mm (≥ rain_hi): dời 05:15 đã chốt sang {06:15, 07:00, 17:15} không làm lần nào tưới.
	s, got := slotMovedDay(t, 12)
	if len(got) != 4 {
		t.Fatalf("phiên auto: %+v", got)
	}
	for _, x := range got {
		if x.RunS != 0 {
			t.Errorf("lần %s tưới %d giây dù mưa 12 mm: %s", hms(x.StartS), x.RunS, x.Reason)
		}
	}
	if got[1].StartS != 22500 || got[1].State != stateSkipped || got[1].Reason != "đã chốt ở giờ cũ 05:15" {
		t.Errorf("06:15 phải bỏ vì 05:15 đã chốt: %+v", got[1])
	}
	if l, _, _ := s.Store.Ledger("2026-11-12"); l.UsedRainS != 2700 {
		t.Errorf("used_rain %v, muốn 2700 (đúng quota)", l.UsedRainS)
	}
}

func TestSlotMovedDecidedPairsDryDay(t *testing.T) {
	// Ngày khô: vẫn đúng 3 lần × 15 phút = 2700 giây, không thành 4 lần.
	_, got := slotMovedDay(t, 0)
	total := 0
	for _, x := range got {
		if x.State == stateFinal {
			total += x.RunS
		}
	}
	if total != 2700 {
		t.Errorf("tổng tưới tự động %d giây, muốn 2700: %+v", total, got)
	}
	checkStarts := []int{18900, 22500, 25200, 62100}
	wantRuns := []int{900, 0, 900, 900}
	if len(got) != len(checkStarts) {
		t.Fatalf("phiên auto: %+v", got)
	}
	for i, x := range got {
		if x.StartS != checkStarts[i] || x.RunS != wantRuns[i] {
			t.Errorf("phiên %d = %d:%d, muốn %d:%d", i, x.StartS, x.RunS, checkStarts[i], wantRuns[i])
		}
	}
}

// ---- R11: huỷ trước giờ chốt ----

func cancelSlot(t *testing.T, s *Server, c *http.Cookie, date string, startS int) *httptest.ResponseRecorder {
	t.Helper()
	return apiDo(t, s, c, http.MethodPost, "/api/day/cancel-slot", fmt.Sprintf(`{"date":%q,"start_s":%d}`, date, startS))
}

func TestCancelSlotBeforeDecision(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	// Khô tới 15:07, từ 16:07 là 5.5 mm ⇒ lúc 17:09 lần 17:15 dùng 270 giây miễn, tưới 11 phút (làm tròn).
	rain := &fakeRain{now: now, snaps: hourlySnaps(vt("2026-11-11", "20:07"), vt("2026-11-12", "19:07"),
		func(tm time.Time) float64 {
			if tm.Day() == 12 && tm.Hour() >= 16 {
				return 5.5
			}
			return 0
		})}
	sc := &Scheduler{Store: s.Store, Rain: rain, Metar: &fakeMetar{now: now, obs: sunnyObs("2026-11-12")},
		Now: func() time.Time { return *now }}
	runTicks(t, sc, now, vt("2026-11-12", "04:00"), vt("2026-11-12", "14:00"))

	if rec := cancelSlot(t, s, c, testToday, 62100); rec.Code != http.StatusOK {
		t.Fatalf("huỷ 17:15 lúc 14:00: %d %s", rec.Code, rec.Body.String())
	}
	want := threeSlots + "\n18900:900,22500:900"
	if body, err := s.deviceBody(*now); err != nil || body != want {
		t.Errorf("body ngay sau huỷ %q (%v), muốn %q", body, err, want)
	}
	d := getDay(t, s, c, "")
	if len(d.Provisional) != 0 {
		t.Errorf("bản tạm còn: %+v", d.Provisional)
	}
	var intent *Session
	for i, x := range d.Sessions {
		if x.Source == sessionAuto && x.StartS == 62100 {
			intent = &d.Sessions[i]
		}
	}
	if intent == nil || intent.State != stateCancelled || intent.RunS != 0 || intent.Reason != "user huỷ trước giờ chốt" {
		t.Fatalf("phiên huỷ trước: %+v", intent)
	}
	if rec := cancelSlot(t, s, c, testToday, 62100); rec.Code != http.StatusBadRequest {
		t.Errorf("huỷ lần hai: %d, muốn 400", rec.Code)
	}

	runTicks(t, sc, now, vt("2026-11-12", "14:01"), vt("2026-11-12", "17:20"))
	var n int
	if err := s.Store.db.QueryRow(`SELECT count(*) FROM blocks WHERE vday = ? AND slot_s = 62100 AND run_s = 660 AND state = ?`,
		testToday, stateFinal).Scan(&n); err != nil || n != 1 {
		t.Errorf("blocks của 17:15: %d dòng (%v), muốn 1 dòng chốt 660 giây", n, err)
	}
	if l, _, _ := s.Store.Ledger(testToday); l.UsedRainS != 270 {
		t.Errorf("used_rain %v, muốn 270 (chốt như bình thường)", l.UsedRainS)
	}
	var pm []Session
	for _, x := range autoSessions(t, s.Store, testToday) {
		if x.StartS == 62100 {
			pm = append(pm, x)
		}
	}
	if len(pm) != 1 || pm[0].State != stateCancelled || pm[0].RunS != 660 || !strings.HasPrefix(pm[0].Reason, "đã huỷ — ") {
		t.Errorf("phiên 17:15 sau giờ chốt: %+v", pm)
	}
	if body, err := s.deviceBody(*now); err != nil || body != want {
		t.Errorf("body sau giờ chốt %q (%v), muốn %q", body, err, want)
	}
}

func TestCancelSlotFutureDate(t *testing.T) {
	s, _, now := newUIServer(t, threeSlots) // 08:00 ngày 12
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	if rec := cancelSlot(t, s, c, "2026-11-13", 22500); rec.Code != http.StatusOK {
		t.Fatalf("huỷ 06:15 ngày mai: %d %s", rec.Code, rec.Body.String())
	}
	d := getDay(t, s, c, "?date=2026-11-13")
	var starts []int
	for _, x := range d.Provisional {
		starts = append(starts, x.Slot.StartS)
	}
	if fmt.Sprint(starts) != "[18900 62100]" {
		t.Errorf("bản tạm ngày mai %v, muốn [18900 62100]", starts)
	}
	rain := &fakeRain{now: now, snaps: hourlySnaps(vt("2026-11-12", "20:07"), vt("2026-11-13", "19:07"),
		func(time.Time) float64 { return 0 })}
	sc := &Scheduler{Store: s.Store, Rain: rain, Now: func() time.Time { return *now }}
	runTicks(t, sc, now, vt("2026-11-13", "04:00"), vt("2026-11-13", "06:20"))
	if body, err := s.deviceBody(*now); err != nil || body != threeSlots+"\n18900:900,62100:900" {
		t.Errorf("body ngày 13 lúc 06:20 %q (%v)", body, err)
	}
	got := autoSessions(t, s.Store, "2026-11-13")
	if len(got) != 2 || got[1].StartS != 22500 || got[1].State != stateCancelled || got[1].RunS != 900 {
		t.Errorf("phiên ngày 13: %+v", got)
	}
}

func TestCancelSlotDecidedFinal(t *testing.T) {
	// UI hiện bản tạm lúc 05:08, user bấm lúc 05:12 khi 05:15 vừa chốt: huỷ luôn phiên đã chốt.
	s, _, now := newUIServer(t, threeSlots)
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	decide05(t, s)
	*now = vt("2026-11-12", "05:12")
	if rec := cancelSlot(t, s, c, testToday, 18900); rec.Code != http.StatusOK {
		t.Fatalf("huỷ 05:15 đã chốt: %d %s", rec.Code, rec.Body.String())
	}
	got := autoSessions(t, s.Store, testToday)
	if len(got) != 1 || got[0].State != stateCancelled || got[0].RunS != 360 {
		t.Errorf("phiên: %+v", got)
	}
}

func TestCancelSlotRejects(t *testing.T) {
	s, _, _ := newUIServer(t, threeSlots) // 08:00 ngày 12
	c := mustLogin(t, s)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	cases := []struct{ name, body, want string }{
		{"đã qua giờ tưới", `{"date":"2026-11-12","start_s":18900}`, "đã qua giờ tưới"},
		{"ngày đã qua", `{"date":"2026-11-11","start_s":62100}`, "đã qua giờ tưới"},
		{"không phải lần tự động", `{"date":"2026-11-12","start_s":40000}`, "Không có lần tự động"},
		{"ngày hỏng", `{"date":"12/11/2026","start_s":62100}`, "YYYY-MM-DD"},
		{"trường lạ", `{"date":"2026-11-12","start_s":62100,"x":1}`, "JSON sai"},
	}
	for _, tc := range cases {
		rec := apiDo(t, s, c, http.MethodPost, "/api/day/cancel-slot", tc.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, muốn 400 %q", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
	}
	p := DefaultParams() // tắt tự động
	if err := s.Store.SetParams(p); err != nil {
		t.Fatal(err)
	}
	if rec := cancelSlot(t, s, c, testToday, 62100); rec.Code != http.StatusBadRequest {
		t.Errorf("tắt tự động: %d %s, muốn 400", rec.Code, rec.Body.String())
	}
	if ss, _ := s.Store.Sessions(testToday); len(ss) != 0 {
		t.Errorf("bị từ chối mà vẫn ghi phiên: %+v", ss)
	}
	if ss, _ := s.Store.Sessions("2026-11-11"); len(ss) != 0 {
		t.Errorf("bị từ chối mà vẫn ghi phiên: %+v", ss)
	}
}

func TestSlotSkipsPairing(t *testing.T) {
	on := func(at time.Time, starts ...int) Params {
		p := DefaultParams()
		p.AutoEnabled, p.AutoEnabledAt, p.Slots = true, at, nil
		for _, st := range starts {
			ph := "am"
			if st >= 12*3600 {
				ph = "pm"
			}
			p.Slots = append(p.Slots, Slot{st, ph})
		}
		return p
	}
	off := func(p Params) Params { p.AutoEnabled = false; return p }
	long := vt("2026-11-01", "00:00")
	real := func(ph string) decidedSlot { return decidedSlot{Phase: ph} }
	rule := decidedSlot{Phase: "am", Rule: true}
	cases := []struct {
		name      string
		cur, next Params
		now       string
		decided   map[int]decidedSlot
		want      string
	}{
		{"dời 05:15 đã chốt, lần mới chưa tới giờ chốt", on(long, 18900, 22500, 62100), on(long, 22500, 25200, 62100), "05:30",
			map[int]decidedSlot{18900: real("am")}, "[{22500 đã chốt ở giờ cũ 05:15}]"},
		{"lần mới đã qua giờ chốt đã đủ chỗ", on(long, 18900, 22500, 62100), on(long, 19200, 22500, 62100), "05:30",
			map[int]decidedSlot{18900: real("am")}, "[{19200 đổi lịch sau giờ chốt}]"},
		{"không còn lần sáng ⇒ lấy lần gần nhất buổi khác", on(long, 18900, 22500, 62100), on(long, 50400, 62100), "06:30",
			map[int]decidedSlot{18900: real("am"), 22500: real("am")}, "[{50400 đã chốt ở giờ cũ 05:15} {62100 đã chốt ở giờ cũ 06:15}]"},
		{"dời lần hai: lần đã nhường không tính là chốt thật", on(long, 22500, 25200, 62100), on(long, 23400, 25200, 62100), "05:40",
			map[int]decidedSlot{18900: real("am"), 22500: rule}, "[{23400 đã chốt ở giờ cũ 05:15}]"},
		{"bật lại: lần bỏ vì bật sau giờ chốt đã đủ chỗ", off(on(long, 18900, 22500, 62100)), on(vt(testToday, "05:50"), 19800, 25200, 62100), "05:50",
			map[int]decidedSlot{18900: real("am")}, "[]"},
		{"tắt tự động ⇒ không ghi gì", on(long, 18900, 22500, 62100), DefaultParams(), "05:30",
			map[int]decidedSlot{18900: real("am")}, "[]"},
	}
	for _, tc := range cases {
		got, err := slotSkips(tc.cur, tc.next, testToday, vt(testToday, tc.now), tc.decided)
		var parts []string
		for _, sk := range got {
			parts = append(parts, fmt.Sprintf("{%d %s}", sk.Slot.StartS, sk.Reason))
		}
		if s := "[" + strings.Join(parts, " ") + "]"; err != nil || s != tc.want {
			t.Errorf("%s: %s (%v), muốn %s", tc.name, s, err, tc.want)
		}
	}
}
