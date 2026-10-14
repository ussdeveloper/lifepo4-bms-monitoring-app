package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"

	"golang.org/x/sys/windows/registry"
)

// DeviceStatus describes how far the identification of a scanned device got.
type DeviceStatus string

const (
	statusKnown       DeviceStatus = "known" // remembered battery, not seen in this run yet
	statusCandidate   DeviceStatus = "candidate"
	statusProbing     DeviceStatus = "probing"
	statusBMS         DeviceStatus = "bms"
	statusNotBMS      DeviceStatus = "notBMS"
	statusUnavailable DeviceStatus = "unavailable"
)

// Probe priorities: batteries and remembered devices first, everything else
// only when the scan has nothing better to do.
const (
	priorityLow    = 1
	priorityMedium = 2
	priorityHigh   = 3

	// lowPriorityGrace lets a high priority device (the battery) cut in front
	// of generic devices that are waiting in the queue.
	lowPriorityGrace = 3 * time.Second
	onlineWindow     = 20 * time.Second

	// A device that did not answer is not probed again for a while, otherwise
	// the identification queue would never catch up.
	probeRetryDelay = 90 * time.Second

	// One identification must never block the queue for longer than this.
	probeDeadline = 12 * time.Second

	// Generic devices wait at the head of the queue only while a battery could
	// still show up.
	highPriorityWindow = 90 * time.Second

	// Unnamed devices without known services are probed once they have been
	// seen a few times, so every device ends up in a definite state.
	unnamedProbeHits = 3

	probeWorkers = 2

	// Upper bound of the device snapshot sent to the interface.
	maxDevicesInState = 48
)

// DeviceInfo is one device seen by the BLE scanner.
type DeviceInfo struct {
	Address  string       `json:"addr"`
	Name     string       `json:"name"`
	Brand    string       `json:"brand,omitempty"`
	RSSI     int          `json:"rssi"`
	Protocol string       `json:"protocol"`
	Status   DeviceStatus `json:"status"`
	Model    string       `json:"model"`
	Firmware string       `json:"firmware"`
	LastSeen time.Time    `json:"lastSeen"`
	Online   bool         `json:"online"`
	Cached   bool         `json:"cached,omitempty"`
	// Manual is true for an address added by hand in the interface.
	Manual bool `json:"manual,omitempty"`
	Hits   int  `json:"hits"`
}

type probeRequest struct {
	address  string
	priority int
}

// Hub runs the scan, the identification queue and the device list.
//
// Note: the WebView2 library initialises COM as STA on the main thread, so all
// BLE work (WinRT, MTA) runs on dedicated OS threads pinned with
// runtime.LockOSThread.
type Hub struct {
	mu          sync.Mutex
	adapter     *bluetooth.Adapter
	store       *Store
	devices     map[string]*DeviceInfo
	addresses   map[string]bluetooth.Address
	queued      map[string]int
	queue       []probeRequest
	headSince   time.Time
	lastProbe   map[string]time.Time
	signal      chan struct{}
	startedAt   time.Time
	batterySeen bool
	registry    map[string]string
	enabled     bool
	enableErr   string
	scanning    bool
	seenCount   int
	lastAdvert  time.Time
	restarts    int
	radios      []radioSummary
}

func NewHub(adapter *bluetooth.Adapter, store *Store) *Hub {
	hub := &Hub{
		adapter:    adapter,
		store:      store,
		devices:    map[string]*DeviceInfo{},
		addresses:  map[string]bluetooth.Address{},
		queued:     map[string]int{},
		signal:     make(chan struct{}, 1),
		lastProbe:  map[string]time.Time{},
		startedAt:  time.Now(),
		lastAdvert: time.Now(),
	}
	// Batteries from earlier runs are listed right away, even when they are
	// asleep at the moment.
	for _, saved := range store.Known() {
		hub.devices[saved.Address] = &DeviceInfo{
			Address:  saved.Address,
			Name:     saved.Name,
			Model:    saved.Model,
			Brand:    saved.Brand,
			Protocol: saved.Protocol,
			RSSI:     saved.RSSI,
			Status:   statusKnown,
			Cached:   true,
			Manual:   saved.Manual,
		}
		// Addresses added by hand are probed right away: the whole point is to
		// reach a battery that does not advertise.
		if saved.Manual {
			if parsed, normalised, err := parseDeviceAddress(saved.Address); err == nil {
				hub.addresses[normalised] = parsed
				hub.enqueueLocked(normalised, priorityHigh)
			}
		}
	}
	if count := len(hub.devices); count > 0 {
		log.Printf("restored %d known batteries from the data directory", count)
	}
	return hub
}

