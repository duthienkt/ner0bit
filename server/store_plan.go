package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Ledger trả sổ phút miễn của ngày Vrain vday. Chưa có dòng ⇒ sổ rỗng (carry 0), found=false.
func (s *Store) Ledger(vday string) (Ledger, bool, error) {
	l := Ledger{VDay: vday}
	err := s.db.QueryRow(`SELECT carry_in, used_carry, used_rain, used_sun FROM days WHERE vday = ?`, vday).
		Scan(&l.CarryInS, &l.UsedCarryS, &l.UsedRainS, &l.UsedSunS)
	if errors.Is(err, sql.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return Ledger{}, false, fmt.Errorf("đọc sổ ngày %s: %w", vday, err)
	}
	return l, true, nil
}

// SaveDecision ghi một lần chốt trong một transaction: blocks + phiên auto + sổ (used_*) của
// l.VDay. state là stateFinal khi RunS > 0, ngược lại stateSkipped. Slot đã chốt ⇒ lỗi (PK
// của blocks), nhờ vậy sổ không bao giờ bị trừ hai lần.
func (s *Store) SaveDecision(date string, d Decision, l Ledger, state string, decidedAt time.Time) error {
	inputs, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("chốt lần tưới: %w", err)
	}
	at := decidedAt.In(vietnam).Format(time.RFC3339)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("chốt lần tưới: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO blocks(vday, slot_s, phase, run_s, state, decided_at, inputs_json, reason)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		l.VDay, d.Slot.StartS, d.Slot.Phase, d.RunS, state, at, string(inputs), d.Reason); err != nil {
		return fmt.Errorf("chốt lần tưới %s %s: %w", date, hms(d.Slot.StartS), err)
	}
	if _, err := tx.Exec(`INSERT INTO sessions(date, start_s, run_s, source, state, vday, slot_s, reason, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		date, d.Slot.StartS, d.RunS, sessionAuto, state, l.VDay, d.Slot.StartS, d.Reason, at, at); err != nil {
		return fmt.Errorf("ghi phiên auto: %w", err)
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chốt lần tưới: %w", err)
	}
	return nil
}

// Decided trả các StartS đã có phiên auto của ngày date, mọi trạng thái (chốt/bỏ/huỷ),
// để scheduler khởi động lại không chốt lần nữa.
func (s *Store) Decided(date string) (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT start_s FROM sessions WHERE date = ? AND source = ?`, date, sessionAuto)
	if err != nil {
		return nil, fmt.Errorf("đọc lần đã chốt: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var st int
		if err := rows.Scan(&st); err != nil {
			return nil, fmt.Errorf("đọc lần đã chốt: %w", err)
		}
		out[st] = true
	}
	return out, rows.Err()
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

// Provisional trả bản tạm (§4.3) cho các lần chưa chốt của ngày date tính tới now: sổ của
// ngày Vrain date, mưa mới nhất (không có ⇒ 0 mm), giả định S = 1. Lần có giờ chốt trước lúc
// bật tự động bị bỏ qua vì scheduler sẽ ghi "bỏ" cho lần đó.
func (s *Store) Provisional(date string, now time.Time, p Params) ([]Decision, error) {
	decided, err := s.Decided(date)
	if err != nil {
		return nil, err
	}
	var pending []Slot
	for _, sl := range sortedSlots(p.Slots) {
		if decided[sl.StartS] {
			continue
		}
		at, err := decideAt(date, sl)
		if err != nil {
			return nil, err
		}
		if at.Before(p.AutoEnabledAt) {
			continue
		}
		pending = append(pending, sl)
	}
	if len(pending) == 0 {
		return nil, nil
	}
	l, _, err := s.Ledger(date)
	if err != nil {
		return nil, err
	}
	sum := 0.0
	if r, ok, err := s.LatestRain(date, now); err != nil {
		return nil, err
	} else if ok {
		sum = r.SumDepthMM
	}
	return provisional(l, pending, sum, p), nil
}
