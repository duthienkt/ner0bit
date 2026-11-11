package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeSnap là một snapshot Vrain giả, chỉ trả về từ thời điểm avail.
type fakeSnap struct {
	avail time.Time
	s     RainSnapshot
}

// fakeRain trả snapshot mới nhất đã "có" tại *now (giống Vrain chỉ có số hiện tại).
type fakeRain struct {
	now   *time.Time
	snaps []fakeSnap
	fail  bool
	calls int
}

func (f *fakeRain) Fetch(ctx context.Context) (RainSnapshot, error) {
	f.calls++
	if f.fail {
		return RainSnapshot{}, errors.New("giả: mạng lỗi")
	}
	var best *RainSnapshot
	for i := range f.snaps {
		if !f.snaps[i].avail.After(*f.now) {
			best = &f.snaps[i].s
		}
	}
	if best == nil {
		return RainSnapshot{}, errors.New("giả: chưa có dữ liệu")
	}
	r := *best
	r.FetchedAt = *f.now
	return r, nil
}

// hourlySnaps tạo snapshot :07 mỗi giờ trong [from, to], có ngay lúc :07; sum(t) cho số đo.
func hourlySnaps(from, to time.Time, sum func(t time.Time) float64) []fakeSnap {
	var out []fakeSnap
	for t := from; !t.After(to); t = t.Add(time.Hour) {
		out = append(out, fakeSnap{avail: t, s: RainSnapshot{DataTS: t, SumDepthMM: sum(t)}})
	}
	return out
}

// fakeMetar trả các quan trắc có ObsTime ≤ *now.
type fakeMetar struct {
	now  *time.Time
	obs  []MetarObs
	fail bool
}

func (f *fakeMetar) Fetch(ctx context.Context) ([]MetarObs, error) {
	if f.fail {
		return nil, errors.New("giả: METAR lỗi")
	}
	var out []MetarObs
	for _, o := range f.obs {
		if !o.ObsTime.After(*f.now) {
			out = append(out, o)
		}
	}
	return out, nil
}

