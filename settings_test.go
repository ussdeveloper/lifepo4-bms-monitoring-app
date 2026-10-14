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

	// The interval is clamped to the supported range.
	interval := 0.1
	if _, _, after := settings.Update(settingsPatch{IntervalS: &interval}); after != time.Second {
		t.Errorf("too small interval: got %s, want 1s", after)
	}
	interval = 99999
	if _, _, after := settings.Update(settingsPatch{IntervalS: &interval}); after != time.Hour {
		t.Errorf("too large interval: got %s, want 1h", after)
	}
}

func TestSettingsUpdatePersists(t *testing.T) {
	directory := t.TempDir()
	settings := NewSettings(directory, 5*time.Second)

	interval := 12.0
	apiEnabled := false
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
		IntervalS:  &interval,
		APIEnabled: &apiEnabled,
		MQTT:       &mqttConfig,
	})
	if before != 5*time.Second || after != 12*time.Second {
		t.Errorf("interval transition: %s -> %s", before, after)
	}
	if view.IntervalS != 12 || view.APIEnabled || !view.MQTT.Enabled {
		t.Errorf("unexpected view: %+v", view)
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

	// Without a patch the other values stay untouched.
	interval = 30
	view, _, _ = settings.Update(settingsPatch{IntervalS: &interval})
	if !view.MQTT.Enabled || view.MQTT.Host != "broker.local" {
		t.Errorf("partial update cleared MQTT settings: %+v", view.MQTT)
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
