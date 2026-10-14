//go:build windows

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleBattery() storeDevice {
	soh := 100
	return storeDevice{
		Address:  "4E:DF:C5:14:DD:67",
		Name:     "HS030902AB26170219",
		Model:    "BMC-04S001",
		Brand:    "HiLink",
		Protocol: "HS03",
		Status:   "bms",
		LastSample: &Sample{
			Protocol:          "HS03",
			VoltageV:          13.4504,
			CurrentA:          10.0375,
			SoC:               77,
			SoH:               &soh,
			RemainingAh:       242.569,
			DesignAh:          314,
			Cycles:            12,
			CellsMv:           []int{3362, 3362, 3363, 3359},
			TemperaturesC:     []float64{20, 26, 26},
			ChargingMosfet:    boolPtr(true),
			DischargingMosfet: boolPtr(true),
			Balancer:          boolPtr(false),
			ProblemCode:       uint32Ptr(0),
			Timestamp:         time.Date(2026, 10, 14, 12, 18, 51, 0, time.Local),
		},
	}
}

func TestMQTTSlug(t *testing.T) {
	if got := mqttSlug("4E:DF:C5:14:DD:67"); got != "4edfc514dd67" {
		t.Errorf("slug: got %q", got)
	}
}

func TestBuildStatePayload(t *testing.T) {
	data, err := buildStatePayload(sampleBattery())
	if err != nil {
		t.Fatalf("payload failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	checks := map[string]float64{
		"voltage":       13.45,
		"current":       10.038,
		"soc":           77,
		"remaining_ah":  242.57,
		"design_ah":     314,
		"cells":         4,
		"cell_min_v":    3.359,
		"cell_max_v":    3.363,
		"cell_delta_mv": 4,
		"temp_min_c":    20,
		"temp_max_c":    26,
	}
	for key, want := range checks {
		value, ok := payload[key].(float64)
		if !ok {
			t.Errorf("field %q missing or not numeric: %v", key, payload[key])
			continue
		}
		if value != want {
			t.Errorf("field %q: got %v, want %v", key, value, want)
		}
	}
	if payload["charging"] != true || payload["discharging"] != true || payload["balancing"] != false {
		t.Errorf("boolean flags wrong: %v %v %v", payload["charging"], payload["discharging"], payload["balancing"])
	}
	if payload["address"] != "4E:DF:C5:14:DD:67" || payload["model"] != "BMC-04S001" {
		t.Errorf("metadata missing: %v / %v", payload["address"], payload["model"])
	}
}

func TestBuildStatePayloadWithoutSample(t *testing.T) {
	if _, err := buildStatePayload(storeDevice{Address: "AA:BB"}); err == nil {
		t.Error("a battery without a reading must not produce a payload")
	}
}

func TestBuildDiscoveryPayloads(t *testing.T) {
	config := mqttSettings{
		Enabled:         true,
		Host:            "broker.local",
		Port:            1883,
		TopicPrefix:     "lifepo4bms",
		Discovery:       true,
		DiscoveryPrefix: "homeassistant",
	}
	payloads := buildDiscoveryPayloads(sampleBattery(), config, "0.1.0")
	if len(payloads) != len(discoveryEntities) {
		t.Fatalf("expected %d discovery messages, got %d", len(discoveryEntities), len(payloads))
	}

	socTopic := "homeassistant/sensor/lifepo4bms_4edfc514dd67/soc/config"
	raw, ok := payloads[socTopic]
	if !ok {
		t.Fatalf("missing discovery topic %s", socTopic)
	}
	var message map[string]any
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("discovery payload is not valid JSON: %v", err)
	}
	if message["unique_id"] != "lifepo4bms_4edfc514dd67_soc" {
		t.Errorf("unique_id: %v", message["unique_id"])
	}
	if message["state_topic"] != "lifepo4bms/4edfc514dd67/state" {
		t.Errorf("state_topic: %v", message["state_topic"])
	}
	if message["availability_topic"] != "lifepo4bms/status" {
		t.Errorf("availability_topic: %v", message["availability_topic"])
	}
	if message["unit_of_measurement"] != "%" || message["device_class"] != "battery" {
		t.Errorf("soc units: %v / %v", message["unit_of_measurement"], message["device_class"])
	}
	if !strings.Contains(message["value_template"].(string), "value_json.soc") {
		t.Errorf("value_template: %v", message["value_template"])
	}
	device, ok := message["device"].(map[string]any)
	if !ok {
		t.Fatal("device block missing")
	}
	if device["name"] != "HS030902AB26170219" || device["model"] != "BMC-04S001" || device["manufacturer"] != "HiLink" {
		t.Errorf("device block: %+v", device)
	}
	identifiers, ok := device["identifiers"].([]any)
	if !ok || len(identifiers) != 1 || identifiers[0] != "lifepo4bms_4edfc514dd67" {
		t.Errorf("device identifiers: %+v", device["identifiers"])
	}
}

func TestBuildDiscoveryPayloadsUsesFallbacks(t *testing.T) {
	battery := storeDevice{Address: "AA:BB:CC:DD:EE:FF", Status: "bms"}
	payloads := buildDiscoveryPayloads(battery, mqttSettings{
		TopicPrefix:     "bms",
		DiscoveryPrefix: "homeassistant",
	}, "0.1.0")
	raw := payloads["homeassistant/sensor/bms_aabbccddeeff/voltage/config"]
	if raw == nil {
		t.Fatal("voltage discovery missing")
	}
	var message map[string]any
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	device := message["device"].(map[string]any)
	if device["name"] != "BMS AA:BB:CC:DD:EE:FF" {
		t.Errorf("fallback device name: %v", device["name"])
	}
}

func TestBatteryListSignatureIgnoresOrder(t *testing.T) {
	first := []storeDevice{
		{Address: "B", Name: "beta"},
		{Address: "A", Name: "alpha"},
	}
	second := []storeDevice{
		{Address: "A", Name: "alpha"},
		{Address: "B", Name: "beta"},
	}
	if batteryListSignature(first) != batteryListSignature(second) {
		t.Error("the signature must not depend on the order of the list")
	}
	if batteryListSignature(first) == batteryListSignature([]storeDevice{{Address: "A", Name: "alpha"}}) {
		t.Error("the signature must change when a battery disappears")
	}
}
