package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// querier là phần đọc chung của *sql.DB và *sql.Tx (đọc nhiều bảng trong một ảnh chụp).
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Ledger trả sổ phút miễn của ngày Vrain vday. Chưa có dòng ⇒ sổ rỗng (carry 0), found=false.
func (s *Store) Ledger(vday string) (Ledger, bool, error) { return ledgerQ(s.db, vday) }

func ledgerQ(q querier, vday string) (Ledger, bool, error) {
	l := Ledger{VDay: vday}
	err := q.QueryRow(`SELECT carry_in, used_carry, used_rain, used_sun FROM days WHERE vday = ?`, vday).
		Scan(&l.CarryInS, &l.UsedCarryS, &l.UsedRainS, &l.UsedSunS)
	if errors.Is(err, sql.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return Ledger{}, false, fmt.Errorf("đọc sổ ngày %s: %w", vday, err)
	}
	return l, true, nil
}

// blockRuleSkip là state trong bảng blocks của lần "bỏ" theo luật (bật tự động sau giờ chốt, đổi
// lịch sau giờ chốt, đã chốt ở giờ cũ): không qua planner, không đụng sổ, không phải một lần tưới
// thật của ngày. Lần chốt bằng planner ghi stateFinal/stateSkipped. Phiên tương ứng vẫn là
// stateSkipped (hoặc giữ stateCancelled nếu user đã huỷ trước).
const blockRuleSkip = "rule_skipped"

// Lý do của phiên user huỷ trước giờ chốt; tới giờ chốt lý do thành tiền tố này + lý do chốt.
const (
	reasonCancelIntent = "user huỷ trước giờ chốt"
	reasonCancelPrefix = "đã huỷ — "
)

// SaveDecision ghi một lần chốt trong một transaction: blocks + phiên auto + sổ (used_*) của
// l.VDay. state là stateFinal khi RunS > 0, ngược lại stateSkipped. Slot đã chốt ⇒ lỗi (PK
// của blocks), nhờ vậy sổ không bao giờ bị trừ hai lần. User đã huỷ trước lần này ⇒ vẫn chốt và
// trừ sổ như thường, nhưng phiên giữ trạng thái "đã huỷ" (spec §4.5).
func (s *Store) SaveDecision(date string, d Decision, l Ledger, state string, decidedAt time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("chốt lần tưới: %w", err)
	}
	defer tx.Rollback()
	if err := saveDecisionTx(tx, date, d, l, state, decidedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chốt lần tưới: %w", err)
	}
	return nil
}

