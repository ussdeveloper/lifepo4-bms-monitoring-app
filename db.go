package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// HistoryDB keeps every reading in a SQLite file next to the other data, so the
// cell voltages can be charted over hours, days or months later — the CSV files
// are per battery and meant for quick checks, the database is the long term
// store. The file is a single ordinary SQLite database, which is what makes the
// backup a plain file download.
type HistoryDB struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
	// batch collects rows so a reading costs one transaction, not one fsync.
	pending []pendingSample
}

type pendingSample struct {
	address string
	sample  *Sample
}

// historySchema creates the tables if they are not there yet.
const historySchema = `
CREATE TABLE IF NOT EXISTS samples (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	address         TEXT    NOT NULL,
	ts              INTEGER NOT NULL,
	voltage_v       REAL,
	current_a       REAL,
	soc             INTEGER,
	soh             INTEGER,
	remaining_ah    REAL,
	design_ah       REAL,
	cycles          INTEGER,
	cell_min_mv     INTEGER,
	cell_max_mv     INTEGER,
	cell_delta_mv   INTEGER,
	temp_min_c      REAL,
	temp_max_c      REAL,
	balance_spread_mv INTEGER,
	balance_state   TEXT,
	UNIQUE (address, ts)
);
CREATE TABLE IF NOT EXISTS cells (
	sample_id INTEGER NOT NULL REFERENCES samples (id) ON DELETE CASCADE,
	cell      INTEGER NOT NULL,
	mv        INTEGER NOT NULL,
	PRIMARY KEY (sample_id, cell)
);
CREATE INDEX IF NOT EXISTS samples_address_ts ON samples (address, ts);
`

// NewHistoryDB opens (and creates) the database. A missing or unreadable
// database is not fatal: the application runs without long term history.
func NewHistoryDB(directory string) *HistoryDB {
	history := &HistoryDB{}
	if directory == "" {
		return history
	}
	history.path = filepath.Join(directory, databaseFileName)
	db, err := sql.Open("sqlite", history.path)
	if err != nil {
		log.Printf("cannot open %s: %v", history.path, err)
		return history
	}
	// One writer: the schema and the inserts come from different goroutines.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(historySchema); err != nil {
		log.Printf("cannot prepare %s: %v", history.path, err)
		db.Close()
		return history
	}
	history.db = db
	return history
}

const databaseFileName = "history.db"

// historyFlushEvery is how often queued readings reach the database file: often
// enough that nothing is lost, rare enough that a reading is not a file write.
const historyFlushEvery = 15 * time.Second

// Path is the database file (empty when the store is disabled).
func (h *HistoryDB) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Enabled reports whether readings are being stored.
func (h *HistoryDB) Enabled() bool { return h != nil && h.db != nil }

// Close flushes what is pending and closes the file.
func (h *HistoryDB) Close() {
	if !h.Enabled() {
		return
	}
	h.Flush()
	h.mu.Lock()
	db := h.db
	h.db = nil
	h.mu.Unlock()
	_ = db.Close()
}

// Record queues one reading. The rows are written with the next flush, so a
// reading every few seconds costs a single transaction (see Flush).
func (h *HistoryDB) Record(address string, sample *Sample) {
	if !h.Enabled() || sample == nil || address == "" {
		return
	}
	copied := *sample
	copied.CellsMv = append([]int(nil), sample.CellsMv...)
	h.mu.Lock()
	h.pending = append(h.pending, pendingSample{address: address, sample: &copied})
	h.mu.Unlock()
}

// Flush writes the queued readings in one transaction.
func (h *HistoryDB) Flush() {
	h.mu.Lock()
	if len(h.pending) == 0 || h.db == nil {
		h.mu.Unlock()
		return
	}
	batch := h.pending
	h.pending = nil
	db := h.db
	h.mu.Unlock()

	if err := writeBatch(db, batch); err != nil {
		log.Printf("cannot write the history database: %v", err)
	}
}

