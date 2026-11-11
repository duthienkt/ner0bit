package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

const latTest = 10.8136

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, muốn %.4f ± %.3f", name, got, want, tol)
	}
}

func TestRaMM(t *testing.T) {
	near(t, "ra 2026-11-04", raMM(vt("2026-11-04", "00:00"), latTest), 13.451, 0.01)
	near(t, "ra 2026-06-21", raMM(vt("2026-06-21", "00:00"), latTest), 15.157, 0.01)
	near(t, "ra 2026-01-01", raMM(vt("2026-01-01", "00:00"), latTest), 12.55, 0.01)
}

func TestHargreaves(t *testing.T) {
	ra := raMM(vt("2026-11-04", "00:00"), latTest)
	near(t, "ET 34/25", hargreaves(34, 25, ra), 4.39, 0.01)
	near(t, "ET 29/25", hargreaves(29, 25, ra), 2.772, 0.01)
}

func f64(v float64) *float64 { return &v }

// obsSeries tạo n quan trắc cách nhau 30 phút từ 05:00, nhiệt độ xen kẽ tmax/tmin.
func obsSeries(day string, n int, tmax, tmin float64) []MetarObs {
	var out []MetarObs
	for i := 0; i < n; i++ {
		tt := tmin
		if i%2 == 0 {
			tt = tmax
		}
		out = append(out, MetarObs{ObsTime: vt(day, "05:00").Add(time.Duration(i) * 30 * time.Minute), TempC: f64(tt)})
	}
	return out
}

func TestSunFactor(t *testing.T) {
	const day = "2026-11-04"
	s, et, ok := SunFactor(obsSeries(day, 17, 34, 25), day, 4.0)
	if !ok || s != 1 {
		t.Errorf("nắng: s=%v ok=%v, muốn 1,true", s, ok)
	}
	near(t, "et", et, 4.39, 0.01)

	s, _, ok = SunFactor(obsSeries(day, 17, 29, 25), day, 4.0)
	if !ok {
		t.Fatal("ok=false")
	}
	near(t, "s âm u", s, 0.693, 0.005)

	s, _, ok = SunFactor(obsSeries(day, 11, 34, 25), day, 4.0)
	if ok || s != 1 {
		t.Errorf("11 quan trắc: s=%v ok=%v, muốn 1,false", s, ok)
	}

	// 11 quan trắc + 1 lúc 17:30 (ngoài cửa sổ) vẫn chưa đủ 12.
	late := append(obsSeries(day, 11, 34, 25), MetarObs{ObsTime: vt(day, "17:30"), TempC: f64(20)})
	if _, _, ok = SunFactor(late, day, 4.0); ok {
		t.Error("quan trắc 17:30 không được tính")
	}
	// Và không làm lệch tmin: 17 quan trắc + 17:30 lạnh vẫn ra s = 1.
	more := append(obsSeries(day, 17, 34, 25), MetarObs{ObsTime: vt(day, "17:30"), TempC: f64(5)})
	if s, et, _ := SunFactor(more, day, 4.0); s != 1 {
		t.Errorf("17:30 làm lệch: s=%v et=%v", s, et)
	} else {
		near(t, "et không đổi", et, 4.39, 0.01)
	}

	// Null temp bị bỏ qua.
	nul := append(obsSeries(day, 11, 34, 25), MetarObs{ObsTime: vt(day, "13:00")}, MetarObs{ObsTime: vt(day, "13:30")})
	if _, _, ok = SunFactor(nul, day, 4.0); ok {
		t.Error("obs null temp không được tính")
	}
}

func TestParseMetarFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/metar_vvts.json")
	if err != nil {
		t.Fatal(err)
	}
	obs, err := parseMetar(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 30 {
		t.Fatalf("có %d bản ghi, muốn 30", len(obs))
	}
	if obs[0].ObsTime.Unix() != 1794373200 {
		t.Errorf("ObsTime = %d, muốn 1794373200", obs[0].ObsTime.Unix())
	}
	if obs[0].ObsTime.Location() != vietnam {
		t.Error("ObsTime phải ở giờ VN")
	}
	if obs[0].TempC == nil || *obs[0].TempC != 32 || obs[0].DewpC == nil || *obs[0].DewpC != 23 {
		t.Errorf("temp/dewp sai: %v %v", obs[0].TempC, obs[0].DewpC)
	}
	if obs[0].Raw == "" || obs[0].CloudsJSON != `[{"cover":"BKN","base":2800}]` {
		t.Errorf("raw/clouds sai: %q %q", obs[0].Raw, obs[0].CloudsJSON)
	}
	if _, err := parseMetar([]byte(`oops`)); err == nil {
		t.Error("JSON hỏng phải lỗi")
	}
}

func TestParseMetarNullTemp(t *testing.T) {
	obs, err := parseMetar([]byte(`[{"obsTime":1794373200,"temp":null,"dewp":null,"rawOb":"X"}]`))
	if err != nil || len(obs) != 1 || obs[0].TempC != nil || obs[0].CloudsJSON != "[]" {
		t.Fatalf("obs=%+v err=%v", obs, err)
	}
}

func TestMetarStore(t *testing.T) {
	s := openTestStore(t)
	base := vt("2026-11-04", "05:00")
	in := []MetarObs{
		{ObsTime: base, TempC: f64(30), DewpC: f64(22), CloudsJSON: `[]`, Raw: "a"},
		{ObsTime: base.Add(30 * time.Minute), CloudsJSON: `[]`, Raw: "b"}, // temp null
	}
	if err := s.InsertMetar(in); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetar(in); err != nil { // trùng bị bỏ
		t.Fatal(err)
	}
	got, err := s.MetarBetween(base, base.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Raw != "a" || *got[0].TempC != 30 {
		t.Fatalf("[from,to) sai: %+v", got)
	}
	got, _ = s.MetarBetween(base, base.Add(2*time.Hour))
	if len(got) != 2 || got[1].TempC != nil || got[1].DewpC != nil {
		t.Fatalf("null temp phải giữ nil: %+v", got)
	}
	if !got[0].ObsTime.Equal(base) {
		t.Errorf("ObsTime %v", got[0].ObsTime)
	}
}

func TestMetarClient(t *testing.T) {
	var ua string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.WriteHeader(status)
		w.Write([]byte(`[{"obsTime":1794373200,"temp":32,"dewp":23,"rawOb":"X"}]`))
	}))
	defer srv.Close()
	c := &MetarClient{URL: srv.URL}
	obs, err := c.Fetch(context.Background())
	if err != nil || len(obs) != 1 || ua != "ner0bit-server" {
		t.Fatalf("obs=%v err=%v ua=%q", obs, err, ua)
	}
	if c.HTTP != nil {
		t.Error("Fetch không được đổi c.HTTP")
	}
	status = http.StatusBadGateway
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Error("HTTP 502 phải lỗi")
	}
}
