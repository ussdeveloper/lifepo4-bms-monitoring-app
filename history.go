package main

import (
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
func (t *powerTrack) Add(address string, at time.Time, watts float64) {
	if t == nil || address == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	points := t.trimLocked(address, at)
	points = append(points, powerPoint{At: at.UnixMilli(), Watts: watts})
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