// AddManual lists an address the user typed in by hand. The device is shown on
// the battery list right away and probed with high priority, so a battery whose
// advertisement is missed (asleep, or connected to another application) can be
// reached as soon as it answers.
func (h *Hub) AddManual(address, name string) (string, error) {
	parsed, normalised, err := parseDeviceAddress(address)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)

	h.mu.Lock()
	info := h.devices[normalised]
	if info == nil {
		info = &DeviceInfo{Address: normalised, Status: statusKnown}
		h.devices[normalised] = info
	}
	info.Manual = true
	if name != "" {
		info.Name = name
	}
	// Keep a previously identified battery as it is, otherwise list the entry
	// like a remembered battery so it is visible before the first answer.
	if info.Status != statusBMS && info.Protocol == "" {
		info.Status = statusKnown
	}
	info.Cached = true
	h.addresses[normalised] = parsed
	h.enqueueLocked(normalised, priorityHigh)
	h.mu.Unlock()

	h.store.SaveWatched(normalised, name)
	log.Printf("%s: added by hand (%s)", normalised, displayName(name))
	return normalised, nil
}

// RemoveManual drops a hand added address from the list again.
func (h *Hub) RemoveManual(address string) error {
	_, normalised, err := parseDeviceAddress(address)
	if err != nil {
		return err
	}
	h.mu.Lock()
	if info, ok := h.devices[normalised]; ok && info.Manual {
		if info.Status == statusBMS {
			info.Manual = false
		} else {
			delete(h.devices, normalised)
			delete(h.addresses, normalised)
		}
	}
	h.mu.Unlock()
	h.store.RemoveWatched(normalised)
	return nil
}

// parseDeviceAddress accepts the usual spellings of a Bluetooth address
// (AA:BB:CC:DD:EE:FF, aa-bb-cc-dd-ee-ff or aabbccddeeff).
func parseDeviceAddress(address string) (bluetooth.Address, string, error) {
	cleaned := strings.ToUpper(strings.TrimSpace(address))
	cleaned = strings.NewReplacer("-", ":", " ", "", ".", ":").Replace(cleaned)
	if !strings.Contains(cleaned, ":") && len(cleaned) == 12 {
		parts := make([]string, 0, 6)
		for index := 0; index < 12; index += 2 {
			parts = append(parts, cleaned[index:index+2])
		}
		cleaned = strings.Join(parts, ":")
	}
	parsed, err := bluetooth.ParseMAC(cleaned)
	if err != nil {
		return bluetooth.Address{}, "", fmt.Errorf("that is not a Bluetooth address (use AA:BB:CC:DD:EE:FF)")
	}
	return bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: parsed}}, cleaned, nil
}

// radioRefresh is how often the radios are asked which batteries they know.
const radioRefresh = 60 * time.Second

// AdoptRadioDevices takes the devices the Bluetooth radios of this computer know
// (paired, remembered or connected) and adds the ones that look like a battery
// to the device list. A computer with two radios may hear the battery on the
// radio the BLE scanner does not use, and this is how such a battery still ends
// up monitored — Windows routes the connection over the radio that can see it.
func (h *Hub) AdoptRadioDevices(devices []radioDevice) int {
	adopted := 0
	for _, device := range devices {
		if !isRadioBatteryCandidate(device, h.store.IsKnownBattery) {
			continue
		}
		parsed, normalised, err := parseDeviceAddress(device.Address)
		if err != nil {
			continue
		}
		h.mu.Lock()
		info, exists := h.devices[normalised]
		if !exists {
			info = &DeviceInfo{Address: normalised, Status: statusKnown, Cached: true}
			h.devices[normalised] = info
			adopted++
		}
		if displayName(info.Name) == "" && device.Name != "" {
			info.Name = device.Name
		}
		// The address is known without an advertisement, so the session can
		// connect to it directly.
		h.addresses[normalised] = parsed
		if info.Status == statusKnown || info.Status == statusUnavailable {
			h.enqueueLocked(normalised, priorityHigh)
		}
		h.mu.Unlock()
	}
	return adopted
}

