package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testUUID = "726096c3-2ede-47f1-827e-0dc1ffe8d897"

func vt(day, hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hm, vietnam)
	if err != nil {
		panic(err)
	}
	return t
}

func TestVDay(t *testing.T) {
	cases := []struct{ day, hm, want string }{
		{"2026-11-04", "19:07", "2026-11-04"},
		{"2026-11-04", "20:07", "2026-11-05"},
		{"2026-11-05", "05:07", "2026-11-05"},
		{"2026-11-04", "23:59", "2026-11-05"},
	}
	for _, c := range cases {
		if got := VDay(vt(c.day, c.hm)); got != c.want {
			t.Errorf("VDay(%s %s) = %s, muốn %s", c.day, c.hm, got, c.want)
		}
	}
	// múi giờ khác phải được quy về giờ VN: 13:07 UTC = 20:07 VN
	if got := VDay(time.Date(2026, 11, 4, 13, 7, 0, 0, time.UTC)); got != "2026-11-05" {
		t.Errorf("VDay(UTC) = %s", got)
	}
}

func TestParseVrainFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/vrain_56.json")
	if err != nil {
		t.Fatal(err)
	}
	sum, level, err := parseVrain(body, testUUID)
	if err != nil {
		t.Fatal(err)
	}
	if sum != 0 || level != "Không mưa" {
		t.Errorf("sum=%v level=%q", sum, level)
	}
	if _, _, err := parseVrain(body, "khong-co-tram"); err == nil {
		t.Error("uuid lạ phải báo lỗi")
	}
	nul := []byte(`[{"level":"x","sumDepth":null,"station":{"uuid":"u1"}}]`)
	if _, _, err := parseVrain(nul, "u1"); err == nil {
		t.Error("sumDepth null phải báo lỗi")
	}
	if _, _, err := parseVrain([]byte(`{hỏng`), "u1"); err == nil {
		t.Error("JSON hỏng phải báo lỗi")
	}
}

func TestVrainClientLastModified(t *testing.T) {
	body, _ := os.ReadFile("testdata/vrain_56.json")
	var lm string
	var gotUA string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		if lm != "" {
			w.Header().Set("Last-Modified", lm)
		}
		w.WriteHeader(status)
		w.Write(body)
	}))
	defer srv.Close()
	c := &VrainClient{URL: srv.URL, UUID: testUUID}

	lm = "Wed, 11 Nov 2026 03:07:03 GMT"
	snap, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.DataTS.Equal(vt("2026-11-11", "10:07").Add(3 * time.Second)) {
		t.Errorf("DataTS = %v", snap.DataTS)
	}
	if snap.DataTS.Location() != vietnam {
		t.Errorf("DataTS phải ở giờ VN, có %v", snap.DataTS.Location())
	}
	if gotUA != "ner0bit-server" {
		t.Errorf("User-Agent = %q", gotUA)
	}

	lm = ""
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Error("thiếu Last-Modified phải báo lỗi")
	}

	lm = "Wed, 11 Nov 2026 03:07:03 GMT"
	status = http.StatusBadGateway
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Error("HTTP không phải 200 phải báo lỗi")
	}
	status = http.StatusOK

	// thiếu trạm, 200 nhưng dữ liệu hỏng: lỗi, không có số 0 giả
	c2 := &VrainClient{URL: srv.URL, UUID: "khong-co-tram"}
	if s, err := c2.Fetch(context.Background()); err == nil {
		t.Errorf("thiếu trạm phải báo lỗi, được %+v", s)
	}
}

func TestVrainClientNullSumDepth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Wed, 11 Nov 2026 03:07:03 GMT")
		w.Write([]byte(`[{"level":"x","sumDepth":null,"station":{"uuid":"u1"}}]`))
	}))
	defer srv.Close()
	c := &VrainClient{URL: srv.URL, UUID: "u1"}
	if s, err := c.Fetch(context.Background()); err == nil {
		t.Errorf("sumDepth null phải báo lỗi, được %+v", s)
	}
}

