package main

import (
	"fmt"
	"math"
	"strings"
)

// Ledger là sổ phút miễn của một ngày Vrain (spec §4.3).
type Ledger struct {
	VDay       string
	CarryInS   float64 // phút miễn dư từ ngày trước, chỉ dùng cho các lần buổi sáng
	UsedCarryS float64
	UsedRainS  float64
	UsedSunS   float64
}

// Decision là kết quả chốt một lần tưới. Các trường số giữ đầu vào/phân rã để UI giải thích.
type Decision struct {
	Slot        Slot
	RunS        int
	SumMM       float64
	RainCreditS float64 // M(t)
	AvailCarryS float64
	AvailRainS  float64
	AvailSunS   float64
	SunS        float64
	ETMM        float64  // bốc hơi ngày (mm) của lần buổi chiều; 0 nếu không tính
	Notes       []string // cảnh báo lúc chốt (Vrain trễ, METAR thiếu…); scheduler điền
	Reason      string
}

// rainCreditS là số giây được miễn do mưa tích luỹ sumMM: clamp((sum − lo)·per_mm, 0, quota).
func rainCreditS(sumMM float64, p Params) float64 {
	m := (sumMM - p.RainLoMM) * p.PerMMS()
	return math.Max(0, math.Min(m, float64(p.QuotaS())))
}

// decide chốt một lần tưới: dùng carry trước, rồi mưa, rồi nắng; trả quyết định và sổ mới.
// sunS chỉ có tác dụng với lần buổi chiều; carry chỉ có tác dụng với lần buổi sáng.
func decide(l Ledger, slot Slot, sumMM, sunS float64, p Params) (Decision, Ledger) {
	m := rainCreditS(sumMM, p)
	d := Decision{Slot: slot, SumMM: sumMM, RainCreditS: m, SunS: sunS}
	if slot.Phase == "am" {
		d.AvailCarryS = math.Max(0, l.CarryInS-l.UsedCarryS)
	}
	d.AvailRainS = math.Max(0, m-l.UsedRainS)
	if slot.Phase == "pm" {
		d.AvailSunS = math.Max(0, (1-sunS)*float64(p.QuotaS())-l.UsedSunS)
	}

	// Dùng lần lượt carry → mưa → nắng cho tới khi đủ block.
	left := float64(p.BlockS)
	take := func(avail float64) float64 {
		u := math.Min(left, avail)
		left -= u
		return u
	}
	uCarry := take(d.AvailCarryS)
	uRain := take(d.AvailRainS)
	uSun := take(d.AvailSunS)
	use := uCarry + uRain + uSun
	l.UsedCarryS += uCarry
	l.UsedRainS += uRain
	l.UsedSunS += uSun

	run := int(math.Round((float64(p.BlockS)-use)/60)) * 60
	if run < p.MinRunS {
		run = 0
	}
	d.RunS = run
	d.Reason = buildReason(d, uCarry, uRain, uSun, use, p)
	return d, l
}

func minutes(s float64) int { return int(math.Round(s / 60)) }

// buildReason viết một dòng tiếng Việt giải thích lần tưới này.
func buildReason(d Decision, uCarry, uRain, uSun, use float64, p Params) string {
	segs := []string{}
	if uCarry > 0 {
		segs = append(segs, fmt.Sprintf("miễn %d phút do mưa hôm trước", minutes(uCarry)))
	}
	segs = append(segs, fmt.Sprintf("mưa %.1f mm từ 19h ⇒ miễn %d phút", d.SumMM, minutes(uRain)))
	if uSun > 0 {
		segs = append(segs, fmt.Sprintf("nắng S=%.2f ⇒ miễn %d phút", d.SunS, minutes(uSun)))
	}
	rest := p.BlockS - minutes(use)*60
	switch {
	case d.RunS > 0:
		segs = append(segs, fmt.Sprintf("tưới %d phút", d.RunS/60))
	case rest > 0:
		segs = append(segs, fmt.Sprintf("còn %d phút < %d ⇒ bỏ", rest/60, p.MinRunS/60))
	default:
		segs = append(segs, "bỏ")
	}
	return strings.Join(segs, "; ")
}

// provisional tính bản tạm cho các lần chưa chốt: sổ đi tiếp qua từng lần, giả định S = 1.
func provisional(l Ledger, slots []Slot, sumMM float64, p Params) []Decision {
	out := make([]Decision, 0, len(slots))
	for _, s := range slots {
		var d Decision
		d, l = decide(l, s, sumMM, 1, p)
		out = append(out, d)
	}
	return out
}

// carryNext là phút miễn dư chuyển sang sáng hôm sau: min(carry_max, max(0, M(final) − used_rain)).
func carryNext(l Ledger, finalSumMM float64, p Params) float64 {
	return math.Min(float64(p.CarryMaxS), math.Max(0, rainCreditS(finalSumMM, p)-l.UsedRainS))
}