// radioLoop keeps the radio device list fresh: radios are cheap to enumerate and
// a battery paired on the other radio would otherwise stay invisible.
func (h *Hub) radioLoop(ctx context.Context) {
	report := func(first bool) {
		radios := enumerateRadios()
		if len(radios) == 0 {
			return
		}
		devices := make([]radioDevice, 0, 16)
		names := make([]string, 0, len(radios))
		for _, radio := range radios {
			names = append(names, radio.Name)
			devices = append(devices, radio.Devices...)
		}
		adopted := h.AdoptRadioDevices(devices)
		h.mu.Lock()
		h.radios = radios
		h.mu.Unlock()
		if first {
			log.Printf("%d Bluetooth radio(s): %s", len(radios), strings.Join(names, ", "))
		}
		if adopted > 0 {
			log.Printf("%d battery address(es) known to the radios were added to the list", adopted)
		}
	}

	report(true)
	ticker := time.NewTicker(radioRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report(false)
		}
	}
}

// Radios returns the radios found on this computer.
func (h *Hub) Radios() []radioSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]radioSummary, len(h.radios))
	copy(out, h.radios)
	return out
}

// Start launches the scanner, the name cache refresher and the identification
// workers.
func (h *Hub) Start(ctx context.Context) {
	go h.registryLoop(ctx)
	go h.radioLoop(ctx)
	go h.scanWorker(ctx)
	go h.scanWatchdog(ctx)
	for worker := 0; worker < probeWorkers; worker++ {
		go h.probeWorker(ctx)
	}
}

// StorageDir exposes the directory used for saved devices and history files.
func (h *Hub) StorageDir() string { return h.store.Dir() }

// scanWorker keeps the advertisement scan running. A flaky adapter or a Windows
// Bluetooth stack that went quiet is recovered by restarting the scan, which is
// what the watchdog triggers.
func (h *Hub) scanWorker(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := h.adapter.Enable(); err != nil {
		h.mu.Lock()
		h.enableErr = err.Error()
		h.mu.Unlock()
		log.Printf("Bluetooth: %v", err)
		return
	}
	h.mu.Lock()
	h.enabled = true
	h.mu.Unlock()

	for ctx.Err() == nil {
		h.mu.Lock()
		h.scanning = true
		h.lastAdvert = time.Now()
		h.mu.Unlock()

		err := h.adapter.Scan(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
			h.handleResult(result)
		})

		h.mu.Lock()
		h.scanning = false
		if err != nil && ctx.Err() == nil {
			h.enableErr = err.Error()
		}
		h.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			log.Printf("scan stopped with an error: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		// A scan that ended without a watchdog restart (adapter reset, radio
		// toggled) is started again, so the device list never goes stale.
		if !sleepCtx(ctx, 2*time.Second) {
			return
		}
	}
}

// scanWatchdog restarts the scan when the adapter stops delivering anything and
// reports how much the scanner sees, so an unreachable battery can be told
// apart from a broken scan.
func (h *Hub) scanWatchdog(ctx context.Context) {
	const idleRestart = 60 * time.Second
	ticker := time.NewTicker(10 * time.Second)
	stats := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer stats.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			silent := h.scanning && time.Since(h.lastAdvert) > idleRestart
			if silent {
				h.lastAdvert = time.Now()
				h.restarts++
			}
			h.mu.Unlock()
			if silent {
				log.Printf("no Bluetooth advertisements for %s, restarting the scan", idleRestart)
				if err := h.adapter.StopScan(); err != nil {
					log.Printf("cannot stop the scan: %v", err)
				}
			}
		case <-stats.C:
			h.mu.Lock()
			adverts := h.seenCount
			devices := len(h.devices)
			named := 0
			for _, info := range h.devices {
				if displayName(info.Name) != "" {
					named++
				}
			}
			identified := h.batterySeen
			h.mu.Unlock()
			if identified {
				continue
			}
			log.Printf("scan: %d advertisements, %d devices, %d with a name, no battery yet", adverts, devices, named)
		}
	}
}

