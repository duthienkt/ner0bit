package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"time"
)

// Nguồn số liệu thật (spec §4.1, §4.2).
const (
	vrainURL         = "https://data.vrain.vn/public/current/56.json"
	vrainStationUUID = "726096c3-2ede-47f1-827e-0dc1ffe8d897" // Đỗ Xuân Hợp, phường Phước Long
	metarURL         = "https://aviationweather.gov/api/data/metar?ids=VVTS&format=json&hours=36"
)

const (
	collectMinute    = 9  // Vrain cập nhật phút :07 ⇒ thu thập từ :09
	vrainLastMinute  = 11 // thử lại Vrain tới :11 rồi thôi
	reasonLateEnable = "bật tự động sau giờ chốt"
	closeAfter       = 20*time.Hour + 12*time.Minute // không có bản 20:07 ⇒ đóng ngày lúc 20:12
)

// Scheduler là goroutine nền: thu thập mưa/nắng, đóng ngày Vrain, chốt các lần tưới tự động.
// Trạng thái trong bộ nhớ chỉ được goroutine chạy Run/Tick đụng tới.
type Scheduler struct {
	Store *Store
	Rain  RainSource
	Metar MetarSource
	Now   func() time.Time // nil ⇒ time.Now

	metarHour string            // giờ ("YYYY-MM-DD HH") đã lấy METAR thành công
	warnedAt  map[string]string // nguồn → giờ đã ghi cảnh báo lỗi thu thập (mỗi giờ một lần)
}

func (sc *Scheduler) now() time.Time {
	if sc.Now != nil {
		return sc.Now()
	}
	return time.Now()
}

// Run gọi Tick ngay rồi ở mỗi đầu phút cho tới khi ctx bị huỷ. Lỗi và panic chỉ được log.
func (sc *Scheduler) Run(ctx context.Context) {
	for {
		sc.safeTick(ctx)
		now := sc.now()
		t := time.NewTimer(now.Truncate(time.Minute).Add(time.Minute).Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (sc *Scheduler) safeTick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("CẢNH BÁO: lịch tự động panic: %v\n%s", r, debug.Stack())
		}
	}()
	if err := sc.Tick(ctx); err != nil {
		log.Printf("CẢNH BÁO: lịch tự động lỗi: %v", err)
	}
}

// Tick chạy một vòng, idempotent: thu thập → đóng các ngày Vrain trước hôm nay → chốt các lần
// đến hạn (khi bật tự động) → đóng ngày Vrain còn lại. Đóng ngày trước hôm nay trước khi chốt để
// carry đã có trong sổ; đóng ngày hôm nay sau khi chốt để lần chốt bù (restart sau 20:07) vẫn
// được tính vào carry.
func (sc *Scheduler) Tick(ctx context.Context) error {
	now := sc.now().In(vietnam)
	today := now.Format(time.DateOnly)
	p, err := sc.Store.Params()
	if err != nil {
		return err
	}
	errs := []error{sc.collect(ctx, now)}
	if err := sc.closeDays(now, p, today); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if p.AutoEnabled {
		errs = append(errs, sc.decideDue(ctx, now, p, today))
	}
	errs = append(errs, sc.closeDays(now, p, ""))
	return errors.Join(errs...)
}

// warnOnce log lỗi thu thập/Vrain trễ và ghi cảnh báo, mỗi nguồn tối đa một lần mỗi giờ (ghi
// lỗi thì lần sau trong giờ thử ghi lại).
func (sc *Scheduler) warnOnce(now time.Time, source, msg string) error {
	log.Printf("CẢNH BÁO: %s", msg)
	hour := now.Format("2006-01-02 15")
	if sc.warnedAt[source] == hour {
		return nil
	}
	if err := sc.Store.AddWarning(now, source, msg); err != nil {
		return err
	}
	if sc.warnedAt == nil {
		sc.warnedAt = map[string]string{}
	}
	sc.warnedAt[source] = hour
	return nil
}

// warn log và ghi cảnh báo.
func (sc *Scheduler) warn(now time.Time, source, msg string) error {
	log.Printf("CẢNH BÁO: %s", msg)
	return sc.Store.AddWarning(now, source, msg)
}

// freshRainHour là giờ của bản Vrain mới nhất có thể có lúc now: giờ hiện tại từ phút :09,
// giờ trước trong phút :00–:08 (Vrain ra bản lúc :07).
func freshRainHour(now time.Time) time.Time {
	want := now.Truncate(time.Hour)
	if now.Minute() < collectMinute {
		want = want.Add(-time.Hour)
	}
	return want
}

