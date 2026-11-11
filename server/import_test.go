package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportLegacy(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("schedule.json", `{"daily":"18000:900,19800:900,61200:900","days":{
		"2026-11-10":"64200:60,65100:60,66000:120,66900:60,67800:60",
		"2026-11-11":"18000:900,19800:900,34560,61200:900",
		"2026-11-12":""}}`)
	write("heartbeats.jsonl",
		`{"received_at":"2026-11-10T20:00:00+07:00","heartbeat":{"id":"AABBCCDDEEFF","events":[{"type":"start","at":"x"}]}}`+"\n"+
			`not json`+"\n"+
			`{"received_at":"2026-11-10T20:05:00+07:00","heartbeat":{"id":"AABBCCDDEEFF","events":[]}}`+"\n")
	write("events.jsonl",
		`{"type":"start","at":"2026-11-10T17:15:00","source":"schedule","id":"AABBCCDDEEFF","received_at":"2026-11-10T17:16:00+07:00"}`+"\n"+
			`{"type":"stop","at":"2026-11-10T17:30:00","source":"schedule","reason":"done","id":"AABBCCDDEEFF","received_at":"2026-11-10T17:31:00+07:00"}`+"\n"+
			"garbage\n"+
			`{"type":"start","at":"2026-11-10T18:00:00","source":"manual","id":"AABBCCDDEEFF","received_at":"2026-11-10T18:01:00+07:00"}`+"\n")
	before, _ := os.ReadFile(filepath.Join(dir, "schedule.json"))

	st := openTestStore(t)
	ok, err := ImportLegacy(st, dir, "2026-11-11")
	if err != nil || !ok {
		t.Fatalf("ImportLegacy = %v, %v", ok, err)
	}
	if d, _ := st.Daily(); d != "18000:900,19800:900,61200:900" {
		t.Errorf("daily = %q", d)
	}
	s10, _ := st.Sessions("2026-11-10")
	if len(s10) != 5 {
		t.Errorf("ngày 10 có %d phiên, muốn 5", len(s10))
	}
	for _, s := range s10 {
		if s.Source != sessionManual || s.State != stateFinal {
			t.Errorf("phiên %+v không phải manual/final", s)
		}
	}
	s11, _ := st.Sessions("2026-11-11")
	if len(s11) != 1 || s11[0].StartS != 34560 || s11[0].RunS != 300 || s11[0].Source != sessionManual {
		t.Errorf("ngày 11 = %+v, muốn đúng 1 phiên manual 34560:300", s11)
	}
	if s12, _ := st.Sessions("2026-11-12"); len(s12) != 0 {
		t.Errorf("ngày rỗng không được nhập: %+v", s12)
	}
	ev, err := st.Events(10)
	if err != nil || len(ev) != 3 {
		t.Fatalf("Events = %d, %v; muốn 3", len(ev), err)
	}
	if !strings.Contains(string(ev[0]), `"received_at":"2026-11-10T18:01:00+07:00"`) {
		t.Errorf("received_at gốc không được giữ: %s", ev[0])
	}
	at, _, hok, _ := st.LastHeartbeat()
	if !hok || at.Format("2006-01-02T15:04:05Z07:00") != "2026-11-10T20:05:00+07:00" {
		t.Errorf("heartbeat cuối = %v, %v", at, hok)
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM heartbeats`).Scan(&n)
	if n != 2 {
		t.Errorf("heartbeats = %d, muốn 2", n)
	}

	if ok, err := ImportLegacy(st, dir, "2026-11-11"); ok || err != nil {
		t.Errorf("lần 2 = %v, %v; muốn false, nil", ok, err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "schedule.json")); string(after) != string(before) {
		t.Error("file cũ bị sửa")
	}
}

func TestImportLegacyMissingFiles(t *testing.T) {
	st := openTestStore(t)
	if ok, err := ImportLegacy(st, t.TempDir(), "2026-11-11"); ok || err != nil {
		t.Errorf("= %v, %v; muốn false, nil", ok, err)
	}
}
