//go:build windows

package main

import (
	"testing"
	"time"
)

func TestTakeoverIsReportedOnlyAfterAWorkingLink(t *testing.T) {
	session := NewSession(nil, nil, NewSettings("", 5*time.Second), nil, nil)

	// A battery that was never reached is simply retried; nothing is reported.
	session.noteTakeover(false, 99)
	if session.Takeover() {
		t.Error("a battery that never answered must not be reported as taken over")
	}

	// A few failures are tolerated (Bluetooth hiccups happen) ...
	session.noteTakeover(true, takeoverAttempts-1)
	if session.Takeover() {
		t.Error("a short burst of failures must not be reported as a takeover")
	}

	// ... but a link that was working and is gone is explained to the user.
	session.noteTakeover(true, takeoverAttempts)
	if !session.Takeover() {
		t.Fatal("a lost working link must be reported as a takeover")
	}
}

func TestReconnectClearsTheTakeoverNotice(t *testing.T) {
	session := NewSession(nil, nil, NewSettings("", 5*time.Second), nil, nil)
	session.noteTakeover(true, takeoverAttempts)
	if !session.Takeover() {
		t.Fatal("expected the takeover note")
	}

	// Getting the battery back (or selecting one) hides the notice.
	session.markConnected()
	if session.Takeover() {
		t.Error("a fresh connection must clear the takeover notice")
	}
}

func TestTakeoverSurvivesUntilTheBatteryIsBack(t *testing.T) {
	session := NewSession(nil, nil, NewSettings("", 5*time.Second), nil, nil)
	session.noteTakeover(true, takeoverAttempts)

	// Monitoring keeps retrying: the state must say so instead of pretending
	// everything is fine.
	state := session.State()
	if !state.Takeover {
		t.Error("the session snapshot must carry the takeover flag")
	}
	if state.Sample != nil {
		t.Error("there is no reading while another application holds the battery")
	}
}