// haveFreshRain: đã có bản Vrain của giờ freshRainHour(now).
func (sc *Scheduler) haveFreshRain(now time.Time) (bool, error) {
	r, ok, err := sc.Store.rainUpTo(now)
	if err != nil || !ok {
		return false, err
	}
	return !r.DataTS.Before(freshRainHour(now)), nil
}

// collect lấy Vrain ở phút 9–11 khi còn thiếu snapshot giờ hiện tại (tới :11 vẫn thiếu ⇒ giữ
// snapshot cũ, cảnh báo "Vrain trễ"; ngoài các phút này decideDue tự lấy khi cần chốt), METAR mỗi
// giờ từ phút 9. Lỗi mạng/dữ liệu hỏng chỉ cảnh báo, không ghi gì; chỉ trả lỗi DB. Cảnh báo lỗi
// Vrain và Vrain trễ chung hạn mức một lần mỗi giờ.
func (sc *Scheduler) collect(ctx context.Context, now time.Time) error {
	m := now.Minute()
	var errs []error
	if sc.Rain != nil && m >= collectMinute && m <= vrainLastMinute {
		have, err := sc.haveFreshRain(now)
		if err != nil {
			errs = append(errs, err)
		} else if !have {
			errs = append(errs, sc.fetchRain(ctx, now))
			if m == vrainLastMinute {
				if have, err := sc.haveFreshRain(now); err != nil {
					errs = append(errs, err)
				} else if !have {
					errs = append(errs, sc.warnOnce(now, "vrain",
						fmt.Sprintf("Vrain trễ: chưa có bản %02d:07 lúc %s, giữ snapshot cũ", now.Hour(), now.Format("15:04"))))
				}
			}
		}
	}
	if hour := now.Format("2006-01-02 15"); sc.Metar != nil && m >= collectMinute && sc.metarHour != hour {
		obs, err := sc.Metar.Fetch(ctx)
		if err != nil {
			errs = append(errs, sc.warnOnce(now, "metar", "không lấy được METAR: "+err.Error()))
		} else if err := sc.Store.InsertMetar(obs); err != nil {
			errs = append(errs, err)
		} else {
			sc.metarHour = hour
		}
	}
	return errors.Join(errs...)
}

func (sc *Scheduler) fetchRain(ctx context.Context, now time.Time) error {
	r, err := sc.Rain.Fetch(ctx)
	if err != nil {
		return sc.warnOnce(now, "vrain", "không lấy được mưa Vrain: "+err.Error())
	}
	prev, hasPrev, err := sc.Store.rainBefore(r.DataTS)
	if err != nil {
		return err
	}
	inserted, err := sc.Store.InsertRainSnapshot(r)
	if err != nil || !inserted || !hasPrev {
		return err
	}
	// Sang ngày Vrain sau một khoảng trống (mất bản 20:07) không phải bất thường của Vrain.
	if VDay(prev.DataTS) != VDay(r.DataTS) && r.DataTS.Sub(prev.DataTS) > 90*time.Minute {
		return nil
	}
	if a := rainAnomaly(prev, r); a != "" {
		return sc.warn(now, "vrain", "Vrain bất thường: "+a)
	}
	return nil
}

// closeDays đóng các ngày Vrain còn mở (chỉ các ngày < before nếu before khác rỗng) khi đã có
// snapshot của ngày Vrain sau, hoặc đã qua 20:12 của ngày đó. Tổng cuối = snapshot cuối của
// ngày (không có ⇒ 0); carry ghi vào sổ ngày kế tiếp.
func (sc *Scheduler) closeDays(now time.Time, p Params, before string) error {
	open, err := sc.Store.OpenDays()
	if err != nil || len(open) == 0 {
		return err
	}
	newest, hasNewest, err := sc.Store.newestRainVDay()
	if err != nil {
		return err
	}
	for _, v := range open {
		if before != "" && v >= before {
			continue
		}
		d, err := time.ParseInLocation(time.DateOnly, v, vietnam)
		if err != nil {
			return fmt.Errorf("ngày Vrain %q không hợp lệ: %w", v, err)
		}
		if !(hasNewest && newest > v) && now.Before(d.Add(closeAfter)) {
			continue
		}
		final := 0.0
		if r, ok, err := sc.Store.LatestRain(v, now); err != nil {
			return err
		} else if ok {
			final = r.SumDepthMM
		}
		l, _, err := sc.Store.Ledger(v)
		if err != nil {
			return err
		}
		carry := carryNext(l, final, p)
		if err := sc.Store.CloseDay(v, final, carry, now); err != nil {
			return err
		}
		log.Printf("Lịch tự động: đóng ngày Vrain %s, mưa cuối %.1f mm, chuyển %d phút sang sáng hôm sau",
			v, final, minutes(carry))
	}
	return nil
}

