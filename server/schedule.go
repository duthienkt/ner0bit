package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Giới hạn khớp firmware (docs/system-design.md, "Quy tắc parse ở ESP").
const (
	maxEntriesPerLine = 20
	secondsPerDay     = 86400
)

// Schedule là nội dung data/schedule.json.
//
//	{"daily": "25200:900,61200", "days": {"2026-11-12": "", "2026-11-13": "28800"}}
//
// daily = dòng 1 (lặp mỗi ngày). days[YYYY-MM-DD giờ VN] = dòng 2 cho đúng ngày đó:
// "" ⇒ hôm đó không tưới; không có key ⇒ không gửi dòng 2 (ESP dùng dòng 1).
type Schedule struct {
	Daily string            `json:"daily"`
	Days  map[string]string `json:"days,omitempty"`
}

// loadSchedule đọc và kiểm tra chặt file lịch; mọi lỗi đều trả về (server không đoán).
// Các dòng được chuẩn hoá về dạng canonical (không khoảng trắng).
func loadSchedule(path string) (*Schedule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := parseSchedule(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// parseSchedule kiểm tra nội dung JSON của lịch (dùng chung cho file và PUT /api/schedule).
func parseSchedule(raw []byte) (*Schedule, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Schedule
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("JSON sai: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON sai: thừa dữ liệu sau lịch")
	}
	daily, err := normalizeLine(s.Daily)
	if err != nil {
		return nil, fmt.Errorf("daily: %w", err)
	}
	if daily == "" {
		return nil, fmt.Errorf("daily phải có ít nhất 1 mốc")
	}
	s.Daily = daily
	for date, line := range s.Days {
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return nil, fmt.Errorf("days: ngày %q phải dạng YYYY-MM-DD", date)
		}
		norm, err := normalizeLine(line)
		if err != nil {
			return nil, fmt.Errorf("days[%s]: %w", date, err)
		}
		s.Days[date] = norm
	}
	return &s, nil
}

// normalizeLine kiểm tra một dòng "<giây>[:<duration>],..." và trả dạng canonical.
// Chuỗi rỗng (sau trim) là hợp lệ và trả "".
func normalizeLine(line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil
	}
	tokens := strings.Split(line, ",")
	if len(tokens) > maxEntriesPerLine {
		return "", fmt.Errorf("tối đa %d mốc, có %d", maxEntriesPerLine, len(tokens))
	}
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		start, dur, hasDur := strings.Cut(strings.TrimSpace(tok), ":")
		sec, err := parseUint(start)
		if err != nil {
			return "", fmt.Errorf("mốc %q: %w", tok, err)
		}
		if sec >= secondsPerDay {
			return "", fmt.Errorf("mốc %q: giây phải < %d", tok, secondsPerDay)
		}
		entry := strconv.FormatUint(sec, 10)
		if hasDur {
			d, err := parseUint(dur)
			if err != nil {
				return "", fmt.Errorf("mốc %q: duration: %w", tok, err)
			}
			if d == 0 {
				return "", fmt.Errorf("mốc %q: duration phải > 0", tok)
			}
			entry += ":" + strconv.FormatUint(d, 10)
		}
		out = append(out, entry)
	}
	return strings.Join(out, ","), nil
}

// parseUint chỉ nhận chữ số thập phân (không dấu, không rỗng) — giống parser ở ESP.
func parseUint(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("rỗng")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q không phải số", s)
		}
	}
	return strconv.ParseUint(s, 10, 32)
}

// render sinh body response cho ngày date (YYYY-MM-DD giờ VN).
func (s *Schedule) render(date string) string {
	if line2, ok := s.Days[date]; ok {
		return s.Daily + "\n" + line2
	}
	return s.Daily
}
