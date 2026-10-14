//go:build windows

package main

import (
	"testing"
	"time"
)

func TestSamplePowerSign(t *testing.T) {
	charging := samplePower(&Sample{VoltageV: 13.45, CurrentA: 10})
	if charging < 134 || charging > 135 {
		t.Errorf("charging power: got %.2f, want about 134.5", charging)
	}
	discharging := samplePower(&Sample{VoltageV: 13.2, CurrentA: -4.5})
	if discharging > -59 || discharging < -60 {
		t.Errorf("discharging power: got %.2f, want about -59.4", discharging)
	}
	if got := samplePower(nil); got != 0 {
		t.Errorf("nil sample: got %.2f, want 0", got)
	}
}

func TestPowerTrackKeepsTheWindowOnly(t *testing.T) {
	track := newPowerTrack()
	now := time.Now()
	track.Add("AA", now.Add(-15*time.Minute), 100, 60)
	track.Add("AA", now.Add(-5*time.Minute), 120, 70)
	track.Add("AA", now, 140, 80)

	points := track.Points("AA", now)
	if len(points) != 2 {
		t.Fatalf("readings inside the window: got %d, want 2", len(points))
	}
	if points[0].Watts != 120 || points[1].Watts != 140 {
		t.Errorf("unexpected readings: %+v", points)
	}
	if points[0].At >= points[1].At {
		t.Error("readings must stay in time order")
	}
	if got := track.Points("BB", now); got != nil {
		t.Errorf("an address without readings must return nothing, got %+v", got)
	}
}

func TestPowerTrackSeedFillsOnlyEmptySeries(t *testing.T) {
	now := time.Now()
	seed := []powerPoint{
		{At: now.Add(-2 * time.Minute).UnixMilli(), Watts: 80},
		{At: now.Add(-time.Minute).UnixMilli(), Watts: 85},
	}

	collected := newPowerTrack()
	collected.Add("AA", now, 90, 50)
	collected.Seed("AA", seed, now)
	if points := collected.Points("AA", now); len(points) != 1 || points[0].Watts != 90 {
		t.Fatalf("seed must not replace readings of this run: %+v", points)
	}

	empty := newPowerTrack()
	empty.Seed("AA", seed, now)
	if points := empty.Points("AA", now); len(points) != 2 {
		t.Fatalf("seed must fill an empty series, got %+v", points)
	}
}

func TestSessionStateCarriesPowerSeries(t *testing.T) {
	session := &Session{power: newPowerTrack(), status: sessionConnected, address: "AA:BB:CC:DD:EE:FF"}
	session.power.Add(session.address, time.Now(), 120, 77)

	state := session.State()
	if len(state.Power) != 1 || state.Power[0].Watts != 120 {
		t.Fatalf("the session snapshot must carry the power series, got %+v", state.Power)
	}
}

func TestStoreRecentPowerReadsHistoryFile(t *testing.T) {
	store := NewStore(t.TempDir())
	if !store.Enabled() {
		t.Fatal("store must be enabled for a writable directory")
	}
	device := testBattery()
	now := time.Now()
	for index := range 6 {
		sample := testSample()
		sample.Timestamp = now.Add(-time.Duration(11-2*index) * time.Minute)
		sample.VoltageV = 13
		sample.CurrentA = float64(index)
		if err := store.AppendSample(device.Address, device, sample); err != nil {
			t.Fatalf("append sample %d: %v", index, err)
		}
	}

	points := store.RecentPower(device.Address, now.Add(-10*time.Minute))
	if len(points) != 5 {
		t.Fatalf("readings of the last ten minutes: got %d, want 5 (%+v)", len(points), points)
	}
	if points[0].Watts != 13 || points[len(points)-1].Watts != 65 {
		t.Errorf("power must be voltage times current, got %+v", points)
	}
	if got := store.RecentPower("00:00:00:00:00:00", now.Add(-10*time.Minute)); got != nil {
		t.Errorf("unknown address must return nothing, got %+v", got)
	}
}
