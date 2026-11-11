package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreOpenMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	for i := 0; i < 2; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatalf("lần mở %d: %v", i, err)
		}
		var v int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 1 {
			t.Fatalf("user_version = %d (%v), muốn 1", v, err)
		}
		for _, tbl := range []string{"heartbeats", "events", "rain_snapshots", "metar_obs", "days",
			"blocks", "sessions", "schedule_daily", "settings", "warnings"} {
			var n int
			if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&n); err != nil || n != 1 {
				t.Errorf("thiếu bảng %s", tbl)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoreParamsRoundTrip(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Params()
	if err != nil || !reflect.DeepEqual(got, DefaultParams()) {
		t.Fatalf("chưa đặt phải trả mặc định: %+v %v", got, err)
	}
	p := DefaultParams()
	p.AutoEnabled = true
	p.AutoEnabledAt = p.AutoEnabledAt.Add(0)
	p.RainHiMM = 12
	if err := s.SetParams(p); err != nil {
		t.Fatal(err)
	}
	got, err = s.Params()
	if err != nil {
		t.Fatal(err)
	}
	if !got.AutoEnabled || got.RainHiMM != 12 || len(got.Slots) != 3 {
		t.Errorf("round trip sai: %+v", got)
	}
	p.BlockS = 1801
	if err := s.SetParams(p); err == nil {
		t.Error("SetParams phải gọi Validate")
	}
}

func TestStoreDailyRoundTrip(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if d, err := s.Daily(); err != nil || d != "" {
		t.Fatalf("chưa đặt: %q %v", d, err)
	}
	if err := s.SetDaily(" 18900:900 , 22500:900"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Daily(); d != "18900:900,22500:900" {
		t.Errorf("Daily = %q", d)
	}
	if err := s.SetDaily(""); err == nil {
		t.Error("SetDaily rỗng phải lỗi")
	}
}