func (h *Hub) handleResult(result bluetooth.ScanResult) {
	address := result.Address.String()
	name := strings.TrimSpace(result.LocalName())

	h.mu.Lock()
	newDevice := h.devices[address] == nil
	cachedName, hasCachedName := h.registry[strings.ToUpper(address)]
	info := h.mergeSeenLocked(address, name, cachedName, hasCachedName, int(result.RSSI))
	h.addresses[address] = result.Address
	h.seenCount++
	h.lastAdvert = time.Now()
	h.onSeenLocked(info, result)
	display := displayName(info.Name)
	h.mu.Unlock()

	if newDevice {
		if display == "" {
			display = "(no name advertised)"
		}
		log.Printf("new device %s: %s, RSSI %d dBm", address, display, result.RSSI)
	}
}

// mergeSeenLocked updates an existing entry or creates a new one.
func (h *Hub) mergeSeenLocked(address, name, cachedName string, hasCachedName bool, rssi int) *DeviceInfo {
	info := h.devices[address]
	if info == nil {
		status := statusCandidate
		if h.store.IsNotBMS(address) {
			// Rejected in an earlier run: do not spend time on it again.
			status = statusNotBMS
		}
		info = &DeviceInfo{Address: address, Status: status}
		h.devices[address] = info
	}
	if name != "" {
		info.Name = name
	} else if info.Name == "" && hasCachedName {
		info.Name = cachedName
	}
	info.RSSI = rssi
	info.LastSeen = time.Now()
	info.Online = true
	info.Hits++
	return info
}

// onSeenLocked decides whether a freshly seen device needs identification.
func (h *Hub) onSeenLocked(info *DeviceInfo, result bluetooth.ScanResult) {
	switch info.Status {
	case statusBMS, statusProbing, statusNotBMS:
		// Already identified, being identified, or rejected during this run.
		// Only a deep scan revisits them.
		return
	case statusUnavailable:
		// Wait before knocking on the same closed door again.
		if time.Since(h.lastProbe[info.Address]) < probeRetryDelay {
			return
		}
		info.Status = statusCandidate
	}
	if info.Cached && info.Protocol != "" {
		// Identified before, protocol is known: no probe needed.
		info.Cached = false
		info.Status = statusBMS
		h.rememberLocked(info)
		return
	}
	priority := classifyDevice(result, info)
	if priority == 0 {
		return
	}
	h.enqueueLocked(info.Address, priority)
}

// serviceAdvertiser reports which services a device announced while scanning.
// It is an interface so the classification rules can be unit tested.
type serviceAdvertiser interface {
	HasServiceUUID(uuid bluetooth.UUID) bool
}

// classifyDevice maps a scan result to a probe priority (0 = do not probe).
func classifyDevice(result serviceAdvertiser, info *DeviceInfo) int {
	if info.Cached {
		return priorityHigh
	}
	if info.Manual {
		return priorityHigh
	}
	if isBatteryName(info.Name) {
		return priorityHigh
	}
	if result.HasServiceUUID(uuidServiceHS04) || result.HasServiceUUID(uuidServiceJBD) {
		return priorityHigh
	}
	if result.HasServiceUUID(uuidServiceHS03) {
		return priorityMedium
	}
	if strings.TrimSpace(info.Name) != "" || info.Hits >= unnamedProbeHits {
		// A named device, or a silent one that keeps showing up.
		return priorityLow
	}
	return 0
}

// enqueueLocked adds a device to the priority queue, keeping it sorted.
func (h *Hub) enqueueLocked(address string, priority int) {
	if current, ok := h.queued[address]; ok {
		if current >= priority {
			return
		}
		for index := range h.queue {
			if h.queue[index].address == address {
				h.queue = append(h.queue[:index], h.queue[index+1:]...)
				break
			}
		}
	}
	previousHead := ""
	if len(h.queue) > 0 {
		previousHead = h.queue[0].address
	}
	h.queued[address] = priority
	h.queue = append(h.queue, probeRequest{address: address, priority: priority})
	sort.SliceStable(h.queue, func(i, j int) bool {
		return h.queue[i].priority > h.queue[j].priority
	})
	if len(h.queue) > 0 && h.queue[0].address != previousHead {
		h.headSince = time.Now()
	}
	select {
	case h.signal <- struct{}{}:
	default:
	}
}

// manualProbeDelay is how often an address added by hand is probed again while
// it stays silent. A battery that is awake answers within seconds, so a slow
// retry is enough and keeps the queue free.
const manualProbeDelay = 45 * time.Second

