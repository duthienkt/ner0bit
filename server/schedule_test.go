package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSchedule(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "schedule.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadScheduleValid(t *testing.T) {
	p := writeSchedule(t, `{"daily":"25200:900,61200","days":{"2026-11-12":"","2026-11-13":"28800, 46800:600"}}`)
	s, err := loadSchedule(p)
	if err != nil {
		t.Fatalf("loadSchedule: %v", err)
	}
	if s.Daily != "25200:900,61200" {
		t.Errorf("daily = %q", s.Daily)
	}
	// Khoảng trắng được chuẩn hoá về dạng canonical.
	if got := s.Days["2026-11-13"]; got != "28800,46800:600" {
		t.Errorf("days[13] = %q", got)
	}
}

func TestLoadScheduleErrors(t *testing.T) {
	cases := map[string]string{
		"missing daily":  `{"days":{}}`,
		"empty daily":    `{"daily":""}`,
		"bad token":      `{"daily":"25200,abc"}`,
		"second too big": `{"daily":"86400"}`,
		"zero duration":  `{"daily":"25200:0"}`,
		"negative":       `{"daily":"-5"}`,
		"bad date key":   `{"daily":"25200","days":{"12/11/2026":""}}`,
		"bad day line":   `{"daily":"25200","days":{"2026-11-12":"xx"}}`,
		"too many":       `{"daily":"` + strings.Repeat("60,", 20) + `60"}`,
		"not json":       `daily=25200`,
		"unknown field":  `{"daily":"25200","dayz":{}}`,
		"empty token":    `{"daily":"25200,,61200"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSchedule(writeSchedule(t, content)); err == nil {
				t.Errorf("expected error for %s", content)
			}
		})
	}
	if _, err := loadSchedule(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRender(t *testing.T) {
	s := &Schedule{Daily: "25200:900,61200", Days: map[string]string{
		"2026-11-12": "",
		"2026-11-13": "28800",
	}}
	cases := []struct{ date, want string }{
		{"2026-11-11", "25200:900,61200"},        // không có dòng 2 ⇒ ESP dùng dòng 1
		{"2026-11-12", "25200:900,61200\n"},      // dòng 2 rỗng ⇒ hôm nay không tưới
		{"2026-11-13", "25200:900,61200\n28800"}, // dòng 2 thay dòng 1
	}
	for _, c := range cases {
		if got := s.render(c.date); got != c.want {
			t.Errorf("render(%s) = %q, want %q", c.date, got, c.want)
		}
	}
}

func TestMaxScheduleFitsFirmwareLimit(t *testing.T) {
	// 20 mốc dài nhất mỗi dòng phải vừa giới hạn body 1023 byte của firmware.
	line := strings.TrimSuffix(strings.Repeat("86399:1800,", 20), ",")
	p := writeSchedule(t, `{"daily":"`+line+`","days":{"2026-11-12":"`+line+`"}}`)
	s, err := loadSchedule(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.render("2026-11-12")); n > 1023 {
		t.Errorf("body %d byte > 1023", n)
	}
}
