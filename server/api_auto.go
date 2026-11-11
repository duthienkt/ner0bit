package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// API cho phần lịch tưới tự động của UI (spec §6.4):
//
//	GET  /api/day?date=YYYY-MM-DD        lịch tưới của ngày (mặc định hôm nay)
//	POST /api/sessions/{id}/cancel       huỷ phiên auto đã chốt của hôm nay/ngày tới
//	POST /api/day/cancel-slot            JSON {date, start_s}: huỷ một lần tự động chưa tới giờ tưới,
//	                                     kể cả khi chưa chốt (hôm nay hoặc ngày tới)
//	GET  /api/weather?hours=48           snapshot mưa (kèm mưa theo giờ) và METAR, tối đa 168 giờ
//	GET  /api/settings                   tham số tự động + quota, per_mm, lịch dự phòng
//	PUT  /api/settings                   JSON Params; auto_enabled_at do server đặt
//	POST /api/settings/daily-from-slots  đặt lịch dự phòng = các lần tự động, mỗi lần đủ block

const (
	defaultWeatherHours = 48
	maxWeatherHours     = 168
	hourGapMax          = 90 * time.Minute // hai snapshot cách hơn ⇒ hiệu số không còn là mưa một giờ
	reasonSlotChanged   = "đổi lịch sau giờ chốt"
	reasonPairedPrefix  = "đã chốt ở giờ cũ " // + HH:MM của lần đã chốt mà giờ không còn trong danh sách
)

// errNoSession: không có phiên với id đó (⇒ 404).
var errNoSession = errors.New("không có phiên này")

// ---- Store ----

// CancelAutoSession đặt state=cancelled cho phiên auto đã chốt chưa tới giờ tưới lúc at. Sổ phút
// miễn và bảng blocks giữ nguyên (spec §4.5: huỷ vẫn tính như đã chốt). Không có phiên ⇒
// errNoSession; phiên manual, đã qua giờ tưới, không ở trạng thái chốt ⇒ *errSession.
func (s *Store) CancelAutoSession(id int64, at time.Time) error {
	at = at.In(vietnam)
	today := at.Format(time.DateOnly)
	nowS := at.Hour()*3600 + at.Minute()*60 + at.Second()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("huỷ phiên: %w", err)
	}
	defer tx.Rollback()
	var date, source, state string
	var startS int
	err = tx.QueryRow(`SELECT date, start_s, source, state FROM sessions WHERE id = ?`, id).Scan(&date, &startS, &source, &state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errNoSession
	case err != nil:
		return fmt.Errorf("huỷ phiên: %w", err)
	case source != sessionAuto:
		return &errSession{"Chỉ huỷ được lần tưới theo thời tiết; phiên bơm thử thì xoá."}
	case date < today || (date == today && startS <= nowS):
		return &errSession{"Phiên đã qua giờ tưới (thiết bị chạy nốt phiên đang chạy), không huỷ được."}
	case state != stateFinal:
		return &errSession{fmt.Sprintf("Phiên đang ở trạng thái %q; chỉ huỷ được phiên đã chốt.", state)}
	}
	if _, err := tx.Exec(`UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`,
		stateCancelled, at.In(vietnam).Format(time.RFC3339), id); err != nil {
		return fmt.Errorf("huỷ phiên: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("huỷ phiên: %w", err)
	}
	return nil
}