// retryManual probes the addresses added by hand again. They may never show up
// in a scan (asleep, or held by another application), which is exactly why the
// user typed them in.
func (h *Hub) retryManual() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for address, info := range h.devices {
		if !info.Manual || info.Status == statusProbing {
			continue
		}
		if info.Status == statusBMS && info.Online {
			continue
		}
		if time.Since(h.lastProbe[address]) < manualProbeDelay {
			continue
		}
		h.enqueueLocked(address, priorityHigh)
	}
}

// registryLoop keeps device names up to date from the Windows cache and retries
// the addresses the user added by hand.
func (h *Hub) registryLoop(ctx context.Context) {
	h.mu.Lock()
	h.registry = loadRegistryNames()
	h.mu.Unlock()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.retryManual()
			names := loadRegistryNames()
			h.mu.Lock()
			for address, name := range names {
				if info, ok := h.devices[address]; ok && info.Name == "" {
					info.Name = name
				}
			}
			h.registry = names
			h.mu.Unlock()
		}
	}
}

func (h *Hub) probeWorker(ctx context.Context) {
	for {
		request, ok := h.nextProbe(ctx)
		if !ok {
			return
		}
		h.probeSafely(ctx, request.address)
		h.mu.Lock()
		delete(h.queued, request.address)
		h.lastProbe[request.address] = time.Now()
		h.mu.Unlock()
	}
}

// nextProbe returns the most promising device to identify. Generic devices wait
// briefly so that a battery discovered a moment later is probed first — but
// only while no battery has answered yet.
func (h *Hub) nextProbe(ctx context.Context) (probeRequest, bool) {
	for {
		h.mu.Lock()
		if len(h.queue) > 0 {
			head := h.queue[0]
			if head.priority >= priorityHigh || !h.graceNeededLocked() || time.Since(h.headSince) >= lowPriorityGrace {
				h.queue = h.queue[1:]
				h.headSince = time.Now()
				h.mu.Unlock()
				return head, true
			}
		}
		h.mu.Unlock()

		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return probeRequest{}, false
		case <-h.signal:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// graceNeededLocked reports whether generic devices should still make room for
// a battery that might appear any second.
func (h *Hub) graceNeededLocked() bool {
	if h.batterySeen {
		return false
	}
	return time.Since(h.startedAt) < highPriorityWindow
}

// probeSafely runs one identification with a hard deadline. A stuck BLE
// operation must never block the whole queue, which is why the probe runs in a
// separate pinned thread and the worker moves on after probeDeadline.
func (h *Hub) probeSafely(ctx context.Context, address string) {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("BLE panic while probing %s: %v", address, recovered)
				h.mu.Lock()
				if info, ok := h.devices[address]; ok {
					info.Status = statusUnavailable
				}
				h.mu.Unlock()
			}
		}()
		h.probe(ctx, address)
	}()

	timer := time.NewTimer(probeDeadline)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
	case <-timer.C:
		log.Printf("%s: identification did not finish within %s, moving on", address, probeDeadline)
		h.mu.Lock()
		if info, ok := h.devices[address]; ok {
			info.Status = statusUnavailable
		}
		h.mu.Unlock()
	}
}

// probe connects to a device and checks whether it speaks a supported protocol.
func (h *Hub) probe(ctx context.Context, address string) {
	h.mu.Lock()
	info, ok := h.devices[address]
	btAddress, hasAddress := h.addresses[address]
	if !ok || !hasAddress {
		h.mu.Unlock()
		return
	}
	info.Status = statusProbing
	h.mu.Unlock()

	result, err := identifyDevice(h.adapter, btAddress, displayName(info.Name))
	if ctx.Err() != nil {
		return
	}

	h.mu.Lock()
	info = h.devices[address]
	if info == nil {
		h.mu.Unlock()
		return
	}
	if err != nil {
		log.Printf("%s: probe failed: %v", address, err)
		info.Status = statusUnavailable
		h.mu.Unlock()
		return
	}
	if result.Protocol == "" {
		info.Status = statusNotBMS
		info.Cached = false
		// The GATT name is the only name many BMS advertise nowhere else, so it
		// is kept for the device list and for the next run.
		if discovered := displayName(result.Name); discovered != "" && displayName(info.Name) == "" {
			info.Name = discovered
		}
		name := info.Name
		h.mu.Unlock()
		h.store.SaveNotBMS(address, name)
		return
	}
	info.Protocol = result.Protocol
	info.Brand = result.Brand
	info.Status = statusBMS
	info.Cached = false
	h.batterySeen = true
	if result.Model != "" {
		info.Model = result.Model
	}
	if result.Name != "" {
		info.Name = result.Name
	}
	snapshot := *info
	h.mu.Unlock()
	h.remember(snapshot)
}

