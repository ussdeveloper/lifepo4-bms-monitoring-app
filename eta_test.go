//go:build windows

package main

import (
	"math"
	"testing"
	"time"
)

// etaSample builds a reading in the middle of a charge.
func etaSample(soc int, current, designAh, remainingAh float64) *Sample {
	soh := 100
	return &Sample{
		SoC:         soc,
		CurrentA:    current,
		DesignAh:    designAh,
		RemainingAh: remainingAh,
		SoH:         &soh,
	}
}

func TestEstimateTimeToFullByCounting(t *testing.T) {
	// 78 % of 314 Ah with 10 A still missing ≈ 6.9 h.
	sample := etaSample(78, 10, 314, 245)
	hours := estimateTimeToEdge(nil, sample) / 3600
	if math.Abs(hours-6.9) > 0.2 {
		t.Errorf("charged in %.2f h, want about 6.9 h", hours)
	}
}

func TestEstimateTimeToEmpty(t *testing.T) {
	// 245 Ah remaining at 40 A ≈ 6.1 h.
	sample := etaSample(70, -40, 314, 220)
	hours := estimateTimeToEdge(nil, sample) / 3600
	if math.Abs(hours-5.5) > 0.3 {
		t.Errorf("empty in %.2f h, want about 5.5 h", hours)
	}
}

func TestEstimateIgnoresAnIdleBattery(t *testing.T) {
	if got := estimateTimeToEdge(nil, etaSample(80, 0.05, 314, 250)); got != 0 {
		t.Errorf("an idle battery has no estimate, got %.0f", got)
	}
	if got := estimateTimeToEdge(nil, nil); got != 0 {
		t.Errorf("a missing reading has no estimate, got %.0f", got)
	}
}

func TestEstimatePrefersTheChargeTrendWhenTheCurrentTapers(t *testing.T) {
	now := time.Now()
	// The current drops from 10 A to 3 A (constant voltage phase) while the
	// charge creeps forward: counting at 3 A would claim 12 h, the trend of the
	// charge says about 2 h.
	points := make([]powerPoint, 0, 12)
	for index := 0; index < 12; index++ {
		points = append(points, powerPoint{
			At:    now.Add(-time.Duration(11-index) * time.Minute).UnixMilli(),
			Watts: 135 - float64(index),
			Soc:   86 + index/6,
		})
	}
	sample := etaSample(88, 3, 314, 270)
	sample.VoltageV = 13.5
	hours := estimateTimeToEdge(points, sample) / 3600
	if hours <= 0 || hours > 6 {
		t.Fatalf("a tapering charge must use the trend, got %.2f h", hours)
	}
}

func TestEstimateCountsWhileTheCurrentIsSteady(t *testing.T) {
	now := time.Now()
	points := make([]powerPoint, 0, 12)
	for index := 0; index < 12; index++ {
		points = append(points, powerPoint{
			At:    now.Add(-time.Duration(11-index) * time.Minute).UnixMilli(),
			Watts: 135,
			Soc:   86 + index/6,
		})
	}
	sample := etaSample(88, 10, 314, 270)
	sample.VoltageV = 13.5
	hours := estimateTimeToEdge(points, sample) / 3600
	// 12 % of 314 Ah at 10 A ≈ 3.8 h — the count wins over the noisy trend.
	if math.Abs(hours-3.77) > 0.3 {
		t.Errorf("counting must win while the current is steady, got %.2f h", hours)
	}
}

func TestTrendNeedsEnoughHistory(t *testing.T) {
	now := time.Now()
	short := []powerPoint{
		{At: now.Add(-2 * time.Minute).UnixMilli(), Soc: 70},
		{At: now.Add(-time.Minute).UnixMilli(), Soc: 71},
		{At: now.UnixMilli(), Soc: 72},
	}
	if got := trendEstimate(short, etaSample(72, 10, 314, 220), true); got != 0 {
		t.Errorf("two minutes are not enough to extrapolate, got %.0f", got)
	}

	flat := make([]powerPoint, 0, 6)
	for index := 0; index < 6; index++ {
		flat = append(flat, powerPoint{At: now.Add(-time.Duration(5-index) * time.Minute).UnixMilli(), Soc: 70})
	}
	if got := trendEstimate(flat, etaSample(70, 10, 314, 220), true); got != 0 {
		t.Errorf("a charge that does not move has no trend, got %.0f", got)
	}
}

func TestEstimateIsDroppedWhenItIsAbsurd(t *testing.T) {
	// Trickle charging at 20 mA would need years: report nothing instead of a
	// number that nobody can trust.
	sample := etaSample(50, 0.02, 314, 150)
	if got := estimateTimeToEdge(nil, sample); got != 0 {
		t.Errorf("an absurd estimate must be dropped, got %.0f s", got)
	}
}

func TestClampEstimateDropsUselessValues(t *testing.T) {
	if got := clampEstimate(10); got != 0 {
		t.Errorf("an estimate below the minimum is dropped: got %.0f", got)
	}
	if got := clampEstimate(72 * 3600); got != 0 {
		t.Errorf("an estimate beyond two days is dropped: got %.0f", got)
	}
	if got := clampEstimate(3600); got != 3600 {
		t.Errorf("a sane estimate stays: got %.0f", got)
	}
}

func TestPickEstimateKeepsTheTrendWhenItExists(t *testing.T) {
	if got := pickEstimate(7200, 3600); got != 7200 {
		t.Errorf("the trend is the better answer: got %.0f", got)
	}
	if got := pickEstimate(0, 3600); got != 3600 {
		t.Errorf("counting is the fallback: got %.0f", got)
	}
}