// autoParams: tham số mặc định, bật tự động từ lâu.
func autoParams(t *testing.T, st *Store, enabledAt time.Time) Params {
	t.Helper()
	p := DefaultParams()
	p.AutoEnabled = true
	p.AutoEnabledAt = enabledAt
	if err := st.SetParams(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// runTicks gọi Tick mỗi phút trong [from, to].
func runTicks(t *testing.T, sc *Scheduler, clock *time.Time, from, to time.Time) {
	t.Helper()
	for tm := from; !tm.After(to); tm = tm.Add(time.Minute) {
		*clock = tm
		if err := sc.Tick(context.Background()); err != nil {
			t.Fatalf("Tick %s: %v", tm.Format("2006-01-02 15:04"), err)
		}
	}
}

func autoSessions(t *testing.T, st *Store, date string) []Session {
	t.Helper()
	all, err := st.Sessions(date)
	if err != nil {
		t.Fatal(err)
	}
	var out []Session
	for _, s := range all {
		if s.Source == sessionAuto {
			out = append(out, s)
		}
	}
	return out
}

func warningsLike(t *testing.T, st *Store, sub string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT count(*) FROM warnings WHERE message LIKE ?`, "%"+sub+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// dayRow đọc final_sum/closed_at của ngày Vrain; closed_at rỗng ⇒ chưa đóng.
func dayRow(t *testing.T, st *Store, vday string) (final float64, closedAt string) {
	t.Helper()
	var f sql.NullFloat64
	var c sql.NullString
	err := st.db.QueryRow(`SELECT final_sum, closed_at FROM days WHERE vday = ?`, vday).Scan(&f, &c)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return f.Float64, c.String
}

func sunnyObs(days ...string) []MetarObs {
	var out []MetarObs
	for _, d := range days {
		out = append(out, obsSeries(d, 24, 34, 25)...)
	}
	return out
}

func checkAuto(t *testing.T, got []Session, starts, runs []int, decidedAt []string) {
	t.Helper()
	if len(got) != len(starts) {
		t.Fatalf("có %d phiên auto, muốn %d: %+v", len(got), len(starts), got)
	}
	for i, s := range got {
		if s.StartS != starts[i] || s.RunS != runs[i] {
			t.Errorf("phiên %d = %d:%d, muốn %d:%d (%s)", i, s.StartS, s.RunS, starts[i], runs[i], s.Reason)
		}
		if at := s.CreatedAt.In(vietnam).Format("15:04"); decidedAt != nil && at != decidedAt[i] {
			t.Errorf("phiên %d chốt lúc %s, muốn %s", i, at, decidedAt[i])
		}
		wantState := stateFinal
		if runs[i] == 0 {
			wantState = stateSkipped
		}
		if s.State != wantState || s.VDay == "" || s.SlotS == nil || *s.SlotS != starts[i] || s.Reason == "" {
			t.Errorf("phiên %d: state %q vday %q slot %v reason %q", i, s.State, s.VDay, s.SlotS, s.Reason)
		}
	}
}

func TestSchedulerFullDay(t *testing.T) {
	st := openTestStore(t)
	autoParams(t, st, vt("2026-11-01", "00:00"))
	clock := vt("2026-11-10", "00:00")
	rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-09", "20:07"), vt("2026-11-11", "07:07"),
		func(tm time.Time) float64 {
			// Số liệu 10/11 (§4.4): 0 tới 16:07, 5.6 từ 17:07; ngày Vrain 11 không mưa.
			if VDay(tm) == "2026-11-10" && tm.Hour() >= 17 {
				return 5.6
			}
			return 0
		})}
	metar := &fakeMetar{now: &clock, obs: sunnyObs("2026-11-10", "2026-11-11")}
	sc := &Scheduler{Store: st, Rain: rain, Metar: metar, Now: func() time.Time { return clock }}

	runTicks(t, sc, &clock, vt("2026-11-10", "00:00"), vt("2026-11-10", "20:08"))
	if _, c := dayRow(t, st, "2026-11-10"); c != "" {
		t.Fatalf("ngày Vrain 10 đóng sớm lúc %s", c)
	}
	runTicks(t, sc, &clock, vt("2026-11-10", "20:09"), vt("2026-11-11", "07:00"))

	checkAuto(t, autoSessions(t, st, "2026-11-10"), []int{18900, 22500, 62100}, []int{900, 900, 600},
		[]string{"05:09", "06:09", "17:09"})
	final, closed := dayRow(t, st, "2026-11-10")
	if final != 5.6 || !strings.HasPrefix(closed, "2026-11-10T20:09") {
		t.Errorf("ngày Vrain 10: final %v closed %q, muốn 5.6 đóng 20:09", final, closed)
	}
	l, ok, err := st.Ledger("2026-11-11")
	if err != nil || !ok || l.CarryInS != 0 {
		t.Errorf("sổ ngày 11: %+v %v %v, muốn carry 0", l, ok, err)
	}
	checkAuto(t, autoSessions(t, st, "2026-11-11"), []int{18900, 22500}, []int{900, 900}, []string{"05:09", "06:09"})

	var sun, et sql.NullFloat64
	if err := st.db.QueryRow(`SELECT sun_s, et FROM days WHERE vday='2026-11-10'`).Scan(&sun, &et); err != nil {
		t.Fatal(err)
	}
	if !sun.Valid || sun.Float64 != 1 || !et.Valid || et.Float64 <= 4 {
		t.Errorf("sun_s %v et %v", sun, et)
	}
	// Có snapshot của giờ ngay ở :09 ⇒ mỗi giờ chỉ gọi Vrain 1 lần (00:09 ngày 10 … 06:09 ngày 11).
	if rain.calls != 31 {
		t.Errorf("gọi Vrain %d lần, muốn 31", rain.calls)
	}
	var nw int
	st.db.QueryRow(`SELECT count(*) FROM warnings`).Scan(&nw)
	if nw != 0 {
		t.Errorf("có %d cảnh báo, muốn 0", nw)
	}
}

func TestSchedulerWaitsForVrainUntil11(t *testing.T) {
	t.Run("05:07 có lúc 05:10", func(t *testing.T) {
		st := openTestStore(t)
		autoParams(t, st, vt("2026-11-01", "00:00"))
		clock := vt("2026-11-10", "04:00")
		snaps := hourlySnaps(vt("2026-11-10", "03:07"), vt("2026-11-10", "04:07"), func(time.Time) float64 { return 0 })
		snaps = append(snaps, fakeSnap{avail: vt("2026-11-10", "05:10"),
			s: RainSnapshot{DataTS: vt("2026-11-10", "05:07"), SumDepthMM: 6}})
		rain := &fakeRain{now: &clock, snaps: snaps}
		sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock}, Now: func() time.Time { return clock }}
		runTicks(t, sc, &clock, vt("2026-11-10", "04:00"), vt("2026-11-10", "05:20"))
		got := autoSessions(t, st, "2026-11-10")
		checkAuto(t, got, []int{18900}, []int{360}, []string{"05:10"})
		if !strings.Contains(got[0].Reason, "6.0 mm") {
			t.Errorf("reason %q không dùng số mới", got[0].Reason)
		}
		if n := warningsLike(t, st, "Vrain trễ"); n != 0 {
			t.Errorf("%d cảnh báo Vrain trễ, muốn 0", n)
		}
	})
	t.Run("05:07 không có", func(t *testing.T) {
		st := openTestStore(t)
		autoParams(t, st, vt("2026-11-01", "00:00"))
		clock := vt("2026-11-10", "04:00")
		rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-10", "03:07"), vt("2026-11-10", "04:07"),
			func(tm time.Time) float64 { return 5.6 })}
		sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock}, Now: func() time.Time { return clock }}
		runTicks(t, sc, &clock, vt("2026-11-10", "04:00"), vt("2026-11-10", "05:08"))
		before := rain.calls
		runTicks(t, sc, &clock, vt("2026-11-10", "05:09"), vt("2026-11-10", "05:20"))
		got := autoSessions(t, st, "2026-11-10")
		checkAuto(t, got, []int{18900}, []int{600}, []string{"05:11"})
		if !strings.Contains(got[0].Reason, "5.6 mm") {
			t.Errorf("reason %q không dùng snapshot 04:07", got[0].Reason)
		}
		if n := warningsLike(t, st, "Vrain trễ"); n != 1 {
			t.Errorf("%d cảnh báo Vrain trễ, muốn 1", n)
		}
		if n := rain.calls - before; n != 3 {
			t.Errorf("gọi Vrain %d lần trong 05:09–05:20, muốn 3 (:09, :10, :11)", n)
		}
	})
}

func TestSchedulerRestartNoDoubleDecide(t *testing.T) {
	t.Run("đã chốt 05:15", func(t *testing.T) {
		st := openTestStore(t)
		autoParams(t, st, vt("2026-11-01", "00:00"))
		clock := vt("2026-11-10", "04:00")
		rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-09", "20:07"), vt("2026-11-10", "07:07"),
			func(time.Time) float64 { return 6 })}
		metar := &fakeMetar{now: &clock}
		sc1 := &Scheduler{Store: st, Rain: rain, Metar: metar, Now: func() time.Time { return clock }}
		runTicks(t, sc1, &clock, vt("2026-11-10", "04:00"), vt("2026-11-10", "05:15"))
		l1, _, err := st.Ledger("2026-11-10")
		if err != nil || l1.UsedRainS != 540 {
			t.Fatalf("sổ sau 05:15: %+v %v, muốn used_rain 540", l1, err)
		}

		sc2 := &Scheduler{Store: st, Rain: rain, Metar: metar, Now: func() time.Time { return clock }}
		runTicks(t, sc2, &clock, vt("2026-11-10", "05:40"), vt("2026-11-10", "05:40"))
		if l, _, _ := st.Ledger("2026-11-10"); l != l1 {
			t.Errorf("sổ đổi sau restart: %+v, trước %+v", l, l1)
		}
		runTicks(t, sc2, &clock, vt("2026-11-10", "05:41"), vt("2026-11-10", "06:20"))
		checkAuto(t, autoSessions(t, st, "2026-11-10"), []int{18900, 22500}, []int{360, 900}, []string{"05:09", "06:09"})
		if l, _, _ := st.Ledger("2026-11-10"); l.UsedRainS != 540 {
			t.Errorf("used_rain = %v sau 06:15, muốn 540", l.UsedRainS)
		}
	})
	t.Run("lỡ mốc 05:09 ⇒ chốt bù khi khởi động", func(t *testing.T) {
		st := openTestStore(t)
		autoParams(t, st, vt("2026-11-01", "00:00"))
		clock := vt("2026-11-10", "05:40")
		rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-09", "20:07"), vt("2026-11-10", "07:07"),
			func(time.Time) float64 { return 0 })}
		sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock}, Now: func() time.Time { return clock }}
		// Server chết lúc 05:09 nên không có snapshot 05:07; chốt ngay khi khởi động.
		runTicks(t, sc, &clock, vt("2026-11-10", "05:40"), vt("2026-11-10", "05:41"))
		checkAuto(t, autoSessions(t, st, "2026-11-10"), []int{18900}, []int{900}, []string{"05:40"})
	})
}

func TestSchedulerCloseDayWithout2007(t *testing.T) {
	st := openTestStore(t)
	autoParams(t, st, vt("2026-11-01", "00:00"))
	clock := vt("2026-11-02", "04:00")
	// 02/11: 05:07/06:07 = 0, 17:07 = 1.0, mưa 18h, 19:07 = 17.8; Vrain không có bản 20:07.
	rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-01", "20:07"), vt("2026-11-02", "19:07"),
		func(tm time.Time) float64 {
			switch {
			case tm.Hour() == 17:
				return 1.0
			case tm.Hour() == 18:
				return 10
			case tm.Hour() == 19:
				return 17.8
			}
			return 0
		})}
	sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock, obs: sunnyObs("2026-11-02")},
		Now: func() time.Time { return clock }}
	runTicks(t, sc, &clock, vt("2026-11-02", "04:00"), vt("2026-11-02", "20:11"))
	if _, c := dayRow(t, st, "2026-11-02"); c != "" {
		t.Fatalf("đóng sớm lúc %s", c)
	}
	runTicks(t, sc, &clock, vt("2026-11-02", "20:12"), vt("2026-11-02", "20:30"))
	checkAuto(t, autoSessions(t, st, "2026-11-02"), []int{18900, 22500, 62100}, []int{900, 900, 900}, nil)
	final, closed := dayRow(t, st, "2026-11-02")
	if final != 17.8 || !strings.HasPrefix(closed, "2026-11-02T20:12") {
		t.Errorf("final %v closed %q, muốn 17.8 lúc 20:12", final, closed)
	}
	if l, ok, err := st.Ledger("2026-11-03"); err != nil || !ok || l.CarryInS != 1800 {
		t.Errorf("sổ 03/11: %+v %v %v, muốn carry 1800", l, ok, err)
	}
	if open, err := st.OpenDays(); err != nil || len(open) != 1 || open[0] != "2026-11-03" {
		t.Errorf("OpenDays = %v %v, muốn [2026-11-03]", open, err)
	}
}

func TestSchedulerClosesDaysWhenAutoOff(t *testing.T) {
	// R6: đóng ngày/tính carry không phụ thuộc chế độ tự động; lỗi Vrain chỉ cảnh báo 1 lần mỗi giờ.
	st := openTestStore(t)
	if _, err := st.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-02", "19:07"), FetchedAt: vt("2026-11-02", "19:09"), SumDepthMM: 17.8}); err != nil {
		t.Fatal(err)
	}
	clock := vt("2026-11-02", "20:00")
	rain := &fakeRain{now: &clock, fail: true}
	sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock, fail: true}, Now: func() time.Time { return clock }}
	runTicks(t, sc, &clock, vt("2026-11-02", "20:00"), vt("2026-11-02", "20:12"))
	if final, closed := dayRow(t, st, "2026-11-02"); final != 17.8 || !strings.HasPrefix(closed, "2026-11-02T20:12") {
		t.Errorf("final %v closed %q", final, closed)
	}
	if l, _, _ := st.Ledger("2026-11-03"); l.CarryInS != 1800 {
		t.Errorf("carry %v, muốn 1800", l.CarryInS)
	}
	if got := autoSessions(t, st, "2026-11-02"); len(got) != 0 {
		t.Errorf("tự động tắt mà có phiên auto: %+v", got)
	}
	if rain.calls != 3 {
		t.Errorf("gọi Vrain %d lần, muốn 3", rain.calls)
	}
	var nv, nm int
	st.db.QueryRow(`SELECT count(*) FROM warnings WHERE source='vrain'`).Scan(&nv)
	st.db.QueryRow(`SELECT count(*) FROM warnings WHERE source='metar'`).Scan(&nm)
	if nv != 1 || nm != 1 {
		t.Errorf("cảnh báo vrain %d metar %d, muốn 1/1", nv, nm)
	}
}

func TestSchedulerPMWithoutMetarWarns(t *testing.T) {
	// R5: thiếu METAR ⇒ S = 1 và có cảnh báo.
	st := openTestStore(t)
	autoParams(t, st, vt("2026-11-01", "00:00"))
	clock := vt("2026-11-04", "17:00")
	rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-04", "16:07"), vt("2026-11-04", "17:07"),
		func(time.Time) float64 { return 0 })}
	sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock, obs: obsSeries("2026-11-04", 5, 29, 25)},
		Now: func() time.Time { return clock }}
	// Đã chốt 2 lần sáng để chỉ còn lần chiều.
	for _, s := range DefaultParams().Slots[:2] {
		l, _, _ := st.Ledger("2026-11-04")
		d, l2 := decide(l, s, 0, 1, DefaultParams())
		if err := st.SaveDecision("2026-11-04", d, l2, stateFinal, vt("2026-11-04", "05:09")); err != nil {
			t.Fatal(err)
		}
	}
	runTicks(t, sc, &clock, vt("2026-11-04", "17:00"), vt("2026-11-04", "17:10"))
	got := autoSessions(t, st, "2026-11-04")
	if len(got) != 3 || got[2].RunS != 900 {
		t.Fatalf("phiên: %+v", got)
	}
	if n := warningsLike(t, st, "METAR"); n != 1 {
		t.Errorf("%d cảnh báo METAR, muốn 1", n)
	}
}

func TestEnableMidDaySkipsPast(t *testing.T) {
	st := openTestStore(t)
	autoParams(t, st, vt("2026-11-10", "05:30"))
	clock := vt("2026-11-10", "05:30")
	rain := &fakeRain{now: &clock, snaps: hourlySnaps(vt("2026-11-10", "04:07"), vt("2026-11-10", "07:07"),
		func(time.Time) float64 { return 6 })}
	for _, s := range rain.snaps[:2] {
		st.InsertRainSnapshot(s.s)
	}
	sc := &Scheduler{Store: st, Rain: rain, Metar: &fakeMetar{now: &clock}, Now: func() time.Time { return clock }}
	runTicks(t, sc, &clock, vt("2026-11-10", "05:30"), vt("2026-11-10", "05:30"))
	got := autoSessions(t, st, "2026-11-10")
	if len(got) != 1 || got[0].StartS != 18900 || got[0].State != stateSkipped || got[0].RunS != 0 ||
		got[0].Reason != "bật tự động sau giờ chốt" {
		t.Fatalf("phiên: %+v", got)
	}
	if l, _, err := st.Ledger("2026-11-10"); err != nil || l.UsedRainS != 0 || l.UsedCarryS != 0 {
		t.Errorf("sổ bị đụng: %+v %v", l, err)
	}
	runTicks(t, sc, &clock, vt("2026-11-10", "05:31"), vt("2026-11-10", "06:10"))
	checkAuto(t, autoSessions(t, st, "2026-11-10"), []int{18900, 22500}, []int{0, 360}, []string{"05:30", "06:09"})
	if l, _, _ := st.Ledger("2026-11-10"); l.UsedRainS != 540 {
		t.Errorf("used_rain %v, muốn 540", l.UsedRainS)
	}
}

func TestSchedulerRunRecoversAndStops(t *testing.T) {
	st := openTestStore(t)
	clock := vt("2026-11-10", "05:09")
	ticked := make(chan struct{}, 1)
	sc := &Scheduler{Store: st, Rain: panicRain{ticked}, Metar: &fakeMetar{now: &clock},
		Now: func() time.Time { return clock }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sc.Run(ctx); close(done) }()
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Fatal("Run không gọi Tick ngay")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run không dừng khi ctx huỷ")
	}
}

type panicRain struct{ ticked chan struct{} }

func (p panicRain) Fetch(ctx context.Context) (RainSnapshot, error) {
	select {
	case p.ticked <- struct{}{}:
	default:
	}
	panic("giả: panic trong Fetch")
}

// ---- deviceBody khi bật tự động ----

func autoServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newTestServer(t, threeSlots)
	autoParams(t, s.Store, vt("2026-11-01", "00:00"))
	return s
}

func TestDeviceBodyAutoOnMidnight(t *testing.T) {
	now := vt("2026-11-12", "00:05")
	t.Run("carry 0", func(t *testing.T) {
		s := autoServer(t)
		s.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-11", "23:07"), FetchedAt: now, SumDepthMM: 0})
		body, err := s.deviceBody(now)
		if want := threeSlots + "\n18900:900,22500:900,62100:900"; err != nil || body != want {
			t.Errorf("body %q err %v, muốn %q", body, err, want)
		}
	})
	t.Run("carry 1800", func(t *testing.T) {
		s := autoServer(t)
		if err := s.Store.CloseDay("2026-11-11", 17.8, 1800, vt("2026-11-11", "20:09")); err != nil {
			t.Fatal(err)
		}
		body, err := s.deviceBody(now)
		if want := threeSlots + "\n62100:900"; err != nil || body != want {
			t.Errorf("body %q err %v, muốn %q", body, err, want)
		}
	})
}

func TestDeviceBodyAutoOnMixes(t *testing.T) {
	s := autoServer(t)
	now := vt("2026-11-12", "05:30")
	s.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "05:07"), FetchedAt: now, SumDepthMM: 6})
	p := DefaultParams()
	l, _, _ := s.Store.Ledger("2026-11-12")
	d, l2 := decide(l, p.Slots[0], 6, 1, p)
	if d.RunS != 360 {
		t.Fatalf("RunS %d", d.RunS)
	}
	if err := s.Store.SaveDecision("2026-11-12", d, l2, stateFinal, vt("2026-11-12", "05:09")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.AddManualSession("2026-11-12", 34560, 300, "thử"); err != nil {
		t.Fatal(err)
	}
	body, err := s.deviceBody(now)
	if want := threeSlots + "\n18900:360,22500:900,34560:300,62100:900"; err != nil || body != want {
		t.Errorf("body %q err %v, muốn %q", body, err, want)
	}
	// Lúc 19:30 (ngày Vrain chưa đóng): mọi lần đã chốt, dòng 2 vẫn đủ.
	for _, sl := range p.Slots[1:] {
		l, _, _ := s.Store.Ledger("2026-11-12")
		d, l2 := decide(l, sl, 6, 1, p)
		s.Store.SaveDecision("2026-11-12", d, l2, stateFinal, now)
	}
	body, err = s.deviceBody(vt("2026-11-12", "19:30"))
	if want := threeSlots + "\n18900:360,22500:900,34560:300,62100:900"; err != nil || body != want {
		t.Errorf("19:30 body %q err %v, muốn %q", body, err, want)
	}
}

func TestDeviceBodyAutoOnEmpty(t *testing.T) {
	// Mưa ≥ rain_hi, không có phiên manual ⇒ dòng 2 rỗng = hôm nay không tưới.
	s := autoServer(t)
	now := vt("2026-11-12", "00:05")
	s.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-11", "23:07"), FetchedAt: now, SumDepthMM: 12})
	body, err := s.deviceBody(now)
	if err != nil || body != threeSlots+"\n" {
		t.Errorf("body %q err %v", body, err)
	}
}

func TestDeviceBodyAutoOnSkipsEnabledLate(t *testing.T) {
	// Bật lúc 05:30 mà scheduler chưa kịp ghi "bỏ": dòng 2 không gửi bản tạm của 05:15.
	s := autoServer(t)
	autoParams(t, s.Store, vt("2026-11-12", "05:30"))
	body, err := s.deviceBody(vt("2026-11-12", "05:30"))
	if want := threeSlots + "\n22500:900,62100:900"; err != nil || body != want {
		t.Errorf("body %q err %v, muốn %q", body, err, want)
	}
}

func TestDecidedIncludesAllStates(t *testing.T) {
	st := openTestStore(t)
	p := DefaultParams()
	d := Decision{Slot: p.Slots[0], Reason: "bật tự động sau giờ chốt"}
	if err := st.SaveDecision("2026-11-12", d, Ledger{VDay: "2026-11-12"}, stateSkipped, vt("2026-11-12", "05:30")); err != nil {
		t.Fatal(err)
	}
	got, err := st.Decided("2026-11-12")
	if err != nil || len(got) != 1 || !got[18900] {
		t.Errorf("Decided = %v %v", got, err)
	}
	// Chốt hai lần cùng slot phải lỗi (PK blocks), sổ không bị trừ lần hai.
	d2, l2 := decide(Ledger{VDay: "2026-11-12"}, p.Slots[0], 6, 1, p)
	if err := st.SaveDecision("2026-11-12", d2, l2, stateFinal, vt("2026-11-12", "05:31")); err == nil {
		t.Error("chốt lại slot đã chốt mà không lỗi")
	}
	if l, _, _ := st.Ledger("2026-11-12"); l.UsedRainS != 0 {
		t.Errorf("sổ bị trừ: %+v", l)
	}
}

func TestSchedulerCatchUpBeforeClosingDay(t *testing.T) {
	// Lỡ chốt 17:15 (server chết 17:00–20:30): khởi động lúc 20:30 thì chốt bù 17:15 trước rồi mới
	// đóng ngày Vrain, để carry không tính lại phần mưa vừa dùng.
	st := openTestStore(t)
	p := autoParams(t, st, vt("2026-11-01", "00:00"))
	for _, hm := range []string{"16:07", "17:07", "19:07"} {
		sum := 6.0
		if hm == "16:07" {
			sum = 0
		}
		st.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-10", hm), FetchedAt: vt("2026-11-10", hm), SumDepthMM: sum})
	}
	for _, sl := range p.Slots[:2] {
		l, _, _ := st.Ledger("2026-11-10")
		d, l2 := decide(l, sl, 0, 1, p)
		if err := st.SaveDecision("2026-11-10", d, l2, stateFinal, vt("2026-11-10", "05:09")); err != nil {
			t.Fatal(err)
		}
	}
	clock := vt("2026-11-10", "20:30")
	sc := &Scheduler{Store: st, Rain: &fakeRain{now: &clock, fail: true}, Metar: &fakeMetar{now: &clock, obs: sunnyObs("2026-11-10")},
		Now: func() time.Time { return clock }}
	runTicks(t, sc, &clock, clock, clock)
	checkAuto(t, autoSessions(t, st, "2026-11-10"), []int{18900, 22500, 62100}, []int{900, 900, 360}, []string{"05:09", "05:09", "20:30"})
	if final, closed := dayRow(t, st, "2026-11-10"); final != 6 || !strings.HasPrefix(closed, "2026-11-10T20:30") {
		t.Errorf("final %v closed %q", final, closed)
	}
	if l, _, _ := st.Ledger("2026-11-11"); l.CarryInS != 0 {
		t.Errorf("carry %v, muốn 0 (mưa đã dùng cho 17:15)", l.CarryInS)
	}
}

func TestSchedulerRainAnomalyWarns(t *testing.T) {
	st := openTestStore(t)
	st.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-10", "15:07"), FetchedAt: vt("2026-11-10", "15:09"), SumDepthMM: 5})
	clock := vt("2026-11-10", "16:09")
	rain := &fakeRain{now: &clock, snaps: []fakeSnap{
		{vt("2026-11-10", "16:07"), RainSnapshot{DataTS: vt("2026-11-10", "16:07"), SumDepthMM: 3}},
		// Mất bản 20:07: sang ngày Vrain mới ở 21:07 sau khoảng trống ⇒ không coi là bất thường.
		{vt("2026-11-10", "21:07"), RainSnapshot{DataTS: vt("2026-11-10", "21:07"), SumDepthMM: 0}},
	}}
	sc := &Scheduler{Store: st, Rain: rain, Now: func() time.Time { return clock }}
	runTicks(t, sc, &clock, clock, clock)
	if n := warningsLike(t, st, "giảm"); n != 1 {
		t.Errorf("%d cảnh báo mưa giảm, muốn 1", n)
	}
	st.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-10", "19:07"), FetchedAt: vt("2026-11-10", "19:09"), SumDepthMM: 3})
	clock = vt("2026-11-10", "21:09")
	runTicks(t, sc, &clock, clock, clock)
	var n int
	st.db.QueryRow(`SELECT count(*) FROM warnings`).Scan(&n)
	if n != 1 {
		t.Errorf("có %d cảnh báo, muốn 1", n)
	}
}

func TestDeviceBodyAutoOnExcludesSkippedAndCancelled(t *testing.T) {
	s := autoServer(t)
	now := vt("2026-11-12", "06:30")
	s.Store.InsertRainSnapshot(RainSnapshot{DataTS: vt("2026-11-12", "06:07"), FetchedAt: now, SumDepthMM: 12})
	p := DefaultParams()
	l, _, _ := s.Store.Ledger("2026-11-12")
	d, l2 := decide(l, p.Slots[0], 12, 1, p)
	if d.RunS != 0 {
		t.Fatalf("RunS %d", d.RunS)
	}
	if err := s.Store.SaveDecision("2026-11-12", d, l2, stateSkipped, vt("2026-11-12", "05:09")); err != nil {
		t.Fatal(err)
	}
	// Phiên auto user huỷ (Task 9 sẽ có API): vẫn trong DB nhưng không gửi ESP.
	if _, err := s.Store.db.Exec(`INSERT INTO sessions(date, start_s, run_s, source, state, vday, slot_s, created_at, updated_at)
		VALUES('2026-11-12', 22500, 900, 'auto', 'cancelled', '2026-11-12', 22500, '', '')`); err != nil {
		t.Fatal(err)
	}
	body, err := s.deviceBody(now)
	if err != nil || body != threeSlots+"\n" {
		t.Errorf("body %q err %v, muốn dòng 2 rỗng", body, err)
	}
}
