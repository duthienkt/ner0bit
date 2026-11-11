package main

import (
	"strings"
	"testing"
)

func TestPlannerGolden(t *testing.T) {
	p := DefaultParams()
	rows := []struct {
		name    string
		carryIn float64
		sums    [3]float64
		sunPM   float64
		final   float64
		runs    [3]int
		carry   float64
		check   bool // có assert carry hay không
	}{
		{"02/11", 0, [3]float64{0, 0, 1.0}, 1, 17.8, [3]int{900, 900, 900}, 1800, true},
		{"03/11", 1800, [3]float64{0, 0, 0}, 1, 0, [3]int{0, 0, 900}, 0, true},
		{"04/11", 0, [3]float64{0, 0, 0}, 0.69, 0, [3]int{900, 900, 0}, 0, false},
		{"10/11", 0, [3]float64{0, 0, 5.6}, 1, 5.6, [3]int{900, 900, 600}, 0, false},
		{"mưa đêm 4", 0, [3]float64{4, 6, 6}, 1, 6, [3]int{900, 360, 900}, 0, true},
		{"đêm 6 sáng 7", 0, [3]float64{6, 7, 7}, 1, 7, [3]int{360, 360, 900}, 0, false},
		{"đêm 7", 0, [3]float64{7, 7, 7}, 1, 7, [3]int{0, 720, 900}, 0, false},
		{"đêm 9", 0, [3]float64{9, 9, 9}, 1, 9, [3]int{0, 0, 540}, 0, false},
		{">= 10", 0, [3]float64{12, 12, 12}, 1, 12, [3]int{0, 0, 0}, 0, true},
	}
	for _, r := range rows {
		l := Ledger{VDay: "2026-11-01", CarryInS: r.carryIn}
		for i, s := range p.Slots {
			var d Decision
			d, l = decide(l, s, r.sums[i], r.sunPM, p)
			if d.RunS != r.runs[i] {
				t.Errorf("%s slot %d: RunS = %d, muốn %d (%s)", r.name, i, d.RunS, r.runs[i], d.Reason)
			}
		}
		got := carryNext(l, r.final, p)
		if r.check && got != r.carry {
			t.Errorf("%s: carryNext = %v, muốn %v", r.name, got, r.carry)
		}
	}
}

func TestDecideMinRun(t *testing.T) {
	p := DefaultParams()
	// mưa miễn 780 s ⇒ còn 120 s < MinRunS ⇒ 0
	l := Ledger{VDay: "d", UsedRainS: 0}
	sum := p.RainLoMM + 780/p.PerMMS()
	d, _ := decide(l, p.Slots[0], sum, 1, p)
	if d.RunS != 0 {
		t.Errorf("RunS = %d, muốn 0", d.RunS)
	}
}

func TestCarryNotForPM(t *testing.T) {
	p := DefaultParams()
	l := Ledger{VDay: "d", CarryInS: 1800}
	d, l2 := decide(l, Slot{62100, "pm"}, 0, 1, p)
	if d.RunS != 900 || l2.UsedCarryS != 0 {
		t.Errorf("RunS = %d, UsedCarryS = %v; muốn 900 và 0", d.RunS, l2.UsedCarryS)
	}
}

func TestProvisionalChainsLedger(t *testing.T) {
	p := DefaultParams()
	ds := provisional(Ledger{VDay: "d"}, p.Slots, 7, p)
	want := []int{0, 720, 900}
	if len(ds) != 3 {
		t.Fatalf("có %d quyết định, muốn 3", len(ds))
	}
	for i, d := range ds {
		if d.RunS != want[i] {
			t.Errorf("slot %d: RunS = %d, muốn %d", i, d.RunS, want[i])
		}
	}
}

func TestReasonMentionsNumbers(t *testing.T) {
	p := DefaultParams()
	d, _ := decide(Ledger{VDay: "d"}, p.Slots[2], 5.6, 1, p)
	if !strings.Contains(d.Reason, "5.6 mm") || !strings.Contains(d.Reason, "10 phút") {
		t.Errorf("Reason thiếu số: %q", d.Reason)
	}
	if strings.Contains(d.Reason, "\n") {
		t.Errorf("Reason phải một dòng: %q", d.Reason)
	}
}
