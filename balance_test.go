//go:build windows

package main

import "testing"

// settledPack is a pack that has been in use for a while and holds ordinary
// sized cells: neither reason to widen the window applies, so the numbers are
// the plain ones from the zone.
var settledPack = packContext{DesignAh: 100, Cycles: 100}

func TestAnalyseBalanceNeedsTwoCells(t *testing.T) {
	for _, cells := range [][]int{nil, {3350}} {
		if got := analyseBalance(cells, settledPack); got != nil {
			t.Fatalf("analyseBalance(%v) = %+v, want nil", cells, got)
		}
	}
}

func TestAnalyseBalanceFindsWeakAndStrongCell(t *testing.T) {
	balance := analyseBalance([]int{3370, 3369, 3366, 3370}, settledPack)
	if balance == nil {
		t.Fatal("expected a balance for four cells")
	}
	if balance.Weak != 3 || balance.Strong != 1 {
		t.Errorf("weak/strong cells: got %d/%d, want 3/1", balance.Weak, balance.Strong)
	}
	if balance.SpreadMv != 4 {
		t.Errorf("spread: got %d, want 4", balance.SpreadMv)
	}
	if balance.CentreMv != 3368 {
		t.Errorf("centre: got %d, want 3368", balance.CentreMv)
	}
}

// A spread in the flat part of the curve is what the interface calls a phantom
// imbalance: the numbers differ, the capacity does not.
func TestAnalyseBalanceFlagsPhantomImbalance(t *testing.T) {
	balance := analyseBalance([]int{3270, 3340, 3335, 3332}, settledPack)
	if balance == nil {
		t.Fatal("expected a balance")
	}
	if balance.Zone != "flat" {
		t.Fatalf("zone: got %q, want flat", balance.Zone)
	}
	if balance.ToleranceMv != balanceFlatToleranceMv {
		t.Errorf("tolerance: got %d, want %d", balance.ToleranceMv, balanceFlatToleranceMv)
	}
	if balance.State != "phantom" {
		t.Errorf("state: got %q, want phantom (spread %d mV, tolerance %d)", balance.State, balance.SpreadMv, balance.ToleranceMv)
	}

	// A spread that stays inside the wide flat window stays quiet, even though
	// the same numbers at the top of the charge would be a fault.
	quiet := analyseBalance([]int{3300, 3340, 3335, 3332}, settledPack)
	if quiet.State != "balanced" {
		t.Errorf("state: got %q, want balanced (spread %d mV, tolerance %d)", quiet.State, quiet.SpreadMv, quiet.ToleranceMv)
	}
}

// The same difference at the top of the charge is a real one, because that is
// where the curve is steep and the BMS balances.
func TestAnalyseBalanceTopZone(t *testing.T) {
	lenient := analyseBalance([]int{3460, 3480, 3475, 3472}, settledPack)
	if lenient.Zone != "top" {
		t.Fatalf("zone: got %q, want top", lenient.Zone)
	}
	if lenient.State != "balanced" {
		t.Errorf("state: got %q, want balanced (spread %d mV)", lenient.State, lenient.SpreadMv)
	}
	if lenient.LowMv != lenient.CentreMv-balanceTopToleranceMv || lenient.HighMv != lenient.CentreMv+balanceTopToleranceMv {
		t.Errorf("window: got %d..%d around %d", lenient.LowMv, lenient.HighMv, lenient.CentreMv)
	}

	spread := analyseBalance([]int{3420, 3480, 3475, 3472}, settledPack)
	if spread.State != "imbalanced" {
		t.Errorf("state: got %q, want imbalanced (spread %d mV)", spread.State, spread.SpreadMv)
	}
	if spread.SpreadMv <= spread.ToleranceMv {
		t.Errorf("a reported imbalance must exceed the tolerance: %d <= %d", spread.SpreadMv, spread.ToleranceMv)
	}
}

