package main

import (
	"strings"
	"testing"
)

func TestParseScheduleNormalizes(t *testing.T) {
	s, hasDays, err := parseSchedule([]byte(`{"daily":" 25200:900 , 61200 "}`))
	if err != nil || hasDays {
		t.Fatalf("err=%v hasDays=%v", err, hasDays)
	}
	if s.Daily != "25200:900,61200" {
		t.Errorf("daily = %q", s.Daily)
	}
}

func TestParseScheduleErrors(t *testing.T) {
	cases := map[string]string{
		"missing daily":  `{"days":{}}`,
		"empty daily":    `{"daily":""}`,
		"bad token":      `{"daily":"25200,abc"}`,
		"second too big": `{"daily":"86400"}`,
		"zero duration":  `{"daily":"25200:0"}`,
		"negative":       `{"daily":"-5"}`,
		"too many":       `{"daily":"` + strings.Repeat("60,", 20) + `60"}`,
		"not json":       `daily=25200`,
		"unknown field":  `{"daily":"25200","dayz":{}}`,
		"empty token":    `{"daily":"25200,,61200"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseSchedule([]byte(content)); err == nil {
				t.Errorf("expected error for %s", content)
			}
		})
	}
}

func TestParseScheduleDetectsDays(t *testing.T) {
	if _, hasDays, err := parseSchedule([]byte(`{"daily":"3600","days":{"2026-11-14":"1"}}`)); err != nil || !hasDays {
		t.Errorf("hasDays=%v err=%v", hasDays, err)
	}
	if _, hasDays, err := parseSchedule([]byte(`{"daily":"3600","days":{}}`)); err != nil || hasDays {
		t.Errorf("days rỗng: hasDays=%v err=%v", hasDays, err)
	}
}

func TestMaxScheduleFitsFirmwareLimit(t *testing.T) {
	// 20 mốc dài nhất mỗi dòng phải vừa giới hạn body 1023 byte của firmware.
	line := strings.TrimSuffix(strings.Repeat("86399:1800,", 20), ",")
	if n := len(line + "\n" + line); n > 1023 {
		t.Errorf("body %d byte > 1023", n)
	}
}