func rs(day, hm string, sum float64) RainSnapshot {
	ts := vt(day, hm)
	return RainSnapshot{DataTS: ts, FetchedAt: ts.Add(time.Minute), SumDepthMM: sum, Level: "x"}
}

func TestHourlyRainAcross19h(t *testing.T) {
	snaps := []RainSnapshot{
		rs("2026-11-04", "18:07", 2.2),
		rs("2026-11-04", "19:07", 2.2),
		rs("2026-11-04", "20:07", 1.2),
		rs("2026-11-04", "21:07", 1.2),
	}
	got := hourlyRain(snaps)
	want := []float64{0, 1.2, 0}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] < want[i]-1e-9 || got[i] > want[i]+1e-9 {
			t.Errorf("got %v, muốn %v", got, want)
		}
	}
	if len(hourlyRain(nil)) != 0 {
		t.Error("rỗng phải ra rỗng")
	}
}

func TestRainAnomaly(t *testing.T) {
	if m := rainAnomaly(rs("2026-11-04", "19:07", 2.2), rs("2026-11-04", "20:07", 1.2)); m != "" {
		t.Errorf("reset 20:07 là bình thường, có %q", m)
	}
	if m := rainAnomaly(rs("2026-11-04", "14:07", 5.0), rs("2026-11-04", "15:07", 3.0)); m == "" {
		t.Error("giảm trong cùng ngày phải bất thường")
	}
	if m := rainAnomaly(rs("2026-11-04", "21:07", 5.0), rs("2026-11-04", "22:07", 0)); m == "" {
		t.Error("reset lúc 22:07 phải bất thường")
	}
	if m := rainAnomaly(rs("2026-11-04", "14:07", 1.0), rs("2026-11-04", "15:07", 3.0)); m != "" {
		t.Errorf("tăng bình thường, có %q", m)
	}
}

func TestInsertRainSnapshotDedup(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := rs("2026-11-04", "19:07", 2.2)
	ok, err := s.InsertRainSnapshot(a)
	if err != nil || !ok {
		t.Fatalf("lần 1: %v %v", ok, err)
	}
	b := a
	b.SumDepthMM = 9
	ok, err = s.InsertRainSnapshot(b)
	if err != nil || ok {
		t.Fatalf("trùng data_ts phải false: %v %v", ok, err)
	}
	c := rs("2026-11-04", "20:07", 1.2)
	d := rs("2026-11-04", "21:07", 1.5)
	for _, r := range []RainSnapshot{c, d} {
		if ok, err := s.InsertRainSnapshot(r); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}

	got, ok, err := s.LatestRain("2026-11-04", vt("2026-11-04", "19:30"))
	if err != nil || !ok || got.SumDepthMM != 2.2 || !got.DataTS.Equal(a.DataTS) {
		t.Fatalf("LatestRain vday cũ: %+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.LatestRain("2026-11-04", vt("2026-11-04", "19:00")); ok {
		t.Error("chưa có snapshot nào trước 19:00")
	}
	got, ok, err = s.LatestRain("2026-11-05", vt("2026-11-04", "22:00"))
	if err != nil || !ok || got.SumDepthMM != 1.5 || got.Level != "x" {
		t.Fatalf("LatestRain vday mới: %+v %v %v", got, ok, err)
	}
	if got.DataTS.Location() != vietnam {
		t.Error("DataTS phải ở giờ VN")
	}

	list, err := s.RainSince(vt("2026-11-04", "20:00"))
	if err != nil || len(list) != 2 || list[0].SumDepthMM != 1.2 || list[1].SumDepthMM != 1.5 {
		t.Fatalf("RainSince: %+v %v", list, err)
	}
}

func TestAddWarning(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddWarning(vt("2026-11-04", "19:07"), "vrain", "thử"); err != nil {
		t.Fatal(err)
	}
	var n int
	var src, msg string
	if err := s.db.QueryRow(`SELECT count(*), source, message FROM warnings`).Scan(&n, &src, &msg); err != nil || n != 1 || src != "vrain" || msg != "thử" {
		t.Fatalf("%d %s %s %v", n, src, msg, err)
	}
}
