package main

// Balance describes how far apart the cells of a pack are and — just as
// important — what that distance means at the current state of charge.
//
// The analysis lives below, next to the constants it uses.
const (
	// balanceTopMv is where the curve steepens while charging and cell
	// voltages start saying something about capacity.
	balanceTopMv = 3400
	// balanceBottomMv is the same while discharging.
	balanceBottomMv = 3100

	balanceTopToleranceMv    = 30
	balanceBottomToleranceMv = 40
	balanceFlatToleranceMv   = 60
)

// packContext describes the battery the cells belong to. Both numbers widen the
// window instead of tightening it:
//
//   - a balancer moves a fixed trickle of current (tens of milliamps), so the
//     bigger the cell, the longer it takes to pull a high cell down — a 314 Ah
//     cell needs hours where a 100 Ah cell needs minutes;
//   - a new pack is still forming and settles over the first cycles, so a
//     spread there is expected to shrink on its own.
type packContext struct {
	DesignAh float64
	Cycles   int
}

// balanceWindowAdjustment is how much the window grows for this pack, in
// millivolts, together with the reasons, so the interface can explain it.
func balanceWindowAdjustment(pack packContext) (adjustment int, newPack bool, largeCells bool) {
	switch {
	case pack.Cycles <= 5:
		adjustment += 15
		newPack = true
	case pack.Cycles <= 20:
		adjustment += 8
		newPack = true
	}
	switch {
	case pack.DesignAh >= 280:
		adjustment += 8
		largeCells = true
	case pack.DesignAh >= 150:
		adjustment += 4
		largeCells = true
	}
	return adjustment, newPack, largeCells
}

// Balance describes how far apart the cells of a pack are and — just as
// important — what that distance means at the current state of charge.
//
// LiFePO4 cells are almost flat between roughly 3.10 V and 3.40 V: moving a
// cell by tens of percent of its capacity changes its voltage by only tens of
// millivolts there. A spread seen in that part of the curve is therefore mostly
// a difference in readings, not in capacity — a "phantom" imbalance that
// disappears on its own. A real imbalance shows at the ends of the curve:
// above 3.40 V while charging (where the BMS balances) and below 3.10 V while
// discharging. The tolerance follows the same idea: it is generous in the flat
// part and tight at the ends, and it grows for large cells and for a pack that
// is still new.
type Balance struct {
	SpreadMv    int    `json:"spreadMv"`
	Zone        string `json:"zone"`
	State       string `json:"state"`
	ToleranceMv int    `json:"toleranceMv"`
	BaseMv      int    `json:"baseMv"`
	AdjustMv    int    `json:"adjustMv"`
	NewPack     bool   `json:"newPack,omitempty"`
	LargeCells  bool   `json:"largeCells,omitempty"`
	CentreMv    int    `json:"centreMv"`
	// LowMv and HighMv are the window every cell should stay inside; it is
	// drawn under the cell bars.
	LowMv  int `json:"lowMv"`
	HighMv int `json:"highMv"`
	// Weak and Strong are 1-based cell numbers (the lowest and the highest
	// cell), so the interface can name them.
	Weak   int `json:"weak"`
	Strong int `json:"strong"`
}

// analyseBalance compares the cells of one reading. It returns nil when there
// is nothing to compare.
func analyseBalance(cellsMv []int, pack packContext) *Balance {
	if len(cellsMv) < 2 {
		return nil
	}
	minimum := cellsMv[0]
	maximum := cellsMv[0]
	weak := 1
	strong := 1
	total := 0
	for index, millivolts := range cellsMv {
		total += millivolts
		if millivolts < minimum {
			minimum = millivolts
			weak = index + 1
		}
		if millivolts > maximum {
			maximum = millivolts
			strong = index + 1
		}
	}
	centre := total / len(cellsMv)
	spread := maximum - minimum

	base := balanceFlatToleranceMv
	zone := "flat"
	switch {
	case centre >= balanceTopMv:
		zone = "top"
		base = balanceTopToleranceMv
	case centre <= balanceBottomMv:
		zone = "bottom"
		base = balanceBottomToleranceMv
	}
	adjustment, newPack, largeCells := balanceWindowAdjustment(pack)
	tolerance := base + adjustment

	state := "balanced"
	if spread > tolerance {
		state = "imbalanced"
		if zone == "flat" {
			state = "phantom"
		}
	}
	return &Balance{
		SpreadMv:    spread,
		Zone:        zone,
		State:       state,
		ToleranceMv: tolerance,
		BaseMv:      base,
		AdjustMv:    adjustment,
		NewPack:     newPack,
		LargeCells:  largeCells,
		CentreMv:    centre,
		LowMv:       centre - tolerance,
		HighMv:      centre + tolerance,
		Weak:        weak,
		Strong:      strong,
	}
}