// CancelAutoSlot huỷ lần tự động startS của ngày date lúc at (spec §4.5, R11), khi tự động đang
// bật và lần đó chưa tới giờ tưới:
//   - Chưa chốt ⇒ thêm phiên auto trạng thái "đã huỷ", run 0, lý do reasonCancelIntent. Phiên này
//     không phải lần chốt: tới giờ chốt scheduler vẫn chốt và trừ sổ như thường (SaveDecision cập
//     nhật run/lý do, giữ trạng thái huỷ); bản tạm và dòng 2 bỏ lần này ngay.
//   - Đã chốt và đang "chốt" ⇒ huỷ phiên đó như CancelAutoSession (UI vừa hiện bản tạm thì lần đó
//     đã chốt).
//
// Sai (tự động tắt, không phải lần tự động, đã qua giờ tưới, đã huỷ/bỏ) ⇒ *errSession.
func (s *Store) CancelAutoSlot(date string, startS int, at time.Time) error {
	at = at.In(vietnam)
	today := at.Format(time.DateOnly)
	nowS := at.Hour()*3600 + at.Minute()*60 + at.Second()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("huỷ lần tưới: %w", err)
	}
	defer tx.Rollback()
	p, err := paramsQ(tx)
	if err != nil {
		return err
	}
	isSlot := false
	for _, sl := range p.Slots {
		isSlot = isSlot || sl.StartS == startS
	}
	switch {
	case !p.AutoEnabled:
		return &errSession{"Đang tưới thủ công: không có lần tưới theo thời tiết nào để huỷ."}
	case date < today || (date == today && startS <= nowS):
		return &errSession{"Lần này đã qua giờ tưới (thiết bị chạy nốt phiên đang chạy), không huỷ được."}
	case !isSlot:
		return &errSession{fmt.Sprintf("Không có lần tưới theo thời tiết lúc %s.", hms(startS))}
	}
	ss, err := sessionsQ(tx, date)
	if err != nil {
		return err
	}
	ts := at.Format(time.RFC3339)
	for _, x := range ss {
		if x.Source != sessionAuto || x.StartS != startS {
			continue
		}
		if x.State != stateFinal {
			return &errSession{fmt.Sprintf("Lần %s đang ở trạng thái %q; chỉ huỷ được lần chưa chốt hoặc đã chốt.", hms(startS), x.State)}
		}
		if _, err := tx.Exec(`UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`, stateCancelled, ts, x.ID); err != nil {
			return fmt.Errorf("huỷ lần tưới: %w", err)
		}
		return commitCancel(tx)
	}
	if _, err := tx.Exec(`INSERT INTO sessions(date, start_s, run_s, source, state, vday, slot_s, reason, created_at, updated_at)
		VALUES(?, ?, 0, ?, ?, ?, ?, ?, ?, ?)`,
		date, startS, sessionAuto, stateCancelled, date, startS, reasonCancelIntent, ts, ts); err != nil {
		return fmt.Errorf("huỷ lần tưới: %w", err)
	}
	return commitCancel(tx)
}

func commitCancel(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("huỷ lần tưới: %w", err)
	}
	return nil
}

// warningRow là một dòng bảng warnings.
type warningRow struct {
	At      time.Time
	Source  string
	Message string
}

