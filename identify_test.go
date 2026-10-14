//go:build windows

package main

import (
	"testing"

	"tinygo.org/x/bluetooth"
)

func TestIsBatteryName(t *testing.T) {
	cases := map[string]bool{
		"HS030902AB26170219":          true,
		"hs0309":                      true,
		"Humsienk-BMS":                true,
		"ECO-WORTHY 12V":              true,
		"DCH-1234":                    true,
		"JBD-SP04S034":                true,
		"Smart BMS 100A":              true,
		"":                            false,
		"FnR 6ATAG72R":                false,
		"soundcore Q20i_BLE":          false,
		"HLK-LD2450_3059":             false,
		"Bluetooth 48:49:49:d8:df:2e": false,
	}
	for name, want := range cases {
		if got := isBatteryName(name); got != want {
			t.Errorf("isBatteryName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsSyntheticName(t *testing.T) {
	if !isSyntheticName("Bluetooth 48:49:49:d8:df:2e") {
		t.Error("Windows generated labels must be recognised")
	}
	if isSyntheticName("Bluetooth Speaker") {
		t.Error("a friendly name must not be treated as generated")
	}
	if isSyntheticName("HS030902AB26170219") {
		t.Error("a battery name must not be treated as generated")
	}
	if got := displayName("Bluetooth 48:49:49:d8:df:2e"); got != "" {
		t.Errorf("generated names must not be displayed, got %q", got)
	}
	if got := displayName(" HS0309 "); got != "HS0309" {
		t.Errorf("displayName must trim, got %q", got)
	}
}

// fakeAdvert reports a fixed set of advertised services.
type fakeAdvert struct{ uuids []bluetooth.UUID }

func (f fakeAdvert) HasServiceUUID(uuid bluetooth.UUID) bool {
	for _, candidate := range f.uuids {
		if candidate == uuid {
			return true
		}
	}
	return false
}

func TestClassifyDevice(t *testing.T) {
	cases := []struct {
		name     string
		advert   serviceAdvertiser
		info     DeviceInfo
		expected int
	}{
		{name: "remembered battery", advert: fakeAdvert{}, info: DeviceInfo{Cached: true}, expected: priorityHigh},
		{name: "battery name", advert: fakeAdvert{}, info: DeviceInfo{Name: "HS0309"}, expected: priorityHigh},
		{name: "HS04 service", advert: fakeAdvert{uuids: []bluetooth.UUID{uuidServiceHS04}}, expected: priorityHigh},
		{name: "JBD service", advert: fakeAdvert{uuids: []bluetooth.UUID{uuidServiceJBD}}, expected: priorityHigh},
		{name: "HS03 service", advert: fakeAdvert{uuids: []bluetooth.UUID{uuidServiceHS03}}, expected: priorityMedium},
		{name: "unnamed device", advert: fakeAdvert{}, expected: 0},
		{name: "other named device", advert: fakeAdvert{}, info: DeviceInfo{Name: "soundcore"}, expected: priorityLow},
	}
	for _, test := range cases {
		if got := classifyDevice(test.advert, &test.info); got != test.expected {
			t.Errorf("%s: got priority %d, want %d", test.name, got, test.expected)
		}
	}
}
