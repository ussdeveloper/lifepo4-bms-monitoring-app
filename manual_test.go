//go:build windows

package main

import (
	"strings"
	"testing"

	"tinygo.org/x/bluetooth"
)

func TestParseDeviceAddressAcceptsCommonSpellings(t *testing.T) {
	cases := map[string]string{
		"AA:BB:CC:11:22:33":   "AA:BB:CC:11:22:33",
		"aa:bb:cc:11:22:33":   "AA:BB:CC:11:22:33",
		"AA-BB-CC-11-22-33":   "AA:BB:CC:11:22:33",
		"aabbcc112233":        "AA:BB:CC:11:22:33",
		"  AA:BB:CC:11:22:33": "AA:BB:CC:11:22:33",
	}
	for input, want := range cases {
		_, normalised, err := parseDeviceAddress(input)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", input, err)
			continue
		}
		if normalised != want {
			t.Errorf("%q: got %q, want %q", input, normalised, want)
		}
	}
}

func TestParseDeviceAddressRejectsGarbage(t *testing.T) {
	for _, input := range []string{"", "not-an-address", "AA:BB:CC:11:22", "AA:BB:CC:11:22:GG", "1234"} {
		if _, _, err := parseDeviceAddress(input); err == nil {
			t.Errorf("%q must be rejected", input)
		}
	}
}

func TestAddManualListsAndProbesTheAddress(t *testing.T) {
	hub := NewHub(bluetooth.DefaultAdapter, NewStore(t.TempDir()))
	normalised, err := hub.AddManual("aabbcc112233", "My battery")
	if err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}
	if normalised != "AA:BB:CC:11:22:33" {
		t.Fatalf("address: got %q", normalised)
	}

	hub.mu.Lock()
	info := hub.devices[normalised]
	address := hub.addresses[normalised]
	queued := len(hub.queue)
	hub.mu.Unlock()

	if info == nil || !info.Manual {
		t.Fatalf("the device must be listed as added by hand: %+v", info)
	}
	if info.Name != "My battery" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.Status != statusKnown {
		t.Errorf("a hand added device is listed like a remembered battery, got %q", info.Status)
	}
	if address.MAC[0] == 0 && address.MAC[5] == 0 {
		t.Error("the address must be parsed for a connection attempt")
	}
	if queued == 0 {
		t.Error("the address must be queued for identification right away")
	}

	state := hub.State()
	if len(state.Manual) != 1 || state.Manual[0] != normalised {
		t.Errorf("the snapshot must list the hand added address, got %+v", state.Manual)
	}
}

func TestManualDeviceSurvivesRestart(t *testing.T) {
	directory := t.TempDir()
	store := NewStore(directory)
	hub := NewHub(bluetooth.DefaultAdapter, store)
	if _, err := hub.AddManual("AA:BB:CC:11:22:33", "Garage pack"); err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}

	// The next run must list the address again, without any advertisement.
	reopened := NewStore(directory)
	restarted := NewHub(bluetooth.DefaultAdapter, reopened)
	restarted.mu.Lock()
	info := restarted.devices["AA:BB:CC:11:22:33"]
	queued := len(restarted.queue)
	restarted.mu.Unlock()

	if info == nil || !info.Manual {
		t.Fatalf("the hand added address must be restored: %+v", info)
	}
	if info.Name != "Garage pack" {
		t.Errorf("name: got %q", info.Name)
	}
	if queued == 0 {
		t.Error("a restored hand added address must be probed on start")
	}
}

func TestRemoveManualDropsTheEntry(t *testing.T) {
	store := NewStore(t.TempDir())
	hub := NewHub(bluetooth.DefaultAdapter, store)
	if _, err := hub.AddManual("AA:BB:CC:11:22:33", ""); err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}
	if err := hub.RemoveManual("aa-bb-cc-11-22-33"); err != nil {
		t.Fatalf("removing failed: %v", err)
	}

	hub.mu.Lock()
	_, exists := hub.devices["AA:BB:CC:11:22:33"]
	hub.mu.Unlock()
	if exists {
		t.Error("the entry must be gone")
	}
	if watched := store.Watched(); len(watched) != 0 {
		t.Errorf("the stored entry must be gone, got %+v", watched)
	}
}