func writeBatch(db *sql.DB, batch []pendingSample) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sampleStmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO samples
			(address, ts, voltage_v, current_a, soc, soh, remaining_ah, design_ah, cycles,
			 cell_min_mv, cell_max_mv, cell_delta_mv, temp_min_c, temp_max_c,
			 balance_spread_mv, balance_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer sampleStmt.Close()

	idStmt, err := tx.Prepare(`SELECT id FROM samples WHERE address = ? AND ts = ?`)
	if err != nil {
		return err
	}
	defer idStmt.Close()

	cellStmt, err := tx.Prepare(`INSERT OR REPLACE INTO cells (sample_id, cell, mv) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer cellStmt.Close()

	for _, entry := range batch {
		sample := entry.sample
		soh := sql.NullInt64{}
		if sample.SoH != nil {
			soh = sql.NullInt64{Int64: int64(*sample.SoH), Valid: true}
		}
		minimum, maximum := cellRange(sample.CellsMv)
		spread := 0
		state := ""
		if sample.Balance != nil {
			spread = sample.Balance.SpreadMv
			state = sample.Balance.State
		}
		if _, err := sampleStmt.Exec(
			entry.address, sample.Timestamp.Unix(),
			sample.VoltageV, sample.CurrentA, sample.SoC, soh,
			sample.RemainingAh, sample.DesignAh, sample.Cycles,
			minimum, maximum, spread, minimumTemperature(sample.TemperaturesC),
			maximumTemperature(sample.TemperaturesC), spread, state,
		); err != nil {
			return err
		}
		if len(sample.CellsMv) == 0 {
			continue
		}
		var id int64
		if err := idStmt.QueryRow(entry.address, sample.Timestamp.Unix()).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		for index, millivolts := range sample.CellsMv {
			if _, err := cellStmt.Exec(id, index+1, millivolts); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func cellRange(cells []int) (int, int) {
	if len(cells) == 0 {
		return 0, 0
	}
	minimum, maximum := cells[0], cells[0]
	for _, millivolts := range cells {
		if millivolts < minimum {
			minimum = millivolts
		}
		if millivolts > maximum {
			maximum = millivolts
		}
	}
	return minimum, maximum
}

func minimumTemperature(temperatures []float64) float64 {
	if len(temperatures) == 0 {
		return 0
	}
	minimum := temperatures[0]
	for _, value := range temperatures {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func maximumTemperature(temperatures []float64) float64 {
	if len(temperatures) == 0 {
		return 0
	}
	maximum := temperatures[0]
	for _, value := range temperatures {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

/* --- reading the history back --- */

// cellSeriesRequest selects the readings drawn on the chart.
type cellSeriesRequest struct {
	Address string
	Since   time.Time
	Until   time.Time
	Limit   int
}

// cellSample is one stored reading with the voltages of its cells.
type cellSample struct {
	Time  time.Time `json:"t"`
	Soc   int       `json:"soc"`
	Cells []int     `json:"cells"`
}

// CellSeries returns the stored cell voltages, oldest first, thinned down to at
// most limit points so a week of readings still draws quickly.
func (h *HistoryDB) CellSeries(request cellSeriesRequest) ([]cellSample, error) {
	if !h.Enabled() {
		return nil, nil
	}
	limit := request.Limit
	if limit <= 0 || limit > 2000 {
		limit = 400
	}
	rows, err := h.db.Query(`
		SELECT ts, soc, cells
		FROM (
			SELECT s.ts AS ts, s.soc AS soc,
			       (SELECT group_concat(c.mv, ',') FROM cells c
			         WHERE c.sample_id = s.id ORDER BY c.cell) AS cells,
			       ROW_NUMBER() OVER (ORDER BY s.ts) AS row_number,
			       COUNT(*) OVER () AS row_count
			FROM samples s
			WHERE s.address = ? AND s.ts >= ? AND s.ts <= ?
		)
		WHERE row_number % MAX(1, (row_count / ?)) = 0 OR row_number = row_count
		ORDER BY ts
		LIMIT ?`, request.Address, request.Since.Unix(), request.Until.Unix(), limit, limit+2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	series := []cellSample{}
	for rows.Next() {
		var timestamp int64
		var soc sql.NullInt64
		var cells sql.NullString
		if err := rows.Scan(&timestamp, &soc, &cells); err != nil {
			return nil, err
		}
		entry := cellSample{Time: time.Unix(timestamp, 0).UTC(), Soc: int(soc.Int64)}
		for _, text := range strings.Split(cells.String, ",") {
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			value := 0
			if _, err := fmt.Sscanf(text, "%d", &value); err == nil {
				entry.Cells = append(entry.Cells, value)
			}
		}
		if len(entry.Cells) == 0 {
			continue
		}
		series = append(series, entry)
	}
	return series, rows.Err()
}

// Stats describes what the database holds, for the settings panel.
type historyStats struct {
	Path      string `json:"path"`
	Samples   int64  `json:"samples"`
	Batteries int64  `json:"batteries"`
	First     string `json:"first,omitempty"`
	Last      string `json:"last,omitempty"`
	Bytes     int64  `json:"bytes"`
}

func (h *HistoryDB) Stats() historyStats {
	stats := historyStats{Path: h.Path()}
	if !h.Enabled() {
		return stats
	}
	h.Flush()
	_ = h.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT address), MIN(ts), MAX(ts) FROM samples`).
		Scan(&stats.Samples, &stats.Batteries, new(sql.NullInt64), new(sql.NullInt64))

	var first, last sql.NullInt64
	if err := h.db.QueryRow(`SELECT MIN(ts), MAX(ts) FROM samples`).Scan(&first, &last); err == nil {
		if first.Valid {
			stats.First = time.Unix(first.Int64, 0).UTC().Format(time.RFC3339)
		}
		if last.Valid {
			stats.Last = time.Unix(last.Int64, 0).UTC().Format(time.RFC3339)
		}
	}
	if info, err := os.Stat(h.path); err == nil {
		stats.Bytes = info.Size()
	}
	return stats
}