// insertBlockTx ghi dòng blocks của một lần đã chốt (planner hoặc luật); đã có ⇒ lỗi PK.
func insertBlockTx(tx *sql.Tx, date string, d Decision, state string, at string) error {
	inputs, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("chốt lần tưới: %w", err)
	}
	// Slot của ngày D (04:15–18:45) luôn thuộc ngày Vrain D.
	if _, err := tx.Exec(`INSERT INTO blocks(vday, slot_s, phase, run_s, state, decided_at, inputs_json, reason)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		date, d.Slot.StartS, d.Slot.Phase, d.RunS, state, at, string(inputs), d.Reason); err != nil {
		return fmt.Errorf("chốt lần tưới %s %s: %w", date, hms(d.Slot.StartS), err)
	}
	return nil
}

// writeAutoSessionTx ghi phiên auto của lần vừa chốt. Có phiên "user huỷ trước giờ chốt" của
// cùng giờ (chưa có dòng blocks nào khác vì insertBlockTx đi trước) ⇒ cập nhật run/lý do, giữ
// trạng thái huỷ; không thì thêm phiên mới với trạng thái state.
func writeAutoSessionTx(tx *sql.Tx, date string, d Decision, state string, at string) error {
	res, err := tx.Exec(`UPDATE sessions SET run_s = ?, vday = ?, slot_s = ?, reason = ?, updated_at = ?
		WHERE date = ? AND start_s = ? AND source = ? AND state = ?`,
		d.RunS, date, d.Slot.StartS, reasonCancelPrefix+d.Reason, at, date, d.Slot.StartS, sessionAuto, stateCancelled)
	if err != nil {
		return fmt.Errorf("ghi phiên auto: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("ghi phiên auto: %w", err)
	} else if n > 0 {
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO sessions(date, start_s, run_s, source, state, vday, slot_s, reason, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		date, d.Slot.StartS, d.RunS, sessionAuto, state, date, d.Slot.StartS, d.Reason, at, at); err != nil {
		return fmt.Errorf("ghi phiên auto: %w", err)
	}
	return nil
}

// saveDecisionTx là phần ghi của SaveDecision trong transaction tx có sẵn.
func saveDecisionTx(tx *sql.Tx, date string, d Decision, l Ledger, state string, decidedAt time.Time) error {
	at := decidedAt.In(vietnam).Format(time.RFC3339)
	if err := insertBlockTx(tx, date, d, state, at); err != nil {
		return err
	}
	if err := writeAutoSessionTx(tx, date, d, state, at); err != nil {
		return err
	}
	// carry_in thuộc về CloseDay của ngày trước; ở đây chỉ ghi phần đã dùng.
	if _, err := tx.Exec(`INSERT INTO days(vday, carry_in, used_carry, used_rain, used_sun) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(vday) DO UPDATE SET used_carry=excluded.used_carry, used_rain=excluded.used_rain,
			used_sun=excluded.used_sun`,
		l.VDay, l.CarryInS, l.UsedCarryS, l.UsedRainS, l.UsedSunS); err != nil {
		return fmt.Errorf("ghi sổ ngày %s: %w", l.VDay, err)
	}
	if d.Slot.Phase == "pm" && d.SunS > 0 {
		var et any
		if d.ETMM > 0 {
			et = d.ETMM
		}
		if _, err := tx.Exec(`UPDATE days SET sun_s = ?, et = ? WHERE vday = ?`, d.SunS, et, l.VDay); err != nil {
			return fmt.Errorf("ghi hệ số nắng ngày %s: %w", l.VDay, err)
		}
	}
	return nil
}

// SaveSkip ghi "bỏ" theo luật cho slot của ngày date (run 0, lý do reason, sổ không đổi).
func (s *Store) SaveSkip(date string, slot Slot, reason string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("bỏ lần tưới: %w", err)
	}
	defer tx.Rollback()
	if err := saveSkipTx(tx, date, slot, reason, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("bỏ lần tưới: %w", err)
	}
	return nil
}

func saveSkipTx(tx *sql.Tx, date string, slot Slot, reason string, at time.Time) error {
	ts := at.In(vietnam).Format(time.RFC3339)
	d := Decision{Slot: slot, Reason: reason}
	if err := insertBlockTx(tx, date, d, blockRuleSkip, ts); err != nil {
		return err
	}
	return writeAutoSessionTx(tx, date, d, stateSkipped, ts)
}

// SetParamsSkipping lưu tham số next và, trong cùng transaction, ghi "bỏ" (run 0, sổ giữ nguyên)
// cho các lần của ngày date mà slotSkips chỉ ra lúc now (đọc tham số đang lưu và các lần đã chốt
// trong chính transaction này). Nhờ vậy scheduler không bao giờ thấy tham số mới mà chưa thấy các
// lần "bỏ" đó. Trả các lần đã ghi "bỏ".
func (s *Store) SetParamsSkipping(next Params, date string, now time.Time) ([]slotSkip, error) {
	if err := next.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("ghi tham số: %w", err)
	}
	defer tx.Rollback()
	cur, err := paramsQ(tx)
	if err != nil {
		return nil, err
	}
	decided, err := decisionsQ(tx, date)
	if err != nil {
		return nil, err
	}
	skips, err := slotSkips(cur, next, date, now, decided)
	if err != nil {
		return nil, err
	}
	for _, sk := range skips {
		if err := saveSkipTx(tx, date, sk.Slot, sk.Reason, now); err != nil {
			return nil, err
		}
	}
	if err := setParamsQ(tx, next); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("ghi tham số: %w", err)
	}
	return skips, nil
}

// SlotSkips xem trước slotSkips của ngày date với các lần đã chốt hiện có (ngoài transaction ghi).
func (s *Store) SlotSkips(cur, next Params, date string, now time.Time) ([]slotSkip, error) {
	decided, err := decisionsQ(s.db, date)
	if err != nil {
		return nil, err
	}
	return slotSkips(cur, next, date, now, decided)
}

// decidedSlot là một dòng blocks của ngày: lần đã chốt bằng planner (Rule=false) hay "bỏ" theo luật.
type decidedSlot struct {
	Phase string
	Rule  bool
}

// decisionsQ trả các lần đã chốt (bảng blocks) của ngày date theo StartS.
func decisionsQ(q querier, date string) (map[int]decidedSlot, error) {
	rows, err := q.Query(`SELECT slot_s, phase, state FROM blocks WHERE vday = ?`, date)
	if err != nil {
		return nil, fmt.Errorf("đọc lần đã chốt: %w", err)
	}
	defer rows.Close()
	out := map[int]decidedSlot{}
	for rows.Next() {
		var st int
		var phase, state string
		if err := rows.Scan(&st, &phase, &state); err != nil {
			return nil, fmt.Errorf("đọc lần đã chốt: %w", err)
		}
		out[st] = decidedSlot{Phase: phase, Rule: state == blockRuleSkip}
	}
	return out, rows.Err()
}

// Decided trả các StartS đã chốt của ngày date (có dòng blocks: chốt/bỏ, kể cả phiên sau đó bị
// huỷ), để scheduler khởi động lại không chốt lần nữa. Phiên "user huỷ trước giờ chốt" chưa phải
// lần chốt nên không có ở đây: tới giờ vẫn chốt và trừ sổ như thường.
func (s *Store) Decided(date string) (map[int]bool, error) {
	ds, err := decisionsQ(s.db, date)
	if err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(ds))
	for st := range ds {
		out[st] = true
	}
	return out, nil
}

// CloseDay đóng ngày Vrain vday (ghi tổng mưa cuối) và chuyển carryNextS sang carry_in của
// ngày kế tiếp, trong một transaction.
func (s *Store) CloseDay(vday string, finalSum float64, carryNextS float64, at time.Time) error {
	d, err := time.ParseInLocation(time.DateOnly, vday, vietnam)
	if err != nil {
		return fmt.Errorf("ngày Vrain %q không hợp lệ: %w", vday, err)
	}
	next := d.AddDate(0, 0, 1).Format(time.DateOnly)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("đóng ngày %s: %w", vday, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO days(vday, final_sum, closed_at) VALUES(?, ?, ?)
		ON CONFLICT(vday) DO UPDATE SET final_sum=excluded.final_sum, closed_at=excluded.closed_at`,
		vday, finalSum, at.In(vietnam).Format(time.RFC3339)); err != nil {
		return fmt.Errorf("đóng ngày %s: %w", vday, err)
	}
	if _, err := tx.Exec(`INSERT INTO days(vday, carry_in) VALUES(?, ?)
		ON CONFLICT(vday) DO UPDATE SET carry_in=excluded.carry_in`, next, carryNextS); err != nil {
		return fmt.Errorf("ghi carry ngày %s: %w", next, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("đóng ngày %s: %w", vday, err)
	}
	return nil
}

