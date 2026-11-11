package main

import (
	"encoding/json"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRecordHeartbeatStoresEvents(t *testing.T) {
	st := openTestStore(t)
	at := time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)
	evs := []json.RawMessage{
		json.RawMessage(`{"type":"start","at":"2026-11-12T07:00:00+07:00","source":"today"}`),
		json.RawMessage(`{"type":"stop","at":"2026-11-12T07:05:00+07:00","source":"today","reason":"done"}`),
	}
	raw := []byte(`{"id":"abc","events":[]}`)
	if err := st.RecordHeartbeat(at, "abc", raw, evs); err != nil {
		t.Fatal(err)
	}
	got, err := st.Events(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d events", len(got))
	}
	var first, second map[string]any
	_ = json.Unmarshal(got[0], &first)
	_ = json.Unmarshal(got[1], &second)
	if first["type"] != "stop" || first["id"] != "abc" || first["reason"] != "done" || first["received_at"] != "2026-11-12T08:00:00+07:00" {
		t.Errorf("event đầu: %s", got[0])
	}
	if second["type"] != "start" {
		t.Errorf("event hai: %s", got[1])
	}
	if _, has := second["reason"]; has {
		t.Errorf("reason rỗng phải bị bỏ: %s", got[1])
	}
	if n, _ := st.Events(1); len(n) != 1 {
		t.Errorf("limit 1: %d", len(n))
	}

	lat, lraw, ok, err := st.LastHeartbeat()
	if err != nil || !ok || !lat.Equal(at) || string(lraw) != string(raw) {
		t.Errorf("LastHeartbeat = %v %s %v %v", lat, lraw, ok, err)
	}
}

func TestLastHeartbeatEmpty(t *testing.T) {
	_, _, ok, err := openTestStore(t).LastHeartbeat()
	if err != nil || ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}
