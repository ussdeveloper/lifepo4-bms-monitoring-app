package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store keeps application state between runs inside the user profile:
//
//	%APPDATA%\LiFePO4BMSMonitor\devices.json          batteries seen so far + "not a BMS" cache
//	%APPDATA%\LiFePO4BMSMonitor\history\<address>.csv statistics sampled while monitoring
//
// The store is best effort: when the profile directory is not writable the
// application keeps running with persistence disabled.
type Store struct {
	mu        sync.Mutex
	dir       string
	history   string
	devices   map[string]storeDevice
	enabled   bool
	lastFlush time.Time
}

// storeDevice is one entry of devices.json.
type storeDevice struct {
	Address    string  `json:"addr"`
	Name       string  `json:"name,omitempty"`
	Model      string  `json:"model,omitempty"`
	Brand      string  `json:"brand,omitempty"`
	Protocol   string  `json:"protocol,omitempty"`
	Status     string  `json:"status"` // "bms" or "notBMS"
	RSSI       int     `json:"rssi,omitempty"`
	SeenAt     string  `json:"lastSeen,omitempty"`
	LastSample *Sample `json:"lastSample,omitempty"`
}

// storeFile is the on-disk layout of devices.json.
type storeFile struct {
	Version int           `json:"version"`
	Updated string        `json:"updatedAt"`
	Devices []storeDevice `json:"devices"`
}

const (
	storeVersion = 1
	// dataDirName is used below the user configuration directory.
	dataDirName = "LiFePO4BMSMonitor"
	// sampleFlush limits how often the newest readings are written to disk.
	sampleFlush = 30 * time.Second
)

// NewStore opens (or creates) the per-user data directory. A non-empty
// override takes precedence over the platform default.
func NewStore(override string) *Store {
	directory := strings.TrimSpace(override)
	if directory == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			log.Printf("persistence disabled, cannot resolve user config dir: %v", err)
			return &Store{devices: map[string]storeDevice{}}
		}
		directory = filepath.Join(configDir, dataDirName)
	}
	store := &Store{
		dir:     directory,
		history: filepath.Join(directory, "history"),
		devices: map[string]storeDevice{},
	}
	if err := os.MkdirAll(store.history, 0o755); err != nil {
		log.Printf("persistence disabled, cannot create %s: %v", directory, err)
		return &Store{devices: map[string]storeDevice{}}
	}
	store.enabled = true
	store.load()
	log.Printf("data directory: %s", directory)
	return store
}

// Dir returns the directory holding devices.json and the history folder.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Enabled reports whether persistence is available.
func (s *Store) Enabled() bool {
	return s != nil && s.enabled
}

func (s *Store) load() {
	path := filepath.Join(s.dir, "devices.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("ignoring unreadable %s: %v", path, err)
		return
	}
	for _, device := range file.Devices {
		if device.Address == "" {
			continue
		}
		s.devices[strings.ToUpper(device.Address)] = device
	}
	log.Printf("loaded %d saved devices from %s", len(s.devices), path)
}

// Known returns the batteries remembered from previous runs.
func (s *Store) Known() []storeDevice {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := make([]storeDevice, 0, len(s.devices))
	for _, device := range s.devices {
		if device.Status == "bms" {
			known = append(known, device)
		}
	}
	return known
}

// IsNotBMS reports whether the address was already rejected by a probe.
func (s *Store) IsNotBMS(address string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[strings.ToUpper(address)]
	return ok && device.Status == "notBMS"
}

// IsKnownBattery reports whether the address was identified as a battery before.
func (s *Store) IsKnownBattery(address string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[strings.ToUpper(address)]
	return ok && device.Status == "bms"
}