// OpenDays trả các ngày Vrain chưa đóng (có dòng sổ hoặc có snapshot), tăng dần.
func (s *Store) OpenDays() ([]string, error) {
	rows, err := s.db.Query(`SELECT vday FROM days WHERE closed_at IS NULL
		UNION
		SELECT DISTINCT vday FROM rain_snapshots
			WHERE vday NOT IN (SELECT vday FROM days WHERE closed_at IS NOT NULL)
		ORDER BY vday`)
	if err != nil {
		return nil, fmt.Errorf("đọc ngày Vrain còn mở: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("đọc ngày Vrain còn mở: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// newestRainVDay trả ngày Vrain của snapshot mới nhất; chưa có snapshot ⇒ ok=false.
func (s *Store) newestRainVDay() (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT vday FROM rain_snapshots ORDER BY data_ts DESC LIMIT 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("đọc snapshot mưa mới nhất: %w", err)
	}
	return v, true, nil
}

// rainBefore trả snapshot ngay trước t (data_ts < t).
func (s *Store) rainBefore(t time.Time) (RainSnapshot, bool, error) {
	return s.rainLatest("<", t)
}

// rainUpTo trả snapshot mới nhất có data_ts <= t, không kể ngày Vrain.
func (s *Store) rainUpTo(t time.Time) (RainSnapshot, bool, error) {
	return s.rainLatest("<=", t)
}

// rainLatest: op là "<" hoặc "<=" (hằng nội bộ, không từ người dùng).
func (s *Store) rainLatest(op string, t time.Time) (RainSnapshot, bool, error) {
	r, err := scanRain(s.db.QueryRow(`SELECT data_ts, fetched_at, sum_depth, level FROM rain_snapshots
		WHERE data_ts `+op+` ? ORDER BY data_ts DESC LIMIT 1`, t.In(vietnam).Format(time.RFC3339)))
	if errors.Is(err, sql.ErrNoRows) {
		return RainSnapshot{}, false, nil
	}
	if err != nil {
		return RainSnapshot{}, false, fmt.Errorf("đọc snapshot mưa: %w", err)
	}
	return r, true, nil
}

// sortedSlots trả bản sao các slot theo giờ tăng dần.
func sortedSlots(slots []Slot) []Slot {
	out := append([]Slot(nil), slots...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartS < out[j].StartS })
	return out
}

// decideLeadS: chốt một lần tưới 6 phút trước giờ tưới (05:15 ⇒ 05:09).
const decideLeadS = 360

// decideAt là thời điểm chốt slot của ngày date (giờ VN).
func decideAt(date string, slot Slot) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, date, vietnam)
	if err != nil {
		return time.Time{}, fmt.Errorf("ngày %q không hợp lệ: %w", date, err)
	}
	return d.Add(time.Duration(slot.StartS-decideLeadS) * time.Second), nil
}

