package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Giới hạn khớp firmware (docs/system-design.md, "Quy tắc parse ở ESP").
const (
	maxEntriesPerLine = 20
	secondsPerDay     = 86400
)

// Schedule là lịch dự phòng (dòng 1) lặp mỗi ngày, ví dụ {"daily": "25200:900,61200"}.
// Lịch riêng từng ngày nằm ở bảng sessions (calendar.go).
type Schedule struct {
	Daily string `json:"daily"`
}

// parseSchedule kiểm tra body PUT /api/schedule. days (định dạng cũ) được nhận để báo lỗi
// rõ ràng: hasDays=true khi nó khác rỗng.
func parseSchedule(raw []byte) (s *Schedule, hasDays bool, err error) {
	var req struct {
		Daily string            `json:"daily"`
		Days  map[string]string `json:"days"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, false, fmt.Errorf("JSON sai: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false, fmt.Errorf("JSON sai: thừa dữ liệu sau lịch")
	}
	daily, err := normalizeLine(req.Daily)
	if err != nil {
		return nil, false, fmt.Errorf("daily: %w", err)
	}
	if daily == "" {
		return nil, false, fmt.Errorf("daily phải có ít nhất 1 mốc")
	}
	return &Schedule{Daily: daily}, len(req.Days) > 0, nil
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
