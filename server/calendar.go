package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Phiên manual dài/ngắn nhất (giây): mức cắt cứng của firmware là blockMaxS.
const (
	defaultRunS    = 300 // mốc thiếu duration ⇒ firmware dùng 300 s
	manualRunMinS  = 1
	manualRunMaxS  = blockMaxS
	sessionManual  = "manual"
	sessionAuto    = "auto"
	stateFinal     = "final"
	stateDeleted   = "deleted"
	stateSkipped   = "skipped"
	stateCancelled = "cancelled"
)

// Session là một phiên tưới của một ngày (lịch kiểu event calendar, spec §4.5).
type Session struct {
	ID        int64     `json:"id"`
	Date      string    `json:"date"` // YYYY-MM-DD giờ VN
	StartS    int       `json:"start_s"`
	RunS      int       `json:"run_s"`
	Source    string    `json:"source"` // "auto" | "manual"
	State     string    `json:"state"`  // "final" | "skipped" | "cancelled" | "deleted"
	VDay      string    `json:"vday,omitempty"`
	SlotS     *int      `json:"slot_s,omitempty"`
	Note      string    `json:"note"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// errSession là lỗi do dữ liệu người dùng gửi (⇒ 400), phân biệt với lỗi DB.
type errSession struct{ msg string }

func (e *errSession) Error() string { return e.msg }

// entry là một mốc trên dòng lịch gửi ESP.
type entry struct{ StartS, RunS int }

// renderLine sinh dòng lịch "s:r,s:r" theo thứ tự StartS tăng dần (không sửa es).
func renderLine(es []entry) string {
	sorted := append([]entry(nil), es...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartS < sorted[j].StartS })
	parts := make([]string, len(sorted))
	for i, e := range sorted {
		parts[i] = strconv.Itoa(e.StartS) + ":" + strconv.Itoa(e.RunS)
	}
	return strings.Join(parts, ",")
}

// parseLineEntries đọc dòng lịch đã chuẩn hoá; mốc thiếu duration ⇒ 300 s.
// Token hỏng bị bỏ qua (dòng trong DB đã qua normalizeLine).
func parseLineEntries(line string) []entry {
	var es []entry
	for _, tok := range strings.Split(strings.TrimSpace(line), ",") {
		start, dur, hasDur := strings.Cut(strings.TrimSpace(tok), ":")
		st, err := parseUint(start)
		if err != nil {
			continue
		}
		run := uint64(defaultRunS)
		if hasDur {
			if run, err = parseUint(dur); err != nil {
				continue
			}
		}
		es = append(es, entry{int(st), int(run)})
	}
	return es
}

// lineBase là số mốc tối đa của dòng 2 ngày date, chưa kể phiên manual, ở trường hợp xấu nhất
// giữa tự động tắt (các mốc daily) và tự động bật (phiên auto đã chốt có run > 0 + các lần trong
// p.Slots chưa có phiên auto — lần user huỷ trước giờ chốt đã có phiên "đã huỷ" nên không tính).
// ss là các phiên của ngày; skipped là các giờ sắp ghi "bỏ" (không tính). Ngày chưa có phiên
// auto nào thì bằng max(mốc daily, số lần tự động).
func lineBase(daily string, p Params, ss []Session, skipped map[int]bool) int {
	decided := map[int]bool{}
	n := 0
	for _, x := range ss {
		if x.Source != sessionAuto {
			continue
		}
		decided[x.StartS] = true
		if x.State == stateFinal && x.RunS > 0 {
			n++
		}
	}
	for _, sl := range p.Slots {
		if !decided[sl.StartS] && !skipped[sl.StartS] {
			n++
		}
	}
	return max(len(parseLineEntries(daily)), n)
}

// manualCount đếm phiên manual còn hiệu lực trong ss.
func manualCount(ss []Session) int {
	n := 0
	for _, x := range ss {
		if x.Source == sessionManual && x.State != stateDeleted {
			n++
		}
	}
	return n
}

// AddManualSession lưu một phiên bơm thử. Kiểm tra giới hạn dữ liệu và số mốc của dòng 2:
// lineBase + các phiên manual còn hiệu lực + phiên mới ≤ maxEntriesPerLine, để dòng 2 không vượt
// giới hạn firmware dù tự động bật hay tắt (lúc thêm hay về sau); sai ⇒ *errSession.
func (s *Store) AddManualSession(date string, startS, runS int, note string) (Session, error) {
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Session{}, &errSession{"Ngày phải dạng YYYY-MM-DD."}
	}
	if startS < 0 || startS >= secondsPerDay {
		return Session{}, &errSession{fmt.Sprintf("Giờ bắt đầu phải trong khoảng 0–%d giây.", secondsPerDay-1)}
	}
	if runS < manualRunMinS || runS > manualRunMaxS {
		return Session{}, &errSession{fmt.Sprintf("Thời lượng phải trong khoảng %d–%d giây.", manualRunMinS, manualRunMaxS)}
	}
	daily, err := s.Daily()
	if err != nil {
		return Session{}, err
	}
	p, err := s.Params()
	if err != nil {
		return Session{}, err
	}
	ss, err := s.Sessions(date)
	if err != nil {
		return Session{}, err
	}
	if base, n := lineBase(daily, p, ss, nil), manualCount(ss); base+n+1 > maxEntriesPerLine {
		return Session{}, &errSession{fmt.Sprintf("Lịch ngày %s sẽ vượt %d mốc (%d mốc daily/tự động + %d phiên bơm thử).",
			date, maxEntriesPerLine, base, n)}
	}
	now := time.Now().In(vietnam)
	id, err := insertManualSessionRaw(s.db, date, startS, runS, note, now)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, Date: date, StartS: startS, RunS: runS, Source: sessionManual,
		State: stateFinal, Note: note, CreatedAt: now}, nil
}

// execer là phần chung của *sql.DB và *sql.Tx để dùng một câu INSERT cho cả hai.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// insertManualSessionRaw chèn phiên manual, không kiểm tra gì (dùng cho nhập dữ liệu cũ,
// kể cả ngày đã qua). Trả id phiên mới.
func insertManualSessionRaw(x execer, date string, startS, runS int, note string, now time.Time) (int64, error) {
	res, err := x.Exec(`INSERT INTO sessions(date, start_s, run_s, source, state, note, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		date, startS, runS, sessionManual, stateFinal, note, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("thêm phiên: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("thêm phiên: %w", err)
	}
	return id, nil
}

// DeleteManualSession đặt state=deleted cho phiên manual; không có phiên manual đó ⇒ *errSession.
func (s *Store) DeleteManualSession(id int64) error {
	res, err := s.db.Exec(`UPDATE sessions SET state = ?, updated_at = ? WHERE id = ? AND source = ? AND state != ?`,
		stateDeleted, time.Now().In(vietnam).Format(time.RFC3339), id, sessionManual, stateDeleted)
	if err != nil {
		return fmt.Errorf("xoá phiên: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("xoá phiên: %w", err)
	} else if n == 0 {
		return &errSession{"Không có phiên manual này (hoặc đã xoá)."}
	}
	return nil
}

// Sessions trả mọi phiên của ngày date (kể cả đã xoá), theo giờ bắt đầu rồi id.
func (s *Store) Sessions(date string) ([]Session, error) { return sessionsQ(s.db, date) }

func sessionsQ(q querier, date string) ([]Session, error) {
	rows, err := q.Query(`SELECT id, date, start_s, run_s, source, state, COALESCE(vday, ''), slot_s, note, reason, created_at
		FROM sessions WHERE date = ? ORDER BY start_s, id`, date)
	if err != nil {
		return nil, fmt.Errorf("đọc phiên: %w", err)
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var ss Session
		var slot sql.NullInt64
		var created string
		if err := rows.Scan(&ss.ID, &ss.Date, &ss.StartS, &ss.RunS, &ss.Source, &ss.State, &ss.VDay, &slot, &ss.Note, &ss.Reason, &created); err != nil {
			return nil, fmt.Errorf("đọc phiên: %w", err)
		}
		if slot.Valid {
			v := int(slot.Int64)
			ss.SlotS = &v
		}
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			ss.CreatedAt = t
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// manualEntries: các phiên manual còn hiệu lực của ngày date.
func (s *Store) manualEntries(date string) ([]entry, error) {
	ss, err := s.Sessions(date)
	if err != nil {
		return nil, err
	}
	var es []entry
	for _, x := range ss {
		if x.Source == sessionManual && x.State != stateDeleted {
			es = append(es, entry{x.StartS, x.RunS})
		}
	}
	return es, nil
}

// manualCountsFrom: số phiên manual còn hiệu lực theo từng ngày ≥ from.
func (s *Store) manualCountsFrom(from string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT date, COUNT(*) FROM sessions
		WHERE source = ? AND state != ? AND date >= ? GROUP BY date`, sessionManual, stateDeleted, from)
	if err != nil {
		return nil, fmt.Errorf("đếm phiên: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, fmt.Errorf("đếm phiên: %w", err)
		}
		out[d] = n
	}
	return out, rows.Err()
}

// dayEntries = daily ∪ phiên manual còn hiệu lực của ngày date (dòng 2 khi tự động tắt).
func (s *Store) dayEntries(date string) ([]entry, error) {
	daily, err := s.Daily()
	if err != nil {
		return nil, err
	}
	man, err := s.manualEntries(date)
	if err != nil {
		return nil, err
	}
	return append(parseLineEntries(daily), man...), nil
}

// deviceBody sinh body response cho ESP lúc now (spec §4.5).
//   - Tự động tắt: chỉ dòng 1 (daily); có phiên manual hôm nay thì thêm dòng 2 = daily ∪ manual.
//   - Tự động bật: dòng 2 = phiên auto đã chốt có run > 0 ∪ phiên manual ∪ bản tạm của các lần
//     chưa chốt có run > 0. Rỗng ⇒ dòng 2 rỗng (hôm nay không tưới).
func (srv *Server) deviceBody(now time.Time) (string, error) {
	daily, err := srv.Store.Daily()
	if err != nil {
		return "", err
	}
	if daily == "" {
		return "", errScheduleMissing
	}
	p, err := srv.Store.Params()
	if err != nil {
		return "", err
	}
	ss, prov, err := srv.Store.daySessions(now.In(vietnam).Format(time.DateOnly), now, p)
	if err != nil {
		return "", err
	}
	body, warns := bodyOf(daily, p.AutoEnabled, ss, prov)
	for _, w := range warns {
		srv.warnCapped(now, w)
	}
	return body, nil
}

// warnCapped log mỗi lần dòng lịch bị cắt và ghi cảnh báo, tối đa một lần mỗi giờ. Ghi cảnh
// báo lỗi chỉ log (không làm heartbeat lỗi); lần sau trong giờ thử ghi lại.
func (srv *Server) warnCapped(now time.Time, msg string) {
	log.Printf("CẢNH BÁO: %s", msg)
	hour := now.In(vietnam).Format("2006-01-02 15")
	srv.capMu.Lock()
	defer srv.capMu.Unlock()
	if srv.capWarnedHour == hour {
		return
	}
	if err := srv.Store.AddWarning(now, "lịch", msg); err != nil {
		log.Printf("lỗi ghi cảnh báo: %v", err)
		return
	}
	srv.capWarnedHour = hour
}

// daySessions đọc các phiên của ngày date; khi bật tự động kèm bản tạm của các lần chưa chốt
// (dayPlan), khi tắt thì không có bản tạm.
func (s *Store) daySessions(date string, now time.Time, p Params) ([]Session, []Decision, error) {
	if p.AutoEnabled {
		return s.dayPlan(date, now, p)
	}
	ss, err := s.Sessions(date)
	return ss, nil, err
}

// lineEntries là các mốc thiết bị sẽ tưới trong ngày (xem deviceBody); hasManual cho biết có
// phiên manual còn hiệu lực.
func lineEntries(daily string, auto bool, ss []Session, prov []Decision) (es []entry, hasManual bool) {
	if !auto {
		es = parseLineEntries(daily)
	}
	for _, x := range ss {
		switch {
		case x.Source == sessionManual && x.State != stateDeleted:
			es = append(es, entry{x.StartS, x.RunS})
			hasManual = true
		case auto && x.Source == sessionAuto && x.State == stateFinal && x.RunS > 0:
			es = append(es, entry{x.StartS, x.RunS})
		}
	}
	for _, d := range prov {
		if d.RunS > 0 {
			es = append(es, entry{d.Slot.StartS, d.RunS})
		}
	}
	return es, hasManual
}

// bodyOf ghép body gửi ESP từ lịch dự phòng và các phiên của hôm nay. Dòng nào quá
// maxEntriesPerLine mốc (chỉ còn qua đường cũ: nhập dữ liệu, sửa tay DB, đua đọc/ghi — các route
// đều chặn) thì chỉ giữ các mốc sớm nhất, kèm cảnh báo trong warns.
func bodyOf(daily string, auto bool, ss []Session, prov []Decision) (body string, warns []string) {
	line1 := daily
	if d := parseLineEntries(daily); len(d) > maxEntriesPerLine {
		line1 = renderLine(capEntries(d))
		warns = append(warns, fmt.Sprintf("Lịch dự phòng có %d mốc, vượt %d: chỉ gửi %d mốc sớm nhất.",
			len(d), maxEntriesPerLine, maxEntriesPerLine))
	}
	es, hasManual := lineEntries(daily, auto, ss, prov)
	if !auto && !hasManual {
		return line1, warns
	}
	if len(es) > maxEntriesPerLine {
		kept := capEntries(es)
		warns = append(warns, fmt.Sprintf("Lịch hôm nay có %d mốc, vượt %d: chỉ gửi %d mốc sớm nhất, bỏ các mốc sau %s.",
			len(es), maxEntriesPerLine, maxEntriesPerLine, hms(kept[len(kept)-1].StartS)))
		es = kept
	}
	return line1 + "\n" + renderLine(es), warns
}

// capEntries trả maxEntriesPerLine mốc sớm nhất của es (theo giờ; firmware chỉ nhận 20 mốc đầu).
func capEntries(es []entry) []entry {
	sorted := append([]entry(nil), es...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartS < sorted[j].StartS })
	if len(sorted) > maxEntriesPerLine {
		sorted = sorted[:maxEntriesPerLine]
	}
	return sorted
}

// ---- HTTP ----

type sessionRequest struct {
	Date   string `json:"date"`
	StartS int    `json:"start_s"`
	RunS   int    `json:"run_s"`
	Note   string `json:"note"`
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	// Chống CSRF (cùng SameSite=Strict): form HTML không gửi được application/json.
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "Cần Content-Type: application/json.")
		return false
	}
	return true
}

func (s *Server) handleAddSession(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "Yêu cầu quá lớn.")
		return
	}
	var req sessionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON sai: "+err.Error())
		return
	}
	today := s.now().In(vietnam).Format(time.DateOnly)
	if _, err := time.Parse(time.DateOnly, req.Date); err == nil && req.Date < today {
		writeJSONError(w, http.StatusBadRequest, "Không thêm phiên cho ngày đã qua.")
		return
	}
	// Cảnh báo (không chặn) khi sát phiên khác; tính trước khi thêm.
	var warnings []string
	if es, err := s.Store.dayEntries(req.Date); err == nil {
		if p, err := s.Store.Params(); err == nil {
			gap := p.BlockS + slotGapExtraS
			for _, e := range es {
				if d := req.StartS - e.StartS; d < gap && -d < gap {
					warnings = append(warnings, fmt.Sprintf("Phiên cách mốc %s dưới %d giây; firmware có thể bỏ lần sau (busy/rest).", hms(e.StartS), gap))
				}
			}
		}
	}
	if warnings == nil {
		warnings = []string{}
	}
	sess, err := s.Store.AddManualSession(req.Date, req.StartS, req.RunS, req.Note)
	var se *errSession
	switch {
	case errors.As(err, &se):
		writeJSONError(w, http.StatusBadRequest, se.msg)
		return
	case err != nil:
		log.Printf("UI: lỗi thêm phiên: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không ghi được phiên.")
		return
	}
	log.Printf("UI: %s thêm phiên manual %s %s (%d giây)", s.clientIP(r), sess.Date, hms(sess.StartS), sess.RunS)
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess, "warnings": warnings})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSONError(w, http.StatusBadRequest, "id phiên không hợp lệ.")
		return
	}
	err = s.Store.DeleteManualSession(id)
	var se *errSession
	switch {
	case errors.As(err, &se):
		writeJSONError(w, http.StatusNotFound, se.msg)
	case err != nil:
		log.Printf("UI: lỗi xoá phiên: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Không xoá được phiên.")
	default:
		log.Printf("UI: %s xoá phiên manual #%d", s.clientIP(r), id)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// hms đổi giây-trong-ngày sang "HH:MM:SS" cho thông báo.
func hms(s int) string { return fmt.Sprintf("%02d:%02d:%02d", s/3600, s%3600/60, s%60) }
