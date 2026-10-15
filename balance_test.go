//go:build windows

package main

import "testing"

func TestAnalyseBalanceNeedsTwoCells(t *testing.T) {
	for _, cells := range [][]int{nil, {3350}} {
		if got := analyseBalance(cells); got != nil {
			t.Fatalf("analyseBalance(%v) = %+v, want nil", cells, got)
		}
	}
}

func TestAnalyseBalanceFindsWeakAndStrongCell(t *testing.T) {
	balance := analyseBalance([]int{3370, 3369, 3366, 3370})
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
	balance := analyseBalance([]int{3270, 3340, 3335, 3332})
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
	quiet := analyseBalance([]int{3300, 3340, 3335, 3332})
	if quiet.State != "balanced" {
		t.Errorf("state: got %q, want balanced (spread %d mV, tolerance %d)", quiet.State, quiet.SpreadMv, quiet.ToleranceMv)
	}
}

// The same difference at the top of the charge is a real one, because that is
// where the curve is steep and the BMS balances.
func TestAnalyseBalanceTopZone(t *testing.T) {
	lenient := analyseBalance([]int{3460, 3480, 3475, 3472})
	if lenient.Zone != "top" {
		t.Fatalf("zone: got %q, want top", lenient.Zone)
	}
	if lenient.State != "balanced" {
		t.Errorf("state: got %q, want balanced (spread %d mV)", lenient.State, lenient.SpreadMv)
	}
	if lenient.LowMv != lenient.CentreMv-balanceTopToleranceMv || lenient.HighMv != lenient.CentreMv+balanceTopToleranceMv {
		t.Errorf("window: got %d..%d around %d", lenient.LowMv, lenient.HighMv, lenient.CentreMv)
	}

	spread := analyseBalance([]int{3420, 3480, 3475, 3472})
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
	balance := analyseBalance([]int{3040, 3095, 3093, 3090})
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
	flat := analyseBalance([]int{3320, 3330, 3335, 3340})
	top := analyseBalance([]int{3440, 3450, 3455, 3460})
	if flat.ToleranceMv <= top.ToleranceMv {
		t.Errorf("flat tolerance %d must be wider than the top one %d", flat.ToleranceMv, top.ToleranceMv)
	}
	if flat.LowMv >= flat.HighMv || top.LowMv >= top.HighMv {
		t.Errorf("windows must be ordered: flat %d..%d, top %d..%d", flat.LowMv, flat.HighMv, top.LowMv, top.HighMv)
	}
}

// analyseBalance must not be confused by a single odd cell.
func TestAnalyseBalanceIgnoresSpreadOfOneReading(t *testing.T) {
	balanced := analyseBalance([]int{3369, 3369, 3369, 3369})
	if balanced.SpreadMv != 0 || balanced.State != "balanced" {
		t.Errorf("identical cells: got %+v, want a zero spread", balanced)
	}
}
