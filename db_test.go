//go:build windows

package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleAt(address string, when time.Time, cells []int) *Sample {
	soh := 100
	sample := &Sample{
		Protocol:      "HS03",
		VoltageV:      13.4,
		CurrentA:      9.9,
		SoC:           88,
		SoH:           &soh,
		RemainingAh:   276,
		DesignAh:      314,
		CellsMv:       cells,
		TemperaturesC: []float64{22, 26},
		Timestamp:     when,
	}
	sample.Balance = analyseBalance(cells, packContext{DesignAh: 314})
	return sample
}

func TestHistoryDBStoresAndReadsCells(t *testing.T) {
	history := NewHistoryDB(t.TempDir())
	if !history.Enabled() {
		t.Fatal("the database must open")
	}
	defer history.Close()

	start := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	for index := 0; index < 10; index++ {
		history.Record("11:22:33:44:55:66", sampleAt("11:22:33:44:55:66", start.Add(time.Duration(index)*time.Minute),
			[]int{3369 + index, 3369, 3370, 3366}))
	}
	history.Flush()

	stats := history.Stats()
	if stats.Samples != 10 {
		t.Errorf("samples: got %d, want 10", stats.Samples)
	}
	if stats.Batteries != 1 {
		t.Errorf("batteries: got %d, want 1", stats.Batteries)
	}
	if stats.Bytes == 0 {
		t.Error("the database file must have a size")
	}

	series, err := history.CellSeries(cellSeriesRequest{
		Address: "11:22:33:44:55:66",
		Since:   start.Add(-time.Minute),
		Until:   time.Now(),
	})
	if err != nil {
		t.Fatalf("CellSeries: %v", err)
	}
	if len(series) != 10 {
		t.Fatalf("points: got %d, want 10", len(series))
	}
	if len(series[0].Cells) != 4 {
		t.Fatalf("cells per point: got %d, want 4", len(series[0].Cells))
	}
	// Oldest first, with the voltages of that reading.
	if series[0].Cells[0] != 3369 {
		t.Errorf("first cell voltage: got %d, want 3369", series[0].Cells[0])
	}
	if series[9].Cells[0] != 3378 {
		t.Errorf("last cell voltage: got %d, want 3378", series[9].Cells[0])
	}
	if !series[0].Time.Before(series[9].Time) {
		t.Error("the series must be ordered oldest first")
	}
}

// The same reading stored twice must not double the row: a restart, an import
// or two listeners must not corrupt the charts.
func TestHistoryDBIgnoresRepeatedReadings(t *testing.T) {
	history := NewHistoryDB(t.TempDir())
	defer history.Close()

	when := time.Now().Truncate(time.Second)
	sample := sampleAt("11:22:33:44:55:66", when, []int{3369, 3369, 3370, 3366})
	history.Record("11:22:33:44:55:66", sample)
	history.Flush()
	history.Record("11:22:33:44:55:66", sample)
	history.Flush()

	if got := history.Stats().Samples; got != 1 {
		t.Errorf("samples: got %d, want 1", got)
	}
}

func TestHistoryDBBackupAndImport(t *testing.T) {
	directory := t.TempDir()
	original := NewHistoryDB(directory)
	defer original.Close()

	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	for index := 0; index < 5; index++ {
		original.Record("11:22:33:44:55:66", sampleAt("11:22:33:44:55:66",
			start.Add(time.Duration(index)*time.Minute), []int{3369, 3370, 3370, 3366}))
	}
	original.Flush()

	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := original.Backup(backup); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if info, err := os.Stat(backup); err != nil || info.Size() == 0 {
		t.Fatalf("the backup file must exist and have content: %v", err)
	}

	// A second database, as if the application ran on another computer.
	other := NewHistoryDB(t.TempDir())
	defer other.Close()
	other.Record("11:22:33:44:55:66", sampleAt("11:22:33:44:55:66",
		start.Add(30*time.Minute), []int{3371, 3370, 3370, 3366}))
	other.Flush()

	added, err := other.Import(backup)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if added != 5 {
		t.Errorf("imported readings: got %d, want 5", added)
	}
	if got := other.Stats().Samples; got != 6 {
		t.Errorf("samples after import: got %d, want 6", got)
	}

	// Importing the same backup again changes nothing.
	again, err := other.Import(backup)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if again != 0 {
		t.Errorf("the second import must add nothing, got %d", again)
	}
	if got := other.Stats().Samples; got != 6 {
		t.Errorf("samples after the second import: got %d, want 6", got)
	}

	// The cell voltages of the imported readings came along.
	series, err := other.CellSeries(cellSeriesRequest{
		Address: "11:22:33:44:55:66",
		Since:   start.Add(-time.Minute),
		Until:   time.Now(),
	})
	if err != nil {
		t.Fatalf("CellSeries: %v", err)
	}
	if len(series) != 6 {
		t.Fatalf("points after import: got %d, want 6", len(series))
	}
	for _, point := range series {
		if len(point.Cells) != 4 {
			t.Fatalf("every point must carry its cells, got %v", point.Cells)
		}
	}
}

func TestHistoryDBRejectsForeignFile(t *testing.T) {
	history := NewHistoryDB(t.TempDir())
	defer history.Close()

	foreign := filepath.Join(t.TempDir(), "not-a-database.db")
	if err := os.WriteFile(foreign, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Import(foreign); err == nil {
		t.Error("importing something that is not a database must fail")
	}
}

func TestHistoryDBSurvivesRestart(t *testing.T) {
	directory := t.TempDir()
	first := NewHistoryDB(directory)
	when := time.Now().Truncate(time.Second)
	first.Record("11:22:33:44:55:66", sampleAt("11:22:33:44:55:66", when, []int{3369, 3369, 3370, 3366}))
	first.Close()

	second := NewHistoryDB(directory)
	defer second.Close()
	if got := second.Stats().Samples; got != 1 {
		t.Errorf("samples after a restart: got %d, want 1", got)
	}
	// The file is an ordinary SQLite database, so anything can read it.
	db, err := sql.Open("sqlite", second.Path())
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	var cells int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cells`).Scan(&cells); err != nil {
		t.Fatalf("count cells: %v", err)
	}
	if cells != 4 {
		t.Errorf("cell rows: got %d, want 4", cells)
	}
}

func TestHistoryDBWithoutDirectory(t *testing.T) {
	history := NewHistoryDB("")
	if history.Enabled() {
		t.Error("without a directory the database stays closed")
	}
	// Everything must be safe to call on a closed database.
	history.Record("11:22:33:44:55:66", sampleAt("11:22:33:44:55:66", time.Now(), []int{3369, 3369}))
	history.Flush()
	history.Close()
	if _, err := history.CellSeries(cellSeriesRequest{Address: "x"}); err != nil {
		t.Errorf("CellSeries on a closed database: %v", err)
	}
	if history.Stats().Path != "" {
		t.Error("a closed database has no path")
	}
}
