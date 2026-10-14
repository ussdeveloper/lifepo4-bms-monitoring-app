//go:build windows

package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func testBattery() DeviceInfo {
	return DeviceInfo{
		Address:  "4E:DF:C5:14:DD:67",
		Name:     "HS030902AB26170219",
		Model:    "BMC-04S001",
		Brand:    "HiLink",
		Protocol: "HS03",
		Status:   statusBMS,
		RSSI:     -80,
	}
}

func testSample() *Sample {
	soh := 100
	return &Sample{
		Protocol:          "HS03",
		VoltageV:          13.44,
		CurrentA:          9.96,
		SoC:               68,
		SoH:               &soh,
		RemainingAh:       210.5,
		DesignAh:          314,
		Cycles:            12,
		CellsMv:           []int{3362, 3362, 3363, 3359},
		TemperaturesC:     []float64{18, 26, 26},
		ChargingMosfet:    boolPtr(true),
		DischargingMosfet: boolPtr(false),
		ProblemCode:       uint32Ptr(0),
	}
}

func TestStoreKeepsKnownBatteries(t *testing.T) {
	directory := t.TempDir()
	store := NewStore(directory)
	if !store.Enabled() {
		t.Fatal("store must be enabled for an existing directory")
	}
	store.SaveBattery(testBattery())

	if known := store.Known(); len(known) != 1 || known[0].Protocol != "HS03" || known[0].Model != "BMC-04S001" {
		t.Fatalf("unexpected known devices: %+v", known)
	}
	if !store.IsKnownBattery(testBattery().Address) {
		t.Error("the battery should be marked as known")
	}

	// A new store instance must read the file back.
	reopened := NewStore(directory)
	if known := reopened.Known(); len(known) != 1 || known[0].Name != "HS030902AB26170219" {
		t.Fatalf("saved devices were not restored: %+v", known)
	}
	if _, err := os.Stat(filepath.Join(directory, "devices.json")); err != nil {
		t.Errorf("devices.json missing: %v", err)
	}
}

func TestStoreCachesRejectedDevices(t *testing.T) {
	store := NewStore(t.TempDir())
	store.SaveNotBMS("00:11:22:33:44:55", "radar-sensor")

	if !store.IsNotBMS("00:11:22:33:44:55") {
		t.Error("rejected device should be cached")
	}
	if store.IsKnownBattery("00:11:22:33:44:55") {
		t.Error("rejected device must not count as a battery")
	}
	// Lower case addresses refer to the same device.
	if !store.IsNotBMS("00:11:22:33:44:55") {
		t.Error("address lookup must be case insensitive")
	}

	store.ForgetNotBMS()
	if store.IsNotBMS("00:11:22:33:44:55") {
		t.Error("deep scan should clear the rejection cache")
	}
}

func TestStoreWritesSampleHistory(t *testing.T) {
	directory := t.TempDir()
	store := NewStore(directory)
	device := testBattery()
	sample := testSample()

	for index := 0; index < 2; index++ {
		if err := store.AppendSample(device.Address, device, sample); err != nil {
			t.Fatalf("append failed: %v", err)
		}
	}

	path := filepath.Join(directory, "history", "4E-DF-C5-14-DD-67.csv")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("history file missing: %v", err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("cannot parse history: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected a header and two rows, got %d rows", len(rows))
	}
	header := rows[0]
	value := map[string]string{}
	for index, column := range header {
		value[column] = rows[1][index]
	}
	checks := map[string]string{
		"protocol":        "HS03",
		"model":           "BMC-04S001",
		"brand":           "HiLink",
		"soc_pct":         "68",
		"cell_min_mv":     "3359",
		"cell_max_mv":     "3363",
		"cell_delta_mv":   "4",
		"cells":           "4",
		"temp_min_c":      "18.0",
		"temp_max_c":      "26.0",
		"charging_mosfet": "on",
		"balancer":        "",
	}
	for column, want := range checks {
		if _, ok := value[column]; !ok {
			t.Errorf("column %q missing in the history file", column)
			continue
		}
		if value[column] != want {
			t.Errorf("column %q: got %q, want %q", column, value[column], want)
		}
	}

	if count := store.SampleCount(device.Address); count != 2 {
		t.Errorf("sample count: got %d, want 2", count)
	}
}

func TestHistoryFileNameIsSafe(t *testing.T) {
	if got := historyFileName("4E:DF:C5:14:DD:67"); got != "4E-DF-C5-14-DD-67" {
		t.Errorf("got %q", got)
	}
	if got := historyFileName(""); got != "unknown" {
		t.Errorf("empty address should fall back to a placeholder, got %q", got)
	}
}

func TestStoreWithoutDirectoryIsDisabled(t *testing.T) {
	// An unusable path must not break the application.
	store := NewStore(filepath.Join(t.TempDir(), "missing", "deeper") + string(os.PathSeparator) + "\x00invalid")
	if store.Enabled() {
		t.Error("store should be disabled for an invalid path")
	}
	if err := store.AppendSample("AA:BB", DeviceInfo{}, testSample()); err != nil {
		t.Errorf("disabled store must not report errors: %v", err)
	}
	if store.SampleCount("AA:BB") != 0 || len(store.Known()) != 0 {
		t.Error("disabled store must stay empty")
	}
}