func TestManualDeviceHasProbePriority(t *testing.T) {
	info := &DeviceInfo{Address: "AA:BB:CC:11:22:33", Manual: true, Status: statusKnown}
	if priority := classifyDevice(fakeAdvertiser{}, info); priority != priorityHigh {
		t.Errorf("a hand added address must be probed first, got priority %d", priority)
	}
}

func TestManualNameKeepsAdvertisedName(t *testing.T) {
	store := NewStore(t.TempDir())
	hub := NewHub(bluetooth.DefaultAdapter, store)
	if _, err := hub.AddManual("AA:BB:CC:11:22:33", "Garage pack"); err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}
	// A real advertisement with the device's own name must win.
	hub.mu.Lock()
	hub.mergeSeenLocked("AA:BB:CC:11:22:33", "HS0309", "", false, -70)
	name := hub.devices["AA:BB:CC:11:22:33"].Name
	manual := hub.devices["AA:BB:CC:11:22:33"].Manual
	hub.mu.Unlock()

	if name != "HS0309" {
		t.Errorf("the advertised name must win, got %q", name)
	}
	if !manual {
		t.Error("the manual flag must stay")
	}
	if watched := store.Watched(); len(watched) != 1 || !strings.EqualFold(watched[0].Address, "AA:BB:CC:11:22:33") {
		t.Errorf("the address must stay stored, got %+v", watched)
	}
}

func TestManualAddressStaysOnTheBatteryList(t *testing.T) {
	hub := NewHub(bluetooth.DefaultAdapter, NewStore(t.TempDir()))
	if _, err := hub.AddManual("AA:BB:CC:11:22:33", "Test by MAC"); err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}

	// The device does not answer (there is no such battery anywhere): the entry
	// must stay on the list so the user can see it and remove it again.
	hub.mu.Lock()
	hub.devices["AA:BB:CC:11:22:33"].Status = statusUnavailable
	hub.mu.Unlock()

	found := false
	for _, battery := range hub.State().Batteries {
		if battery.Address == "AA:BB:CC:11:22:33" {
			found = true
			if !battery.Manual {
				t.Error("the entry must be marked as added by hand")
			}
		}
	}
	if !found {
		t.Error("a hand added address must stay on the battery list while it is not answering")
	}
}

func TestManualAddressKeepsTryingToConnect(t *testing.T) {
	hub := NewHub(bluetooth.DefaultAdapter, NewStore(t.TempDir()))
	if _, err := hub.AddManual("AA:BB:CC:11:22:33", ""); err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}
	// A hand added address is what the session should monitor, even when it is
	// not advertising (that is the whole point of adding it by hand).
	best, ok := hub.BestBattery()
	if !ok || best.Address != "AA:BB:CC:11:22:33" {
		t.Fatalf("the hand added address must be selected, got %+v (%v)", best, ok)
	}
}

func TestNotBatteryKeepsTheManualFlag(t *testing.T) {
	store := NewStore(t.TempDir())
	store.SaveWatched("AA:BB:CC:11:22:33", "Test by MAC")
	store.SaveNotBMS("AA:BB:CC:11:22:33", "")

	watched := store.Watched()
	if len(watched) != 1 {
		t.Fatalf("the hand added address must survive a rejection, got %+v", watched)
	}
	if watched[0].Name != "Test by MAC" {
		t.Errorf("the name must survive, got %q", watched[0].Name)
	}
}

// fakeAdvertiser reports no services at all.
type fakeAdvertiser struct{}

func (fakeAdvertiser) HasServiceUUID(bluetooth.UUID) bool { return false }
