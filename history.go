package main

import (
	"math"
	"sync"
	"time"
)

// powerWindow is how much history the charging power graph in the panel shows.
const powerWindow = 10 * time.Minute

// powerKeep is how long an address may stay silent before it is dropped from
// memory, so hovering many batteries does not grow the map forever.
const powerKeep = 20 * time.Minute

// powerPoint is one reading of the charging power graph. Time is sent as unix
// milliseconds so the interface can place it on the axis without parsing.
type powerPoint struct {
	At    int64   `json:"t"`
	Watts float64 `json:"w"`
	// Soc is the state of charge of that reading; it lets the time to full be
	// estimated from how the charge actually moves.
	Soc int `json:"s,omitempty"`
}

// estimateTimeToEdge tells how long the battery needs to be full (while
// charging) or empty (while discharging), in seconds. Zero means "cannot tell".
//
// Two estimates are combined. Counting the charge that still has to move at the
// current is exact while the current is constant (the constant current phase of
// a charge). Near the end of a charge the current tapers instead, so counting
// becomes far too optimistic — there the trend of the state of charge over the
// last minutes is the better answer.
func estimateTimeToEdge(points []powerPoint, sample *Sample) float64 {
	if sample == nil {
		return 0
	}
	const idleCurrent = 0.3
	charging := sample.CurrentA > idleCurrent
	discharging := sample.CurrentA < -idleCurrent
	if !charging && !discharging {
		return 0
	}

	coulomb := clampEstimate(coulombEstimate(sample, charging))
	trend := clampEstimate(trendEstimate(points, sample, charging))
	if taperingCurrent(points, sample) && trend > 0 {
		return trend
	}
	if coulomb > 0 {
		return coulomb
	}
	return trend
}

// taperingCurrent reports whether the current already dropped noticeably inside
// the window, which is what happens when a charge moves from constant current to
// constant voltage.
func taperingCurrent(points []powerPoint, sample *Sample) bool {
	if len(points) < 3 || sample.VoltageV <= 0 {
		return false
	}
	peak := 0.0
	for _, point := range points {
		if amps := math.Abs(point.Watts) / sample.VoltageV; amps > peak {
			peak = amps
		}
	}
	if peak <= 0 {
		return false
	}
	return math.Abs(sample.CurrentA) < peak*0.8
}

// clampEstimate drops estimates that are too small or too large to be useful.
func clampEstimate(seconds float64) float64 {
	const (
		minEta = 30 * time.Second
		maxEta = 48 * time.Hour
	)
	if seconds < minEta.Seconds() || seconds > maxEta.Seconds() {
		return 0
	}
	return seconds
}

// coulombEstimate counts the capacity that still has to move at the current.
func coulombEstimate(sample *Sample, charging bool) float64 {
	current := math.Abs(sample.CurrentA)
	if current < 0.01 {
		return 0
	}
	capacity := sample.DesignAh
	if capacity <= 0 {
		capacity = sample.RemainingAh
	}
	if capacity <= 0 {
		return 0
	}
	missingAh := (100 - float64(sample.SoC)) / 100 * capacity
	if !charging {
		missingAh = sample.RemainingAh
		if missingAh <= 0 {
			missingAh = float64(sample.SoC) / 100 * capacity
		}
	}
	if missingAh <= 0 {
		return 0
	}
	return missingAh / current * 3600
}

// trendEstimate extrapolates the state of charge over the readings of the last
// minutes. It needs a long enough window and a charge that really moves.
func trendEstimate(points []powerPoint, sample *Sample, charging bool) float64 {
	const minSpan = 3 * time.Minute
	if len(points) < 3 {
		return 0
	}
	first, last := points[0], points[len(points)-1]
	if first.Soc == 0 && last.Soc == 0 {
		return 0
	}
	span := time.Duration(last.At-first.At) * time.Millisecond
	if span < minSpan {
		return 0
	}
	deltaSoc := float64(last.Soc - first.Soc)
	if charging && deltaSoc <= 0 {
		return 0
	}
	if !charging && deltaSoc >= 0 {
		return 0
	}
	ratePerSecond := deltaSoc / span.Seconds()
	if ratePerSecond == 0 {
		return 0
	}
	missing := 100 - float64(sample.SoC)
	if !charging {
		missing = float64(sample.SoC)
	}
	if missing <= 0 {
		return 0
	}
	return missing / math.Abs(ratePerSecond)
}

// pickEstimate is kept for the sanity limits only; the estimate itself is chosen
// in estimateTimeToEdge.
func pickEstimate(trend, coulomb float64) float64 {
	if trend > 0 {
		return clampEstimate(trend)
	}
	return clampEstimate(coulomb)
}

// samplePower turns a reading into power: positive while charging, negative
// while discharging.
func samplePower(sample *Sample) float64 {
	if sample == nil {
		return 0
	}
	return sample.VoltageV * sample.CurrentA
}

// powerTrack keeps the last minutes of readings per battery address, which is
// what the panel needs to draw the charging power graph.
type powerTrack struct {
	mu      sync.Mutex
	series  map[string][]powerPoint
	updated map[string]time.Time
}

func newPowerTrack() *powerTrack {
	return &powerTrack{
		series:  make(map[string][]powerPoint),
		updated: make(map[string]time.Time),
	}
}

// Add records one reading and forgets everything outside the window.
func (t *powerTrack) Add(address string, at time.Time, watts float64, soc int) {
	if t == nil || address == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	points := t.trimLocked(address, at)
	points = append(points, powerPoint{At: at.UnixMilli(), Watts: watts, Soc: soc})
	t.series[address] = points
	t.updated[address] = at
	t.evictLocked(at)
}

// Seed fills the series of an address from stored history. It never replaces
// readings that were collected in this run.
func (t *powerTrack) Seed(address string, points []powerPoint, at time.Time) {
	if t == nil || address == "" || len(points) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.series[address]) > 0 {
		return
	}
	cutoff := at.Add(-powerWindow).UnixMilli()
	fresh := make([]powerPoint, 0, len(points))
	for _, point := range points {
		if point.At >= cutoff {
			fresh = append(fresh, point)
		}
	}
	if len(fresh) == 0 {
		return
	}
	t.series[address] = fresh
	t.updated[address] = at
}

// Points returns a copy of the window of readings for an address.
func (t *powerTrack) Points(address string, now time.Time) []powerPoint {
	if t == nil || address == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	points := t.trimLocked(address, now)
	if len(points) == 0 {
		return nil
	}
	out := make([]powerPoint, len(points))
	copy(out, points)
	return out
}

// trimLocked drops readings older than the window. The caller holds the lock.
func (t *powerTrack) trimLocked(address string, now time.Time) []powerPoint {
	points := t.series[address]
	cutoff := now.Add(-powerWindow).UnixMilli()
	start := 0
	for start < len(points) && points[start].At < cutoff {
		start++
	}
	if start == 0 {
		return points
	}
	trimmed := make([]powerPoint, len(points)-start)
	copy(trimmed, points[start:])
	t.series[address] = trimmed
	return trimmed
}

// evictLocked forgets addresses that stopped reporting a while ago.
func (t *powerTrack) evictLocked(now time.Time) {
	if len(t.series) <= 8 {
		return
	}
	for address, seen := range t.updated {
		if now.Sub(seen) < powerKeep {
			continue
		}
		delete(t.series, address)
		delete(t.updated, address)
	}
}