// Provisional trả bản tạm (§4.3) cho các lần chưa chốt của ngày date tính tới now (xem dayPlan).
func (s *Store) Provisional(date string, now time.Time, p Params) ([]Decision, error) {
	_, prov, err := s.dayPlan(date, now, p)
	return prov, err
}

// dayPlan đọc trong một transaction (một ảnh chụp, không lẫn với lần chốt đang ghi) mọi phiên của
// ngày date và tính bản tạm cho các lần chưa chốt: sổ của ngày Vrain date, mưa mới nhất (không có
// ⇒ 0 mm), giả định S = 1. Lần có giờ chốt trước lúc bật tự động bị bỏ qua vì scheduler sẽ ghi
// "bỏ" cho lần đó. Lần user huỷ trước giờ chốt vẫn được tính trong chuỗi sổ (tới giờ nó vẫn trừ
// sổ) nhưng không có trong bản tạm trả về, nên không bao giờ gửi thiết bị.
func (s *Store) dayPlan(date string, now time.Time, p Params) ([]Session, []Decision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("đọc lịch ngày %s: %w", date, err)
	}
	defer tx.Rollback() // chỉ đọc
	ss, err := sessionsQ(tx, date)
	if err != nil {
		return nil, nil, err
	}
	decided, err := decisionsQ(tx, date)
	if err != nil {
		return nil, nil, err
	}
	cancelled := map[int]bool{}
	for _, x := range ss {
		if x.Source == sessionAuto && x.State == stateCancelled {
			cancelled[x.StartS] = true
		}
	}
	var pending []Slot
	for _, sl := range sortedSlots(p.Slots) {
		if _, ok := decided[sl.StartS]; ok {
			continue
		}
		at, err := decideAt(date, sl)
		if err != nil {
			return nil, nil, err
		}
		if at.Before(p.AutoEnabledAt) {
			continue
		}
		pending = append(pending, sl)
	}
	if len(pending) == 0 {
		return ss, nil, nil
	}
	l, _, err := ledgerQ(tx, date)
	if err != nil {
		return nil, nil, err
	}
	sum := 0.0
	if r, ok, err := latestRainQ(tx, date, now); err != nil {
		return nil, nil, err
	} else if ok {
		sum = r.SumDepthMM
	}
	var prov []Decision
	for _, d := range provisional(l, pending, sum, p) {
		if !cancelled[d.Slot.StartS] {
			prov = append(prov, d)
		}
	}
	return ss, prov, nil
}