// remember stores an identified battery for the next run.
func (h *Hub) remember(info DeviceInfo) {
	h.store.SaveBattery(info)
}

func (h *Hub) rememberLocked(info *DeviceInfo) {
	h.store.SaveBattery(*info)
}

// DeepScan queues every visible device again, including cached rejections.
func (h *Hub) DeepScan() {
	h.store.ForgetNotBMS()
	h.mu.Lock()
	defer h.mu.Unlock()
	for address, info := range h.devices {
		if info.Status == statusBMS || info.Status == statusProbing {
			continue
		}
		info.Status = statusCandidate
		info.Cached = false
		info.Hits = 0
		delete(h.lastProbe, address)
		h.enqueueLocked(address, priorityHigh)
	}
}

// HubState is the JSON snapshot of the scanner.
type HubState struct {
	Scanning     bool         `json:"scanning"`
	Enabled      bool         `json:"enabled"`
	EnableError  string       `json:"enableError,omitempty"`
	Seen         int          `json:"seen"`
	Adverts      int          `json:"adverts"`
	Named        int          `json:"named"`
	ScanRestarts int          `json:"scanRestarts"`
	BatteriesN   int          `json:"batteriesCount"`
	Identified   int          `json:"identified"`
	Rejected     int          `json:"rejected"`
	Pending      int          `json:"pending"`
	Storage      string       `json:"storage"`
	Batteries    []DeviceInfo `json:"batteries"`
	Checking     []DeviceInfo `json:"checking"`
	Devices      []DeviceInfo `json:"devices"`
	// Manual lists the addresses the user added by hand.
	Manual []string `json:"manual"`
}

func (h *Hub) State() HubState {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := HubState{
		Scanning:     h.scanning,
		Enabled:      h.enabled,
		EnableError:  h.enableErr,
		Seen:         len(h.devices),
		Adverts:      h.seenCount,
		ScanRestarts: h.restarts,
		Storage:      h.store.Dir(),
	}
	all := make([]DeviceInfo, 0, len(h.devices))
	for _, info := range h.devices {
		info.Online = !info.LastSeen.IsZero() && time.Since(info.LastSeen) < onlineWindow
		if displayName(info.Name) != "" {
			state.Named++
		}
		// The device list reports what the current scan actually sees, not the
		// batteries restored from disk. An address added by hand is listed as a
		// battery instead, even before it ever advertises.
		seen := !info.LastSeen.IsZero()
		if seen {
			all = append(all, *info)
		} else if !info.Manual {
			continue
		}
		switch info.Status {
		case statusBMS, statusKnown:
			state.Batteries = append(state.Batteries, *info)
			state.Identified++
		case statusCandidate, statusProbing:
			state.Checking = append(state.Checking, *info)
			state.Pending++
		case statusNotBMS:
			// An address added by hand stays on the battery list even when it
			// turns out not to be a battery: the user put it there and can
			// remove it in the same dialog.
			if info.Manual {
				state.Batteries = append(state.Batteries, *info)
				break
			}
			state.Rejected++
		case statusUnavailable:
			if info.Manual {
				state.Batteries = append(state.Batteries, *info)
				break
			}
			state.Rejected++
		}
	}
	state.BatteriesN = len(state.Batteries)
	// Alphabetical order keeps the list stable — sorting by signal strength
	// makes rows jump around while scanning.
	sort.Slice(state.Batteries, func(i, j int) bool {
		return lessByDisplayName(&state.Batteries[i], &state.Batteries[j])
	})
	sort.Slice(state.Checking, func(i, j int) bool {
		return lessByDisplayName(&state.Checking[i], &state.Checking[j])
	})
	sort.Slice(all, func(i, j int) bool {
		return lessByDisplayName(&all[i], &all[j])
	})
	if len(all) > maxDevicesInState {
		all = all[:maxDevicesInState]
	}
	state.Devices = all
	for _, info := range h.devices {
		if info.Manual {
			state.Manual = append(state.Manual, info.Address)
		}
	}
	sort.Strings(state.Manual)
	return state
}