// WarningsBetween trả các cảnh báo có from <= at < to, cũ trước.
func (s *Store) WarningsBetween(from, to time.Time) ([]warningRow, error) {
	rows, err := s.db.Query(`SELECT at, source, message FROM warnings WHERE at >= ? AND at < ? ORDER BY at, id`,
		from.In(vietnam).Format(time.RFC3339), to.In(vietnam).Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("đọc cảnh báo: %w", err)
	}
	defer rows.Close()
	var out []warningRow
	for rows.Next() {
		var w warningRow
		var at string
		if err := rows.Scan(&at, &w.Source, &w.Message); err != nil {
			return nil, fmt.Errorf("đọc cảnh báo: %w", err)
		}
		if w.At, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("thời điểm cảnh báo hỏng %q: %w", at, err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ---- /api/day ----

// dayResponse là lịch tưới của một ngày cho UI (cũng là trường "day" của /api/state).
type dayResponse struct {
	Date        string     `json:"date"`
	VDay        string     `json:"vday"` // ngày Vrain chứa các lần tưới của ngày (slot 04:15–18:45 ⇒ trùng date)
	Sessions    []Session  `json:"sessions"`
	Provisional []Decision `json:"provisional"`           // bản tạm của các lần chưa chốt (chỉ khi bật tự động, ngày ≥ hôm nay)
	DeviceBody  *string    `json:"device_body,omitempty"` // chỉ có khi date là hôm nay
	Ledger      Ledger     `json:"ledger"`
	SumMM       *float64   `json:"sum_mm"` // mưa cộng dồn mới nhất của ngày Vrain; null ⇒ chưa có số liệu
	Warnings    []string   `json:"warnings"`
}

// dayView dựng lịch tưới ngày date lúc now. Phiên đã xoá không trả về.
func (s *Server) dayView(date string, now time.Time) (*dayResponse, error) {
	now = now.In(vietnam)
	today := now.Format(time.DateOnly)
	p, err := s.Store.Params()
	if err != nil {
		return nil, err
	}
	daily, err := s.Store.Daily()
	if err != nil {
		return nil, err
	}
	var ss []Session
	var prov []Decision
	if date >= today {
		ss, prov, err = s.Store.daySessions(date, now, p)
	} else {
		ss, err = s.Store.Sessions(date) // ngày đã qua: chỉ lịch sử, không có bản tạm
	}
	if err != nil {
		return nil, err
	}
	resp := &dayResponse{Date: date, VDay: date, Sessions: []Session{}, Provisional: []Decision{}, Warnings: []string{}}
	for _, x := range ss {
		if x.State != stateDeleted {
			resp.Sessions = append(resp.Sessions, x)
		}
	}
	resp.Provisional = append(resp.Provisional, prov...)
	if resp.Ledger, _, err = s.Store.Ledger(date); err != nil {
		return nil, err
	}
	if r, ok, err := s.Store.LatestRain(date, now); err != nil {
		return nil, err
	} else if ok {
		resp.SumMM = &r.SumDepthMM
	}

	d0, err := time.ParseInLocation(time.DateOnly, date, vietnam)
	if err != nil {
		return nil, err
	}
	ws, err := s.Store.WarningsBetween(d0, d0.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("%s %s: %s", w.At.In(vietnam).Format("15:04"), w.Source, w.Message))
	}
	if date >= today {
		es, _ := lineEntries(daily, p.AutoEnabled, ss, prov)
		resp.Warnings = append(resp.Warnings, closeWarnings(es, p.BlockS+slotGapExtraS)...)
	}
	if date == today {
		if daily == "" {
			resp.Warnings = append(resp.Warnings, "Chưa có lịch thủ công: thiết bị đang nhận lỗi 503 và giữ lịch cũ.")
		} else {
			body, warns := bodyOf(daily, p.AutoEnabled, ss, prov)
			resp.DeviceBody = &body
			resp.Warnings = append(resp.Warnings, warns...)
		}
	}
	return resp, nil
}

// closeWarnings cảnh báo các cặp mốc liền nhau cách dưới gap giây (firmware có thể bỏ lần sau).
func closeWarnings(es []entry, gap int) []string {
	sorted := append([]entry(nil), es...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartS < sorted[j].StartS })
	var out []string
	for i := 1; i < len(sorted); i++ {
		if d := sorted[i].StartS - sorted[i-1].StartS; d < gap {
			out = append(out, fmt.Sprintf("Mốc %s cách mốc %s dưới %d phút; firmware có thể bỏ lần sau (busy/rest).",
				hms(sorted[i].StartS), hms(sorted[i-1].StartS), gap/60))
		}
	}
	return out
}

func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	now := s.now().In(vietnam)
	date := r.URL.Query().Get("date")
	if date == "" {
		date = now.Format(time.DateOnly)
	} else if _, err := time.Parse(time.DateOnly, date); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Ngày phải dạng YYYY-MM-DD.")
		return
	}
	resp, err := s.dayView(date, now)
	if err != nil {
		log.Printf("UI: lỗi đọc lịch ngày %s: %v", date, err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được lịch ngày.")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- Huỷ phiên auto ----

func (s *Server) handleCancelSession(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) { // chống CSRF như các route ghi khác; body bỏ qua
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSONError(w, http.StatusBadRequest, "id phiên không hợp lệ.")
		return
	}
	err = s.Store.CancelAutoSession(id, s.now())
	var se *errSession
	switch {
	case errors.Is(err, errNoSession):
		writeJSONError(w, http.StatusNotFound, "Không có phiên này.")
	case errors.As(err, &se):
		writeJSONError(w, http.StatusBadRequest, se.msg)
	case err != nil:
		log.Printf("UI: lỗi huỷ phiên: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không huỷ được phiên.")
	default:
		log.Printf("UI: %s huỷ phiên auto #%d", s.clientIP(r), id)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleCancelSlot: POST /api/day/cancel-slot {"date": "YYYY-MM-DD", "start_s": 62100}.
func (s *Server) handleCancelSlot(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "Yêu cầu quá lớn.")
		return
	}
	var req struct {
		Date   string `json:"date"`
		StartS int    `json:"start_s"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON sai: "+err.Error())
		return
	}
	if _, err := time.Parse(time.DateOnly, req.Date); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Ngày phải dạng YYYY-MM-DD.")
		return
	}
	err = s.Store.CancelAutoSlot(req.Date, req.StartS, s.now())
	var se *errSession
	switch {
	case errors.As(err, &se):
		writeJSONError(w, http.StatusBadRequest, se.msg)
	case err != nil:
		log.Printf("UI: lỗi huỷ lần tưới: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không huỷ được lần tưới.")
	default:
		log.Printf("UI: %s huỷ lần tự động %s ngày %s", s.clientIP(r), hms(req.StartS), req.Date)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// ---- /api/weather ----

type rainPoint struct {
	DataTS string   `json:"data_ts"`
	SumMM  float64  `json:"sum_mm"`
	HourMM *float64 `json:"hour_mm"` // mưa kể từ snapshot liền trước; null ⇒ không có snapshot trước trong 90 phút
	VDay   string   `json:"vday"`
}

type metarPoint struct {
	ObsTime string   `json:"obs_time"`
	TempC   *float64 `json:"temp_c"`
	Raw     string   `json:"raw"`
}

func (s *Server) handleWeather(w http.ResponseWriter, r *http.Request) {
	hours := defaultWeatherHours
	if q := r.URL.Query().Get("hours"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "hours phải là số nguyên.")
			return
		}
		hours = min(max(n, 1), maxWeatherHours)
	}
	now := s.now().In(vietnam)
	from := now.Add(-time.Duration(hours) * time.Hour)
	snaps, err := s.Store.RainSince(from)
	if err != nil {
		log.Printf("UI: lỗi đọc mưa: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được số liệu mưa.")
		return
	}
	prev, hasPrev, err := s.Store.rainBefore(from)
	if err != nil {
		log.Printf("UI: lỗi đọc mưa: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được số liệu mưa.")
		return
	}
	obs, err := s.Store.MetarBetween(from, now.Add(time.Second))
	if err != nil {
		log.Printf("UI: lỗi đọc METAR: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được METAR.")
		return
	}
	resp := struct {
		Rain  []rainPoint  `json:"rain"`
		Metar []metarPoint `json:"metar"`
	}{Rain: rainSeries(snaps, prev, hasPrev), Metar: []metarPoint{}}
	for _, o := range obs {
		resp.Metar = append(resp.Metar, metarPoint{ObsTime: o.ObsTime.In(vietnam).Format(time.RFC3339), TempC: o.TempC, Raw: o.Raw})
	}
	writeJSON(w, http.StatusOK, resp)
}

// rainSeries đổi snapshot sang điểm cho UI. hour_mm của mỗi điểm tính bằng hourlyRain so với
// snapshot liền trước (điểm đầu so với prev, snapshot ngay trước cửa sổ); không có snapshot
// trước, hoặc snapshot trước cách quá 90 phút (mất bản giữa chừng), thì hour_mm = null.
func rainSeries(snaps []RainSnapshot, prev RainSnapshot, hasPrev bool) []rainPoint {
	out := make([]rainPoint, 0, len(snaps))
	for i, cur := range snaps {
		p := rainPoint{DataTS: cur.DataTS.In(vietnam).Format(time.RFC3339), SumMM: cur.SumDepthMM, VDay: VDay(cur.DataTS)}
		before, ok := prev, hasPrev
		if i > 0 {
			before, ok = snaps[i-1], true
		}
		if ok && cur.DataTS.Sub(before.DataTS) <= hourGapMax {
			h := math.Round(hourlyRain([]RainSnapshot{before, cur})[0]*100) / 100
			p.HourMM = &h
		}
		out = append(out, p)
	}
	return out
}

// ---- /api/settings ----

type settingsResponse struct {
	Params            Params  `json:"params"`
	QuotaS            int     `json:"quota_s"`
	PerMMS            float64 `json:"per_mm_s"`
	Daily             string  `json:"daily"`
	DailyMatchesSlots bool    `json:"daily_matches_slots"` // lịch dự phòng trùng các lần tự động, mỗi lần đủ block
}

// slotsLine là dòng lịch gồm các lần tự động, mỗi lần đủ block.
func slotsLine(p Params) string {
	es := make([]entry, 0, len(p.Slots))
	for _, sl := range p.Slots {
		es = append(es, entry{sl.StartS, p.BlockS})
	}
	return renderLine(es)
}

func (s *Server) settingsView() (*settingsResponse, error) {
	p, err := s.Store.Params()
	if err != nil {
		return nil, err
	}
	daily, err := s.Store.Daily()
	if err != nil {
		return nil, err
	}
	return &settingsResponse{
		Params:            p,
		QuotaS:            p.QuotaS(),
		PerMMS:            p.PerMMS(),
		Daily:             daily,
		DailyMatchesSlots: daily != "" && renderLine(parseLineEntries(daily)) == slotsLine(p),
	}, nil
}

func (s *Server) writeSettings(w http.ResponseWriter) {
	resp, err := s.settingsView()
	if err != nil {
		log.Printf("UI: lỗi đọc tham số: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được tham số.")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) { s.writeSettings(w) }

// handlePutSettings lưu tham số. Trường thiếu giữ giá trị đang lưu. auto_enabled_at do server
// đặt: chuyển tắt → bật ⇒ giờ server lúc lưu; đang bật ⇒ giữ nguyên; tắt ⇒ giữ mốc bật lần
// trước (chỉ để tham khảo; lần bật sau sẽ đặt mốc mới).
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "Yêu cầu quá lớn.")
		return
	}
	cur, err := s.Store.Params()
	if err != nil {
		log.Printf("UI: lỗi đọc tham số: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được tham số.")
		return
	}
	next := cur
	next.Slots = append([]Slot(nil), cur.Slots...)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON sai: "+err.Error())
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "JSON sai: thừa dữ liệu sau tham số.")
		return
	}
	now := s.now().In(vietnam)
	next.AutoEnabledAt = cur.AutoEnabledAt
	if next.AutoEnabled && !cur.AutoEnabled {
		next.AutoEnabledAt = now.Truncate(time.Second)
	}
	if err := next.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Tham số không hợp lệ: "+err.Error()+".")
		return
	}
	today := now.Format(time.DateOnly)
	// Xem trước các lần hôm nay sẽ ghi "bỏ" để kiểm tra số mốc; SetParamsSkipping tính lại trong
	// transaction ghi.
	skips, err := s.Store.SlotSkips(cur, next, today, now)
	if err != nil {
		log.Printf("UI: lỗi đọc phiên: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được phiên.")
		return
	}
	// Không để dòng 2 của ngày nào từ hôm nay vượt giới hạn mốc của firmware.
	if msg, err := s.overflowWith(next, today, skips); err != nil {
		log.Printf("UI: lỗi đọc phiên: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được phiên.")
		return
	} else if msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	if skips, err = s.Store.SetParamsSkipping(next, today, now); err != nil {
		log.Printf("UI: lỗi ghi tham số: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không ghi được tham số.")
		return
	}
	for _, sk := range skips {
		log.Printf("UI: %s đổi các lần tự động ⇒ hôm nay bỏ lần %s (%s)", s.clientIP(r), hms(sk.Slot.StartS), sk.Reason)
	}
	switch {
	case next.AutoEnabled && !cur.AutoEnabled:
		log.Printf("UI: %s BẬT lịch tự động lúc %s", s.clientIP(r), now.Format("2006-01-02 15:04:05"))
	case !next.AutoEnabled && cur.AutoEnabled:
		log.Printf("UI: %s TẮT lịch tự động, thiết bị quay về lịch dự phòng", s.clientIP(r))
	}
	log.Printf("UI: %s lưu tham số tự động: %d lần, block %d giây, mưa %.1f–%.1f mm, min_run %d, carry_max %d, e_ref %.1f",
		s.clientIP(r), len(next.Slots), next.BlockS, next.RainLoMM, next.RainHiMM, next.MinRunS, next.CarryMaxS, next.ERefMM)
	s.writeSettings(w)
}

// slotSkip là một lần của hôm nay phải ghi "bỏ" theo luật khi lưu tham số.
type slotSkip struct {
	Slot   Slot
	Reason string
}

// slotSkips trả các lần trong next của ngày date phải ghi "bỏ" (run 0, sổ giữ nguyên) ngay khi lưu
// tham số lúc now, để hôm nay số lần tưới tự động (đã chốt thật + chưa chốt) không vượt
// len(next.Slots) (R10). Chỉ khi next bật tự động. decided là các lần đã chốt của date (decisionsQ).
//
//  1. Lần mới (giờ không có trong cur) mà giờ chốt đã qua, khi tự động đang bật và vẫn bật ⇒
//     reasonSlotChanged: không chốt bù làm tưới thêm/trừ sổ lần nữa (bật từ tắt thì không cần:
//     auto_enabled_at = now đã làm scheduler bỏ các lần đó).
//  2. Mỗi lần đã chốt bằng planner mà giờ không còn trong next vẫn là một lần của hôm nay, nên
//     "dùng" một chỗ của next. Các lần của next đã/sắp bị bỏ theo luật (bước 1, đổi lịch trước đó,
//     bật sau giờ chốt) là chỗ đã dùng sẵn, xét cho các lần chốt cũ theo thứ tự giờ. Lần chốt cũ còn
//     lại lấy lần chưa chốt của next gần nhất (theo giờ) cùng buổi, không có thì gần nhất bất kỳ
//     buổi nào; lần đó ghi "bỏ" với lý do "đã chốt ở giờ cũ HH:MM".
func slotSkips(cur, next Params, date string, now time.Time, decided map[int]decidedSlot) ([]slotSkip, error) {
	if !next.AutoEnabled {
		return nil, nil
	}
	old, inNext := map[int]bool{}, map[int]bool{}
	for _, sl := range cur.Slots {
		old[sl.StartS] = true
	}
	for _, sl := range next.Slots {
		inNext[sl.StartS] = true
	}
	var out []slotSkip
	used := 0       // chỗ của next đã/sắp bị bỏ theo luật
	var open []Slot // lần của next chưa chốt, sẽ được chốt bình thường
	for _, sl := range sortedSlots(next.Slots) {
		if d, ok := decided[sl.StartS]; ok {
			if d.Rule {
				used++
			}
			continue
		}
		at, err := decideAt(date, sl)
		if err != nil {
			return nil, err
		}
		switch {
		case cur.AutoEnabled && !old[sl.StartS] && !at.After(now):
			out = append(out, slotSkip{sl, reasonSlotChanged})
			used++
		case at.Before(next.AutoEnabledAt): // scheduler sẽ ghi "bỏ" (bật sau giờ chốt)
			used++
		default:
			open = append(open, sl)
		}
	}
	var orphans []int
	for st, d := range decided {
		if !d.Rule && !inNext[st] {
			orphans = append(orphans, st)
		}
	}
	sort.Ints(orphans)
	for i, st := range orphans {
		if i < used || len(open) == 0 {
			continue
		}
		k := nearestSlot(open, st, decided[st].Phase)
		out = append(out, slotSkip{open[k], reasonPairedPrefix + fmt.Sprintf("%02d:%02d", st/3600, st%3600/60)})
		open = append(open[:k], open[k+1:]...)
	}
	return out, nil
}

// nearestSlot trả chỉ số lần trong slots (khác rỗng) gần giờ st nhất, ưu tiên cùng buổi phase;
// bằng nhau thì lần sớm hơn.
func nearestSlot(slots []Slot, st int, phase string) int {
	dist := func(i int) int { return max(slots[i].StartS-st, st-slots[i].StartS) }
	best := 0
	for i := 1; i < len(slots); i++ {
		same, bestSame := slots[i].Phase == phase, slots[best].Phase == phase
		if (same && !bestSame) || (same == bestSame && dist(i) < dist(best)) {
			best = i
		}
	}
	return best
}

// overflowWith trả thông báo lỗi nếu với tham số p có ngày ≥ today mà dòng 2 vượt
// maxEntriesPerLine: lineBase (tính cả phiên auto đã chốt của hôm nay; skips là các lần hôm nay
// sắp ghi "bỏ", xem slotSkips) + số phiên bơm thử; "" nếu ổn. Hôm nay luôn được xét, kể cả khi không có phiên
// bơm thử (đổi giờ giữa ngày có thể làm dòng 2 dài thêm).
func (s *Server) overflowWith(p Params, today string, skips []slotSkip) (string, error) {
	daily, err := s.Store.Daily()
	if err != nil {
		return "", err
	}
	counts, err := s.Store.manualCountsFrom(today)
	if err != nil {
		return "", err
	}
	dates := []string{today}
	for d := range counts {
		if d != today {
			dates = append(dates, d)
		}
	}
	sort.Strings(dates)
	skipped := map[int]bool{}
	for _, sk := range skips {
		skipped[sk.Slot.StartS] = true
	}
	for _, d := range dates {
		ss, err := s.Store.Sessions(d)
		if err != nil {
			return "", err
		}
		var sk map[int]bool
		if d == today {
			sk = skipped
		}
		if base, n := lineBase(daily, p, ss, sk), manualCount(ss); base+n > maxEntriesPerLine {
			return fmt.Sprintf("Lịch ngày %s sẽ vượt %d mốc (%d mốc lịch nền + %d phiên bơm thử). Xoá bớt phiên bơm thử hoặc bớt lần tưới.",
				d, maxEntriesPerLine, base, n), nil
		}
	}
	return "", nil
}

// handleDailyFromSlots đặt lịch dự phòng = các lần tự động, mỗi lần đủ block. Số mốc daily mới
// = số lần tự động, nên không làm dòng 2 của ngày nào dài thêm.
func (s *Server) handleDailyFromSlots(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) { // chống CSRF như các route ghi khác; body bỏ qua
		return
	}
	p, err := s.Store.Params()
	if err != nil {
		log.Printf("UI: lỗi đọc tham số: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không đọc được tham số.")
		return
	}
	line := slotsLine(p)
	if err := s.Store.SetDaily(line); err != nil {
		log.Printf("UI: lỗi ghi lịch: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không ghi được lịch.")
		return
	}
	log.Printf("UI: %s đặt lịch dự phòng theo lịch tự động: daily=%q", s.clientIP(r), line)
	s.writeSettings(w)
}
