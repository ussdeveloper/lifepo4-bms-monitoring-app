//go:build windows

package main

import "testing"

func TestRemoteAllowed(t *testing.T) {
	cases := []struct {
		name    string
		remote  string
		method  string
		path    string
		allowed bool
	}{
		{"this computer may do anything", "127.0.0.1:51234", "POST", "/api/settings", true},
		{"this computer over IPv6", "[::1]:52000", "POST", "/api/add", true},
		{"another computer reads the state", "10.3.0.100:51234", "GET", "/api/state", true},
		{"another computer reads the batteries", "10.3.0.100:51234", "GET", "/api/batteries", true},
		{"another computer opens the interface", "10.3.0.100:51234", "GET", "/", true},
		{"another computer fetches the script", "10.3.0.100:51234", "GET", "/app.js", true},
		{"another computer cannot read the settings", "10.3.0.100:51234", "GET", "/api/settings", false},
		{"another computer cannot select", "10.3.0.100:51234", "POST", "/api/select", false},
		{"another computer cannot release", "10.3.0.100:51234", "POST", "/api/release", false},
		{"another computer cannot add devices", "10.3.0.100:51234", "POST", "/api/add", false},
		{"another computer cannot move the window", "10.3.0.100:51234", "POST", "/api/window", false},
		{"another computer cannot force a scan", "10.3.0.100:51234", "POST", "/api/deepscan", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := remoteAllowed(test.remote, test.method, test.path); got != test.allowed {
				t.Fatalf("remoteAllowed(%q, %s %s) = %v, want %v",
					test.remote, test.method, test.path, got, test.allowed)
			}
		})
	}
}

func TestListenAddress(t *testing.T) {
	cases := map[string]string{
		"":            "127.0.0.1",
		"local":       "127.0.0.1",
		"LAN":         "0.0.0.0",
		"all":         "0.0.0.0",
		"10.3.0.148":  "10.3.0.148",
		" 0.0.0.0  ":  "0.0.0.0",
		"127.0.0.1":   "127.0.0.1",
		"network":     "0.0.0.0",
		"192.168.5.7": "192.168.5.7",
	}
	for input, want := range cases {
		if got := listenAddress(input); got != want {
			t.Errorf("listenAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNetworkURLs(t *testing.T) {
	if got := networkURLs("127.0.0.1", 8822); len(got) != 0 {
		t.Errorf("a loopback only server must not advertise addresses, got %v", got)
	}
	if got := networkURLs("10.3.0.148", 8822); len(got) != 1 || got[0] != "10.3.0.148:8822" {
		t.Errorf("a bound address must be advertised, got %v", got)
	}
	// The whole network: every usable IPv4 address, without loopback or
	// link-local entries and without duplicates.
	for _, entry := range networkURLs("0.0.0.0", 8822) {
		if entry == "" {
			t.Error("empty address advertised")
		}
		if len(entry) > 5 && entry[:3] == "127" {
			t.Errorf("loopback advertised: %s", entry)
		}
		if len(entry) > 7 && entry[:7] == "169.254" {
			t.Errorf("link local advertised: %s", entry)
		}
	}
}
