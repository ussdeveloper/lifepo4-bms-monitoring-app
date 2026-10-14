//go:build windows

package main

import (
	"strings"
	"testing"

	"tinygo.org/x/bluetooth"
)

func TestFormatBluetoothAddressIsLittleEndian(t *testing.T) {
	// The Win32 structures keep the address in a 64 bit integer, least
	// significant byte first: 0x332211CCBBAA is AA:BB:CC:11:22:33.
	if got := formatBluetoothAddress(0x332211CCBBAA); got != "AA:BB:CC:11:22:33" {
		t.Errorf("got %q, want AA:BB:CC:11:22:33", got)
	}
	if got := formatBluetoothAddress(0); got != "" {
		t.Errorf("an empty address must stay empty, got %q", got)
	}
}

func TestDecodeBluetoothName(t *testing.T) {
	var raw [bluetoothNameLength]uint16
	copy(raw[:], []uint16{'H', 'S', '0', '3', '0', '9'})
	if got := decodeBluetoothName(raw); got != "HS0309" {
		t.Errorf("got %q, want HS0309", got)
	}
	var padded [bluetoothNameLength]uint16
	copy(padded[:], []uint16{' ', 'x', ' ', 0, 'y'})
	if got := decodeBluetoothName(padded); got != "x" {
		t.Errorf("the name ends at the first null and is trimmed, got %q", got)
	}
	var empty [bluetoothNameLength]uint16
	if got := decodeBluetoothName(empty); got != "" {
		t.Errorf("an empty name must stay empty, got %q", got)
	}
}

func TestRadioBatteryCandidate(t *testing.T) {
	known := func(address string) bool {
		return strings.EqualFold(address, "AA:BB:CC:11:22:33")
	}
	cases := []struct {
		device radioDevice
		want   bool
	}{
		{radioDevice{Address: "AA:BB:CC:11:22:33", Name: "whatever"}, true}, // already a battery
		{radioDevice{Address: "11:22:33:44:55:66", Name: "Humsienk-BMS"}, true},
		{radioDevice{Address: "11:22:33:44:55:66", Name: "JBD-SP04S034"}, true},
		{radioDevice{Address: "11:22:33:44:55:66", Name: "Galaxy Buds"}, false},
		{radioDevice{Address: "11:22:33:44:55:66"}, false},
		{radioDevice{Address: "", Name: "Humsienk"}, false},
	}
	for _, testCase := range cases {
		if got := isRadioBatteryCandidate(testCase.device, known); got != testCase.want {
			t.Errorf("%+v: got %v, want %v", testCase.device, got, testCase.want)
		}
	}
}

func TestAdoptRadioDevicesAddsBatteriesFromOtherRadios(t *testing.T) {
	hub := NewHub(bluetooth.DefaultAdapter, NewStore(t.TempDir()))

	adopted := hub.AdoptRadioDevices([]radioDevice{
		{Address: "AA:BB:CC:11:22:33", Name: "Humsienk-BMS", Connected: true},
		{Address: "11:22:33:44:55:66", Name: "Galaxy Buds"},
		{Address: "not an address", Name: "JBD-SP04S034"},
	})
	if adopted != 1 {
		t.Fatalf("only the battery may be adopted, got %d", adopted)
	}

	hub.mu.Lock()
	info := hub.devices["AA:BB:CC:11:22:33"]
	_, hasAddress := hub.addresses["AA:BB:CC:11:22:33"]
	queued := len(hub.queue)
	hub.mu.Unlock()

	if info == nil {
		t.Fatal("the battery from the other radio must be listed")
	}
	if info.Name != "Humsienk-BMS" {
		t.Errorf("the name from the radio must be used, got %q", info.Name)
	}
	if !hasAddress {
		t.Error("the address must be known so the session can connect without an advertisement")
	}
	if queued == 0 {
		t.Error("the adopted battery must be queued for identification")
	}
	if len(hub.Radios()) != 0 {
		t.Error("radio descriptions come from the radio loop, not from adoption")
	}
}

func TestAdoptRadioDevicesKeepsTheBetterName(t *testing.T) {
	hub := NewHub(bluetooth.DefaultAdapter, NewStore(t.TempDir()))
	if _, err := hub.AddManual("AA:BB:CC:11:22:33", "Garage pack"); err != nil {
		t.Fatalf("adding by hand failed: %v", err)
	}
	hub.AdoptRadioDevices([]radioDevice{{Address: "AA:BB:CC:11:22:33"}})

	hub.mu.Lock()
	name := hub.devices["AA:BB:CC:11:22:33"].Name
	hub.mu.Unlock()
	if name != "Garage pack" {
		t.Errorf("a radio without a name must not clear the known name, got %q", name)
	}
}