// lessByDisplayName orders devices by name and, when the names are equal or
// missing, by address.
func lessByDisplayName(a, b *DeviceInfo) bool {
	left := strings.ToLower(strings.TrimSpace(a.Name))
	right := strings.ToLower(strings.TrimSpace(b.Name))
	if left != right {
		return left < right
	}
	return a.Address < b.Address
}

// BestBattery returns the battery that should be monitored automatically:
// an online battery whose protocol is already known, otherwise the first
// remembered battery.
func (h *Hub) BestBattery() (DeviceInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	best := DeviceInfo{}
	found := false
	for _, info := range h.devices {
		switch info.Status {
		case statusBMS, statusKnown:
		default:
			continue
		}
		if !info.Online {
			continue
		}
		if info.Protocol == "" {
			continue
		}
		if !found || lessByDisplayName(info, &best) {
			best = *info
			found = true
		}
	}
	if found {
		return best, true
	}
	// Nothing in range: fall back to a remembered battery or one that was added
	// by hand, so the session keeps trying to reach it.
	for _, info := range h.devices {
		if info.Status != statusKnown && !info.Manual {
			continue
		}
		if info.Status == statusNotBMS {
			continue
		}
		if !found || lessByDisplayName(info, &best) {
			best = *info
			found = true
		}
	}
	return best, found
}

func (h *Hub) ProtocolOf(address string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if info, ok := h.devices[address]; ok {
		return info.Protocol
	}
	return ""
}

func (h *Hub) Device(address string) (DeviceInfo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if info, ok := h.devices[address]; ok {
		return *info, true
	}
	return DeviceInfo{}, false
}

func (h *Hub) AddressOf(address string) (bluetooth.Address, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	value, ok := h.addresses[address]
	return value, ok
}

// Touch marks a device as seen right now. The session calls it for every
// reading, so a battery that is being monitored always counts as online.
func (h *Hub) Touch(address string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if info, ok := h.devices[address]; ok {
		info.LastSeen = time.Now()
		info.Online = true
	}
}

func (h *Hub) SetDeviceMeta(address, name, model, firmware string) {
	h.mu.Lock()
	info, ok := h.devices[address]
	if !ok {
		h.mu.Unlock()
		return
	}
	if name != "" {
		info.Name = name
	}
	if model != "" {
		info.Model = model
	}
	if firmware != "" {
		info.Firmware = firmware
	}
	snapshot := *info
	h.mu.Unlock()
	if name != "" {
		h.store.SaveName(address, name)
	}
	if snapshot.Status == statusBMS && (model != "" || firmware != "") {
		h.store.SaveBattery(snapshot)
	}
}

// --- names remembered by Windows (BTHPORT registry key) ---

func loadRegistryNames() map[string]string {
	names := map[string]string{}
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\BTHPORT\Parameters\Devices`,
		registry.READ,
	)
	if err != nil {
		return names
	}
	defer key.Close()
	subkeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return names
	}
	for _, subkey := range subkeys {
		address := formatRegistryAddress(subkey)
		if address == "" {
			continue
		}
		deviceKey, err := registry.OpenKey(key, subkey, registry.READ)
		if err != nil {
			continue
		}
		if value, _, err := deviceKey.GetStringValue("Name"); err == nil && strings.TrimSpace(value) != "" {
			names[address] = strings.TrimSpace(value)
			deviceKey.Close()
			continue
		}
		if data, _, err := deviceKey.GetBinaryValue("Name"); err == nil {
			text := strings.TrimSpace(strings.TrimRight(string(data), "\x00"))
			if text != "" {
				names[address] = text
			}
		}
		deviceKey.Close()
	}
	return names
}

func formatRegistryAddress(raw string) string {
	digits := make([]byte, 0, 12)
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		if isHexDigit(character) {
			digits = append(digits, character)
		}
	}
	if len(digits) != 12 {
		return ""
	}
	parts := make([]string, 0, 6)
	for index := 0; index < 12; index += 2 {
		parts = append(parts, strings.ToUpper(string(digits[index:index+2])))
	}
	return strings.Join(parts, ":")
}

func isHexDigit(character byte) bool {
	return (character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'f') ||
		(character >= 'A' && character <= 'F')
}
