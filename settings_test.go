//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsDefaultsAndLimits(t *testing.T) {
	directory := t.TempDir()
	settings := NewSettings(directory, 5*time.Second)

	if got := settings.Interval(); got != 5*time.Second {
		t.Fatalf("interval: got %s, want 5s", got)
	}
	if !settings.APIEnabled() {
		t.Error("the read API should be enabled by default")
	}
	if mqtt := settings.MQTT(); mqtt.Enabled || mqtt.Port != 1883 || mqtt.TopicPrefix != "lifepo4bms" {
		t.Errorf("unexpected default MQTT settings: %+v", mqtt)
	}
	if got := settings.ReleaseDuration(); got != 5*time.Minute {
		t.Errorf("release time: got %s, want 5m", got)
	}
	if settings.Console() {
		t.Error("the console window must be off by default")
	}

	// The interval is clamped to the supported range.
	interval := 0.1
	if _, _, after := settings.Update(settingsPatch{IntervalS: &interval}); after != time.Second {
		t.Errorf("too small interval: got %s, want 1s", after)
	}
	interval = 99999
	if _, _, after := settings.Update(settingsPatch{IntervalS: &interval}); after != time.Hour {
		t.Errorf("too large interval: got %s, want 1h", after)
	}

	// The release time is clamped to the supported range too.
	tooShort := 0
	view, _, _ := settings.Update(settingsPatch{ReleaseMins: &tooShort})
	if got := settings.ReleaseDuration(); got != time.Minute || view.ReleaseMins != 1 {
		t.Errorf("too short release: got %s (%d min), want 1m", got, view.ReleaseMins)
	}
	tooLong := 5000
	view, _, _ = settings.Update(settingsPatch{ReleaseMins: &tooLong})
	if got := settings.ReleaseDuration(); got != 120*time.Minute || view.ReleaseMins != 120 {
		t.Errorf("too long release: got %s (%d min), want 120m", got, view.ReleaseMins)
	}
}

func TestSettingsUpdatePersists(t *testing.T) {
	directory := t.TempDir()
	settings := NewSettings(directory, 5*time.Second)

	interval := 12.0
	apiEnabled := false
	releaseMins := 15
	console := true
	mqttConfig := mqttSettings{
		Enabled:         true,
		Host:            "  broker.local  ",
		Port:            8883,
		Username:        " user ",
		Password:        "secret",
		TopicPrefix:     " BMS Topic! ",
		Discovery:       true,
		DiscoveryPrefix: "ha",
	}
	view, before, after := settings.Update(settingsPatch{
		IntervalS:   &interval,
		APIEnabled:  &apiEnabled,
		ReleaseMins: &releaseMins,
		Console:     &console,
		MQTT:        &mqttConfig,
	})
	if before != 5*time.Second || after != 12*time.Second {
		t.Errorf("interval transition: %s -> %s", before, after)
	}
	if view.IntervalS != 12 || view.APIEnabled || !view.MQTT.Enabled {
		t.Errorf("unexpected view: %+v", view)
	}
	if !view.Console {
		t.Error("the console flag must reach the interface")
	}
	if view.MinReleaseMins != 1 || view.MaxReleaseMins != 120 {
		t.Errorf("release limits not exposed: %+v", view)
	}
	if view.MQTT.Host != "broker.local" {
		t.Errorf("host must be trimmed, got %q", view.MQTT.Host)
	}
	if view.MQTT.TopicPrefix != "BMSTopic" {
		t.Errorf("topic prefix must be sanitised, got %q", view.MQTT.TopicPrefix)
	}

	// A second instance reads the same file back.
	reloaded := NewSettings(directory, 5*time.Second)
	if got := reloaded.Interval(); got != 12*time.Second {
		t.Errorf("interval not persisted: %s", got)
	}
	if reloaded.APIEnabled() {
		t.Error("API toggle not persisted")
	}
	if mqtt := reloaded.MQTT(); mqtt.Host != "broker.local" || mqtt.Port != 8883 || mqtt.Password != "secret" {
		t.Errorf("MQTT settings not persisted: %+v", mqtt)
	}
	if got := reloaded.ReleaseDuration(); got != 15*time.Minute {
		t.Errorf("release time not persisted: %s", got)
	}
	if !reloaded.Console() {
		t.Error("the console setting not persisted")
	}

	// Without a patch the other values stay untouched.
	interval = 30
	view, _, _ = settings.Update(settingsPatch{IntervalS: &interval})
	if !view.MQTT.Enabled || view.MQTT.Host != "broker.local" {
		t.Errorf("partial update cleared MQTT settings: %+v", view.MQTT)
	}
	if view.ReleaseMins != 15 {
		t.Errorf("partial update cleared the release time: %d", view.ReleaseMins)
	}
	if !view.Console {
		t.Error("a partial update must not switch the console off")
	}
	if _, err := os.Stat(filepath.Join(directory, "settings.json")); err != nil {
		t.Errorf("settings.json missing: %v", err)
	}
}

func TestSettingsWithoutDirectory(t *testing.T) {
	settings := NewSettings("", 3*time.Second)
	if got := settings.Interval(); got != 3*time.Second {
		t.Errorf("interval: got %s, want 3s", got)
	}
	if view := settings.View(); view.Path != "" {
		t.Errorf("path should be empty, got %q", view.Path)
	}
	// Updating must not panic when there is nowhere to save.
	interval := 7.0
	settings.Update(settingsPatch{IntervalS: &interval})
	if got := settings.Interval(); got != 7*time.Second {
		t.Errorf("interval after update: got %s, want 7s", got)
	}
}