// decideDue chốt các lần tưới của hôm nay đã tới giờ chốt mà chưa chốt (kể cả chốt bù khi
// server vừa khởi động). Phút :09–:10 mà chưa có snapshot Vrain của giờ này thì chờ tick sau.
// Ngoài phút :09–:11 (chốt bù, hoặc slot không rơi vào :15) mà chưa có thì lấy Vrain một lần
// trước khi chốt. Vẫn chưa có ⇒ chốt bằng snapshot cũ và cảnh báo "Vrain trễ".
func (sc *Scheduler) decideDue(ctx context.Context, now time.Time, p Params, today string) error {
	decided, err := sc.Store.Decided(today)
	if err != nil {
		return err
	}
	fetched := false
	for _, slot := range sortedSlots(p.Slots) {
		if decided[slot.StartS] {
			continue
		}
		at, err := decideAt(today, slot)
		if err != nil {
			return err
		}
		if now.Before(at) {
			break // các slot sau còn muộn hơn
		}
		if at.Before(p.AutoEnabledAt) {
			if err := sc.Store.SaveSkip(today, slot, reasonLateEnable, now); err != nil {
				return err
			}
			log.Printf("Lịch tự động: bỏ lần %s ngày %s (%s)", hms(slot.StartS), today, reasonLateEnable)
			continue
		}
		var notes []string
		have, err := sc.haveFreshRain(now)
		if err != nil {
			return err
		}
		if m := now.Minute(); !have && !fetched && sc.Rain != nil && (m < collectMinute || m > vrainLastMinute) {
			fetched = true // collect chỉ lấy Vrain ở phút :09–:11
			if err := sc.fetchRain(ctx, now); err != nil {
				return err
			}
			if have, err = sc.haveFreshRain(now); err != nil {
				return err
			}
		}
		if !have {
			if m := now.Minute(); m >= collectMinute && m < vrainLastMinute {
				return nil // chờ Vrain tới :11
			}
			msg := fmt.Sprintf("Vrain trễ: chốt lần %s ngày %s khi chưa có bản %02d:07, dùng snapshot cũ",
				hms(slot.StartS), today, freshRainHour(now).Hour())
			notes = append(notes, msg)
			if err := sc.warnOnce(now, "vrain", msg); err != nil {
				return err
			}
		}
		// Slot của ngày D luôn thuộc ngày Vrain D (slot trong 04:15–18:45).
		l, _, err := sc.Store.Ledger(today)
		if err != nil {
			return err
		}
		sum := 0.0
		if r, ok, err := sc.Store.LatestRain(today, now); err != nil {
			return err
		} else if ok {
			sum = r.SumDepthMM
		} else {
			notes = append(notes, "chưa có số liệu mưa của ngày ⇒ coi 0 mm")
		}
		sunS, et := 1.0, 0.0
		if slot.Phase == "pm" {
			day0 := at.Add(-time.Duration(slot.StartS-decideLeadS) * time.Second)
			obs, err := sc.Store.MetarBetween(day0, day0.Add(17*time.Hour))
			if err != nil {
				return err
			}
			var ok bool
			if sunS, et, ok = SunFactor(obs, today, p.ERefMM); !ok {
				msg := fmt.Sprintf("METAR thiếu (dưới 12 quan trắc 00–17h ngày %s) ⇒ coi S = 1", today)
				notes = append(notes, msg)
				if err := sc.warn(now, "metar", msg); err != nil {
					return err
				}
			}
		}
		d, l2 := decide(l, slot, sum, sunS, p)
		d.ETMM, d.Notes = et, notes
		state := stateFinal
		if d.RunS == 0 {
			state = stateSkipped
		}
		if err := sc.Store.SaveDecision(today, d, l2, state, now); err != nil {
			return err
		}
		log.Printf("Lịch tự động: chốt lần %s ngày %s: %s", hms(slot.StartS), today, d.Reason)
	}
	return nil
}
