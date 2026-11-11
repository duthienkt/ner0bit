package main

import "testing"

func TestParamsDefaultValid(t *testing.T) {
	p := DefaultParams()
	if err := p.Validate(); err != nil {
		t.Fatalf("mặc định phải hợp lệ: %v", err)
	}
	if p.QuotaS() != 2700 {
		t.Errorf("QuotaS = %d, muốn 2700", p.QuotaS())
	}
	if p.PerMMS() != 540 {
		t.Errorf("PerMMS = %v, muốn 540", p.PerMMS())
	}
	if p.AutoEnabled {
		t.Error("auto_enabled mặc định phải tắt")
	}
}

func TestParamsValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		mod  func(p *Params)
	}{
		{"slot 04:14", func(p *Params) { p.Slots[0].StartS = 15240 }},
		{"slot 18:46", func(p *Params) { p.Slots[2].StartS = 67560 }},
		{"hai slot cách 1000s", func(p *Params) { p.Slots = []Slot{{18900, "am"}, {19900, "am"}} }},
		{"phase x", func(p *Params) { p.Slots[0].Phase = "x" }},
		{"rain hi == lo", func(p *Params) { p.RainHiMM = p.RainLoMM }},
		{"block 1801", func(p *Params) { p.BlockS = 1801 }},
		{"min_run > block", func(p *Params) { p.MinRunS = p.BlockS + 1 }},
		{"0 slot", func(p *Params) { p.Slots = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := DefaultParams()
			c.mod(&p)
			if err := p.Validate(); err == nil {
				t.Error("muốn lỗi, nhận nil")
			}
		})
	}
}
