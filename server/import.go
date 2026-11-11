package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Nhập dữ liệu của server cũ (file JSON/JSONL) vào SQLite, đúng một lần (spec §6.3).
// Không sửa, không xoá file cũ để còn đường quay lại.

type legacySchedule struct {
	Daily string            `json:"daily"`
	Days  map[string]string `json:"days"`
}

type legacyHeartbeat struct {
	ReceivedAt string          `json:"received_at"`
	Heartbeat  json.RawMessage `json:"heartbeat"`
}

type legacyEvent struct {
	Type       string `json:"type"`
	At         string `json:"at"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	ID         string `json:"id"`
	ReceivedAt string `json:"received_at"`
}

// badLine: dòng JSONL hỏng, chỉ cảnh báo rồi bỏ qua (khác lỗi DB, phải dừng).
type badLine struct{ msg string }

func (e *badLine) Error() string { return e.msg }

// ImportLegacy nhập schedule.json, heartbeats.jsonl, events.jsonl trong dataDir vào st
// trong một transaction. Chỉ chạy khi DB còn trống (chưa có lịch dự phòng và heartbeat);
// trả true nếu đã nhập. today (YYYY-MM-DD giờ VN) phân biệt ngày đã qua với ngày sắp tới.
func ImportLegacy(st *Store, dataDir string, today string) (bool, error) {
	if d, err := st.Daily(); err != nil {
		return false, err
	} else if d != "" {
		return false, nil
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM heartbeats`).Scan(&n); err != nil {
		return false, fmt.Errorf("đếm heartbeat: %w", err)
	}
	if n > 0 {
		return false, nil
	}

	var sched legacySchedule
	if b, err := os.ReadFile(filepath.Join(dataDir, "schedule.json")); err == nil {
		if err := json.Unmarshal(b, &sched); err != nil {
			log.Printf("nhập: schedule.json hỏng, bỏ qua: %v", err)
			sched = legacySchedule{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("đọc schedule.json: %w", err)
	}

	tx, err := st.db.Begin()
	if err != nil {
		return false, fmt.Errorf("nhập dữ liệu cũ: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().In(vietnam)
	nDaily, nSess, nHB, nEv := 0, 0, 0, 0

	daily, err := normalizeLine(sched.Daily)
	if err != nil {
		log.Printf("nhập: daily cũ không hợp lệ, bỏ qua: %v", err)
		daily = ""
	}
	if daily != "" {
		if _, err := tx.Exec(`INSERT INTO schedule_daily(id, line, updated_at) VALUES(1, ?, ?)`,
			daily, now.Format(time.RFC3339)); err != nil {
			return false, fmt.Errorf("nhập lịch dự phòng: %w", err)
		}
		nDaily = 1
	}
	inDaily := map[entry]bool{}
	for _, e := range parseLineEntries(daily) {
		inDaily[e] = true
	}
	dates := make([]string, 0, len(sched.Days))
	for d := range sched.Days {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	for _, date := range dates {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			log.Printf("nhập: bỏ ngày %q (sai định dạng)", date)
			continue
		}
		line, err := normalizeLine(sched.Days[date])
		if err != nil {
			log.Printf("nhập: bỏ ngày %s (dòng lịch hỏng): %v", date, err)
			continue
		}
		for _, e := range parseLineEntries(line) {
			// Ngày từ hôm nay: định dạng cũ "thay cả ngày" nên bỏ các mốc đã có trong daily.
			if date >= today && inDaily[e] {
				continue
			}
			if _, err := insertManualSessionRaw(tx, date, e.StartS, e.RunS, "nhập từ schedule.json", now); err != nil {
				return false, err
			}
			nSess++
		}
	}

	err = scanJSONL(filepath.Join(dataDir, "heartbeats.jsonl"), func(line []byte) error {
		var h legacyHeartbeat
		if err := json.Unmarshal(line, &h); err != nil || h.ReceivedAt == "" || len(h.Heartbeat) == 0 {
			return &badLine{"heartbeat hỏng"}
		}
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(h.Heartbeat, &id); err != nil {
			return &badLine{"heartbeat không phải JSON object"}
		}
		if _, err := tx.Exec(`INSERT INTO heartbeats(device_id, received_at, raw_json) VALUES(?, ?, ?)`,
			id.ID, h.ReceivedAt, string(h.Heartbeat)); err != nil {
			return fmt.Errorf("nhập heartbeat: %w", err)
		}
		nHB++
		return nil
	})
	if err != nil {
		return false, err
	}
	err = scanJSONL(filepath.Join(dataDir, "events.jsonl"), func(line []byte) error {
		var e legacyEvent
		if err := json.Unmarshal(line, &e); err != nil || e.Type == "" || e.ReceivedAt == "" {
			return &badLine{"event hỏng"}
		}
		if _, err := tx.Exec(`INSERT INTO events(device_id, at, type, source, reason, received_at) VALUES(?, ?, ?, ?, ?, ?)`,
			e.ID, e.At, e.Type, e.Source, e.Reason, e.ReceivedAt); err != nil {
			return fmt.Errorf("nhập event: %w", err)
		}
		nEv++
		return nil
	})
	if err != nil {
		return false, err
	}

	if nDaily+nSess+nHB+nEv == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("nhập dữ liệu cũ: %w", err)
	}
	log.Printf("Đã nhập dữ liệu cũ: lịch dự phòng %d, phiên %d, heartbeat %d, event %d", nDaily, nSess, nHB, nEv)
	return true, nil
}

// scanJSONL gọi fn cho từng dòng không rỗng. fn trả *badLine ⇒ cảnh báo và bỏ qua dòng;
// lỗi khác ⇒ dừng. File không tồn tại ⇒ không có gì để nhập.
func scanJSONL(path string, fn func(line []byte) error) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mở %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	for no := 1; ; no++ {
		line, rerr := r.ReadBytes('\n')
		if l := bytes.TrimSpace(line); len(l) > 0 {
			if ferr := fn(l); ferr != nil {
				var bl *badLine
				if !errors.As(ferr, &bl) {
					return ferr
				}
				log.Printf("nhập: %s dòng %d bị bỏ qua: %v", filepath.Base(path), no, ferr)
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("đọc %s: %w", filepath.Base(path), rerr)
		}
	}
}
