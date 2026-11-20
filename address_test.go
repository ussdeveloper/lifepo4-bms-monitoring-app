//go:build windows

package main

import "testing"

// A battery that was read before has to stay reachable while it is not
// advertising. The address it was saved under is enough for Windows to connect;
// waiting for a fresh advertisement is what made a battery at the edge of the
// range impossible to monitor, because it announces itself only now and then.
func TestAddressOfFallsBackToTheSavedAddress(t *testing.T) {
	hub := NewHub(nil, NewStore(""))

	address, ok := hub.AddressOf("4E:DF:C5:14:DD:67")
	if !ok {
		t.Fatal("a remembered address must be usable without a scan result")
	}
	if got := address.String(); got != "4E:DF:C5:14:DD:67" {
		t.Errorf("address: got %q", got)
	}

	// The same address in lower case is answered from the cache, unchanged.
	again, ok := hub.AddressOf("4e:df:c5:14:dd:67")
	if !ok || again.String() != address.String() {
		t.Errorf("second lookup: got %v, ok=%v", again, ok)
	}

	if _, ok := hub.AddressOf("not an address"); ok {
		t.Error("a malformed address must be refused")
	}
}
