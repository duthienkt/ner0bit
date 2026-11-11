package main

import (
	"fmt"
	"sort"
	"time"
)

const (
	slotMinS      = 4*3600 + 15*60  // 04:15
	slotMaxS      = 18*3600 + 45*60 // 18:45
	blockMaxS     = 1800            // mức cắt cứng của firmware
	slotGapExtraS = 180             // khoảng cách tối thiểu giữa hai lần = block + 3 phút
)

// Slot là một lần tưới trong ngày: giờ bắt đầu (giây từ 00:00 giờ VN) và buổi.
type Slot struct {
	StartS int    `json:"start_s"`
	Phase  string `json:"phase"` // "am" hoặc "pm"
}

// Params là tham số của chế độ tưới tự động (spec §4.3).
type Params struct {
	AutoEnabled   bool      `json:"auto_enabled"`
	AutoEnabledAt time.Time `json:"auto_enabled_at"`
	Slots         []Slot    `json:"slots"`
	BlockS        int       `json:"block_s"`
	MinRunS       int       `json:"min_run_s"`
	CarryMaxS     int       `json:"carry_max_s"`
	RainLoMM      float64   `json:"rain_lo_mm"`
	RainHiMM      float64   `json:"rain_hi_mm"`
	ERefMM        float64   `json:"e_ref_mm"`
}

// DefaultParams trả tham số mặc định; chế độ tự động tắt.
func DefaultParams() Params {
	return Params{
		Slots:     []Slot{{18900, "am"}, {22500, "am"}, {62100, "pm"}},
		BlockS:    900,
		MinRunS:   180,
		CarryMaxS: 1800,
		RainLoMM:  5,
		RainHiMM:  10,
		ERefMM:    4.0,
	}
}

// QuotaS là quota ngày = số lần × block (giây).
func (p Params) QuotaS() int { return len(p.Slots) * p.BlockS }

// PerMMS là số giây tưới được miễn cho mỗi mm mưa vượt rain_lo.
func (p Params) PerMMS() float64 {
	return float64(p.QuotaS()) / (p.RainHiMM - p.RainLoMM)
}

// Validate kiểm tra miền giá trị.
func (p Params) Validate() error {
	if len(p.Slots) == 0 {
		return fmt.Errorf("cần ít nhất 1 lần tưới")
	}
	if len(p.Slots) > maxEntriesPerLine {
		return fmt.Errorf("tối đa %d lần tưới", maxEntriesPerLine)
	}
	if p.BlockS <= 0 || p.BlockS > blockMaxS {
		return fmt.Errorf("block phải trong 1..%d giây", blockMaxS)
	}
	if p.MinRunS < 0 || p.MinRunS > p.BlockS {
		return fmt.Errorf("min_run phải trong 0..block (%d giây)", p.BlockS)
	}
	if p.CarryMaxS < 0 {
		return fmt.Errorf("carry_max không được âm")
	}
	if p.RainLoMM < 0 || p.RainHiMM <= p.RainLoMM {
		return fmt.Errorf("cần 0 ≤ rain_lo < rain_hi")
	}
	if p.ERefMM <= 0 {
		return fmt.Errorf("e_ref phải > 0")
	}
	starts := make([]int, 0, len(p.Slots))
	for _, s := range p.Slots {
		if s.Phase != "am" && s.Phase != "pm" {
			return fmt.Errorf("buổi %q không hợp lệ (am hoặc pm)", s.Phase)
		}
		if s.StartS < slotMinS || s.StartS > slotMaxS {
			return fmt.Errorf("giờ tưới %d giây phải trong %d..%d (04:15..18:45)", s.StartS, slotMinS, slotMaxS)
		}
		starts = append(starts, s.StartS)
	}
	sort.Ints(starts)
	for i := 1; i < len(starts); i++ {
		if starts[i]-starts[i-1] < p.BlockS+slotGapExtraS {
			return fmt.Errorf("hai lần tưới %d và %d giây cách nhau < block + %d giây", starts[i-1], starts[i], slotGapExtraS)
		}
	}
	return nil
}