/* --- backup --- */

// Backup writes a consistent copy of the database to the given file.
func (h *HistoryDB) Backup(target string) error {
	if !h.Enabled() {
		return errors.New("the history database is not open")
	}
	h.Flush()
	_ = os.Remove(target)
	// VACUUM INTO writes a clean single file, without the temporary pages a
	// plain copy could catch in the middle of a write.
	if _, err := h.db.Exec(`VACUUM INTO ?`, target); err != nil {
		return err
	}
	return nil
}

// Import merges another database into this one: every reading that is not here
// yet is added, nothing is overwritten or removed.
func (h *HistoryDB) Import(source string) (int64, error) {
	if !h.Enabled() {
		return 0, errors.New("the history database is not open")
	}
	h.Flush()
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, err := h.db.Exec(`ATTACH DATABASE ? AS incoming`, source); err != nil {
		return 0, fmt.Errorf("cannot read the backup: %w", err)
	}
	defer h.db.Exec(`DETACH DATABASE incoming`)

	// Copy the rows that are not here yet; the unique index on (address, ts)
	// makes the merge idempotent, so importing the same file twice is harmless.
	result, err := h.db.Exec(`
		INSERT OR IGNORE INTO samples
			(address, ts, voltage_v, current_a, soc, soh, remaining_ah, design_ah, cycles,
			 cell_min_mv, cell_max_mv, cell_delta_mv, temp_min_c, temp_max_c,
			 balance_spread_mv, balance_state)
		SELECT address, ts, voltage_v, current_a, soc, soh, remaining_ah, design_ah, cycles,
			 cell_min_mv, cell_max_mv, cell_delta_mv, temp_min_c, temp_max_c,
			 balance_spread_mv, balance_state
		FROM incoming.samples`)
	if err != nil {
		return 0, fmt.Errorf("cannot merge the readings: %w", err)
	}
	added, _ := result.RowsAffected()

	if _, err := h.db.Exec(`
		INSERT OR REPLACE INTO cells (sample_id, cell, mv)
		SELECT mine.id, incoming.cells.cell, incoming.cells.mv
		FROM incoming.cells
		JOIN incoming.samples AS theirs ON theirs.id = incoming.cells.sample_id
		JOIN samples AS mine ON mine.address = theirs.address AND mine.ts = theirs.ts`); err != nil {
		return added, fmt.Errorf("cannot merge the cell voltages: %w", err)
	}
	return added, nil
}