// SaveBattery remembers an identified battery including its protocol and brand.
func (s *Store) SaveBattery(info DeviceInfo) {
	if !s.Enabled() || info.Status != statusBMS {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.devices[strings.ToUpper(info.Address)]
	entry.Address = info.Address
	if info.Name != "" {
		entry.Name = info.Name
	}
	if info.Model != "" {
		entry.Model = info.Model
	}
	if info.Brand != "" {
		entry.Brand = info.Brand
	}
	if info.Protocol != "" {
		entry.Protocol = info.Protocol
	}
	entry.Status = "bms"
	if info.RSSI != 0 {
		entry.RSSI = info.RSSI
	}
	entry.SeenAt = time.Now().Format(time.RFC3339)
	s.devices[strings.ToUpper(info.Address)] = entry
	s.flushLocked()
}

// SaveLastSample keeps the newest reading of a battery so the API and Home
// Assistant can still show the last known state while the battery is asleep.
// The in-memory copy is always updated, the file at most every sampleFlush.
func (s *Store) SaveLastSample(address string, sample *Sample) {
	if !s.Enabled() || sample == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToUpper(address)
	entry, ok := s.devices[key]
	if !ok || entry.Status != "bms" {
		return
	}
	entry.LastSample = sample
	entry.SeenAt = sample.Timestamp.Format(time.RFC3339)
	s.devices[key] = entry
	if time.Since(s.lastFlush) >= sampleFlush {
		s.lastFlush = time.Now()
		s.flushLocked()
	}
}

// Batteries returns every identified battery together with its newest reading.
func (s *Store) Batteries() []storeDevice {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batteries := make([]storeDevice, 0, len(s.devices))
	for _, device := range s.devices {
		if device.Status == "bms" {
			batteries = append(batteries, device)
		}
	}
	sort.Slice(batteries, func(i, j int) bool {
		left := strings.ToLower(batteries[i].Name)
		right := strings.ToLower(batteries[j].Name)
		if left != right {
			return left < right
		}
		return batteries[i].Address < batteries[j].Address
	})
	return batteries
}

// SaveName updates the cached display name without touching the rest.
func (s *Store) SaveName(address, name string) {
	if !s.Enabled() || strings.TrimSpace(name) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToUpper(address)
	entry, ok := s.devices[key]
	if !ok || entry.Status != "bms" {
		return
	}
	if entry.Name == name {
		return
	}
	entry.Name = name
	s.devices[key] = entry
	s.flushLocked()
}

// SaveNotBMS caches a device that answered the probe but is not a battery, so
// later runs do not waste time on it again.
func (s *Store) SaveNotBMS(address, name string) {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToUpper(address)
	if entry, ok := s.devices[key]; ok && entry.Status == "bms" {
		return
	}
	s.devices[key] = storeDevice{
		Address: address,
		Name:    name,
		Status:  "notBMS",
		SeenAt:  time.Now().Format(time.RFC3339),
	}
	s.flushLocked()
}

// ForgetNotBMS drops cached rejections so a deep scan probes them again.
func (s *Store) ForgetNotBMS() {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.devices {
		if entry.Status == "notBMS" {
			delete(s.devices, key)
		}
	}
	s.flushLocked()
}

// AppendSample writes one monitoring sample to history/<address>.csv.
func (s *Store) AppendSample(address string, device DeviceInfo, sample *Sample) error {
	if !s.Enabled() || sample == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.history, historyFileName(address)+".csv")
	newFile := false
	if stat, err := os.Stat(path); err != nil || stat.Size() == 0 {
		newFile = true
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if newFile {
		if err := writer.Write([]string{
			"timestamp", "device", "model", "brand", "protocol",
			"voltage_v", "current_a", "soc_pct", "soh_pct",
			"remaining_ah", "design_ah", "cycles",
			"cell_min_mv", "cell_max_mv", "cell_delta_mv", "cells",
			"temp_min_c", "temp_max_c",
			"charging_mosfet", "discharging_mosfet", "balancer", "problem_code",
		}); err != nil {
			return err
		}
	}
	record := []string{
		sample.Timestamp.Format(time.RFC3339),
		device.Name,
		device.Model,
		device.Brand,
		sample.Protocol,
		formatFloat(sample.VoltageV, 3),
		formatFloat(sample.CurrentA, 3),
		strconv.Itoa(sample.SoC),
		formatOptionalInt(sample.SoH),
		formatFloat(sample.RemainingAh, 3),
		formatFloat(sample.DesignAh, 3),
		strconv.Itoa(sample.Cycles),
		formatOptionalInt(minCell(sample.CellsMv)),
		formatOptionalInt(maxCell(sample.CellsMv)),
		formatCellDelta(sample.CellsMv),
		strconv.Itoa(len(sample.CellsMv)),
		formatOptionalFloat(minTemp(sample.TemperaturesC), 1),
		formatOptionalFloat(maxTemp(sample.TemperaturesC), 1),
		formatOptionalBool(sample.ChargingMosfet),
		formatOptionalBool(sample.DischargingMosfet),
		formatOptionalBool(sample.Balancer),
		formatOptionalProblem(sample.ProblemCode),
	}
	if err := writer.Write(record); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

// SampleCount reports how many rows history/<address>.csv holds.
func (s *Store) SampleCount(address string) int {
	if !s.Enabled() {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(filepath.Join(s.history, historyFileName(address)+".csv"))
	if err != nil {
		return 0
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	if count > 0 {
		count-- // header row
	}
	return count
}

// powerTailBytes limits how much of a history file is parsed for the graph; the
// last window only ever needs the last few hundred rows.
const powerTailBytes = 256 << 10

// RecentPower reads the tail of history/<address>.csv and returns the readings
// that are newer than since, so the panel graph is filled right after a start.
func (s *Store) RecentPower(address string, since time.Time) []powerPoint {
	if !s.Enabled() || strings.TrimSpace(address) == "" {
		return nil
	}
	s.mu.Lock()
	file, err := os.Open(filepath.Join(s.history, historyFileName(address)+".csv"))
	s.mu.Unlock()
	if err != nil {
		return nil
	}
	defer file.Close()

	if stat, err := file.Stat(); err == nil && stat.Size() > powerTailBytes {
		if _, err := file.Seek(stat.Size()-powerTailBytes, io.SeekStart); err != nil {
			return nil
		}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil
	}
	text := string(data)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[index+1:] // the first line may be cut in half
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	points := make([]powerPoint, 0, 128)
	for {
		record, err := reader.Read()
		if err != nil {
			break
		}
		if len(record) < 7 { // timestamp, ..., voltage, current
			continue
		}
		stamp, err := time.Parse(time.RFC3339, record[0])
		if err != nil || stamp.Before(since) {
			continue
		}
		volts, err := strconv.ParseFloat(record[5], 64)
		if err != nil {
			continue
		}
		amps, err := strconv.ParseFloat(record[6], 64)
		if err != nil {
			continue
		}
		points = append(points, powerPoint{At: stamp.UnixMilli(), Watts: volts * amps})
	}
	return points
}

func (s *Store) flushLocked() {
	file := storeFile{
		Version: storeVersion,
		Updated: time.Now().Format(time.RFC3339),
	}
	for _, device := range s.devices {
		file.Devices = append(file.Devices, device)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, "devices.json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		log.Printf("cannot write %s: %v", temporary, err)
		return
	}
	if err := os.Rename(temporary, path); err != nil {
		log.Printf("cannot replace %s: %v", path, err)
	}
}

// historyFileName turns a Bluetooth address into a safe file name.
func historyFileName(address string) string {
	replaced := strings.Map(func(character rune) rune {
		switch character {
		case ':', '/', '\\', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return character
	}, address)
	if replaced == "" {
		return "unknown"
	}
	return replaced
}

func formatFloat(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

func formatOptionalInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func formatOptionalFloat(value *float64, digits int) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', digits, 64)
}

func formatOptionalBool(value *bool) string {
	if value == nil {
		return ""
	}
	if *value {
		return "on"
	}
	return "off"
}

func formatOptionalProblem(value *uint32) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("0x%08X", *value)
}

func formatCellDelta(cells []int) string {
	if len(cells) == 0 {
		return ""
	}
	minimum := cells[0]
	maximum := cells[0]
	for _, cell := range cells {
		if cell < minimum {
			minimum = cell
		}
		if cell > maximum {
			maximum = cell
		}
	}
	return strconv.Itoa(maximum - minimum)
}

func minCell(cells []int) *int {
	if len(cells) == 0 {
		return nil
	}
	value := cells[0]
	for _, cell := range cells[1:] {
		if cell < value {
			value = cell
		}
	}
	return &value
}

func maxCell(cells []int) *int {
	if len(cells) == 0 {
		return nil
	}
	value := cells[0]
	for _, cell := range cells[1:] {
		if cell > value {
			value = cell
		}
	}
	return &value
}

func minTemp(temps []float64) *float64 {
	if len(temps) == 0 {
		return nil
	}
	value := temps[0]
	for _, temp := range temps[1:] {
		if temp < value {
			value = temp
		}
	}
	return &value
}

func maxTemp(temps []float64) *float64 {
	if len(temps) == 0 {
		return nil
	}
	value := temps[0]
	for _, temp := range temps[1:] {
		if temp > value {
			value = temp
		}
	}
	return &value
}