func TestAnalyseBalanceBottomZone(t *testing.T) {
	// At the bottom of the discharge the curve is steep again, so a real
	// difference shows up here too — with a slightly wider window than at the
	// top, because the last percent of capacity is worth more millivolts.
	balance := analyseBalance([]int{3040, 3095, 3093, 3090}, settledPack)
	if balance.Zone != "bottom" {
		t.Fatalf("zone: got %q, want bottom", balance.Zone)
	}
	if balance.State != "imbalanced" {
		t.Errorf("state: got %q, want imbalanced (spread %d mV, tolerance %d)", balance.State, balance.SpreadMv, balance.ToleranceMv)
	}
	if balance.Weak != 1 {
		t.Errorf("weak cell: got %d, want 1", balance.Weak)
	}
}

// The window has to be tight enough at the ends to catch a drifting cell and
// wide enough in the middle not to cry wolf.
func TestAnalyseBalanceWindowFollowsZone(t *testing.T) {
	flat := analyseBalance([]int{3320, 3330, 3335, 3340}, settledPack)
	top := analyseBalance([]int{3440, 3450, 3455, 3460}, settledPack)
	if flat.ToleranceMv <= top.ToleranceMv {
		t.Errorf("flat tolerance %d must be wider than the top one %d", flat.ToleranceMv, top.ToleranceMv)
	}
	if flat.LowMv >= flat.HighMv || top.LowMv >= top.HighMv {
		t.Errorf("windows must be ordered: flat %d..%d, top %d..%d", flat.LowMv, flat.HighMv, top.LowMv, top.HighMv)
	}
}

// analyseBalance must not be confused by a single odd cell.
func TestAnalyseBalanceIgnoresSpreadOfOneReading(t *testing.T) {
	balanced := analyseBalance([]int{3369, 3369, 3369, 3369}, settledPack)
	if balanced.SpreadMv != 0 || balanced.State != "balanced" {
		t.Errorf("identical cells: got %+v, want a zero spread", balanced)
	}
}

// A brand new pack and a large cell both need a wider window: a 314 Ah cell
// takes hours to move with a balancer that moves tens of milliamps, and a pack
// that has done a handful of cycles is still forming.
func TestBalanceWindowFollowsThePack(t *testing.T) {
	cases := []struct {
		name       string
		pack       packContext
		wantAdjust int
		newPack    bool
		large      bool
	}{
		{"settled pack of ordinary cells", settledPack, 0, false, false},
		{"brand new pack", packContext{DesignAh: 100, Cycles: 0}, 15, true, false},
		{"still forming", packContext{DesignAh: 100, Cycles: 20}, 8, true, false},
		{"past the forming phase", packContext{DesignAh: 100, Cycles: 21}, 0, false, false},
		{"large cells", packContext{DesignAh: 314, Cycles: 100}, 8, false, true},
		{"medium cells", packContext{DesignAh: 200, Cycles: 100}, 4, false, true},
		{"new pack of large cells", packContext{DesignAh: 314, Cycles: 0}, 23, true, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			adjustment, newPack, large := balanceWindowAdjustment(test.pack)
			if adjustment != test.wantAdjust || newPack != test.newPack || large != test.large {
				t.Fatalf("got adjustment %d (new %v, large %v), want %d (%v, %v)",
					adjustment, newPack, large, test.wantAdjust, test.newPack, test.large)
			}
		})
	}

	// The window of the real battery in this project: 314 Ah, a handful of
	// cycles, at the top of the charge.
	balance := analyseBalance([]int{3484, 3383, 3391, 3411}, packContext{DesignAh: 314, Cycles: 0})
	if balance.BaseMv != balanceTopToleranceMv {
		t.Errorf("base: got %d, want %d", balance.BaseMv, balanceTopToleranceMv)
	}
	if balance.ToleranceMv != balanceTopToleranceMv+23 {
		t.Errorf("tolerance: got %d, want %d", balance.ToleranceMv, balanceTopToleranceMv+23)
	}
	if balance.LowMv != balance.CentreMv-balance.ToleranceMv || balance.HighMv != balance.CentreMv+balance.ToleranceMv {
		t.Errorf("window: got %d..%d around %d", balance.LowMv, balance.HighMv, balance.CentreMv)
	}
	// A 100 mV spread is still a real imbalance even with the wider window.
	if balance.State != "imbalanced" {
		t.Errorf("state: got %q, want imbalanced", balance.State)
	}
}
