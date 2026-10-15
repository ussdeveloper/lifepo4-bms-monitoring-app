package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// Session owns the connection to the currently selected BMS and reads its data
// in a loop (5 seconds by default). While it runs, every sample is appended to
// the history file in the user data directory and forwarded to MQTT.
type Session struct {
	hub       *Hub
	store     *Store
	settings  *Settings
	publisher *mqttPublisher

	mu       sync.Mutex
	address  string
	status   string
	errMsg   string
	errCode  string
	brand    string
	model    string
	firmware string
	protocol string
	samples  int
	attempt  int
	manual   bool
	sample   *Sample
	updated  time.Time
	cancel   context.CancelFunc
	power    *powerTrack

	// connectedAt marks when the current connection was established and is
	// cleared when it ends; it tells a lost working link apart from a battery
	// that was never reached.
	connectedAt time.Time
	// takeover is set when a working link was taken away by another
	// application; monitoring keeps retrying while the interface says so.
	takeover bool
	// pauseUntil holds monitoring back: the battery is deliberately left free so
	// that another device (a phone, a second computer) can connect to it.
	pauseUntil time.Time
}

// Connection tuning: the battery answers in a few seconds when it is awake, so
// a failed attempt is retried quickly instead of waiting for the next interval.
const (
	connectTimeout  = 5 * time.Second
	fastRetryDelay  = 2 * time.Second
	slowRetryDelay  = 12 * time.Second
	maxFastAttempts = 6
	autoSelectEvery = 4 * time.Second
)

const (
	sessionIdle       = "idle"
	sessionConnecting = "connecting"
	sessionConnected  = "connected"
	sessionError      = "error"
)

// Error codes are translated by the interface; the error text carries the raw
// technical detail from the BLE stack.
const (
	errCodeNoDevice          = "noDevice"
	errCodeConnect           = "connect"
	errCodeNotify            = "notify"
	errCodeAuth              = "auth"
	errCodeProtocol          = "protocol"
	errCodeUnsupportedDevice = "unsupportedDevice"
	errCodeBLE               = "ble"
)

const protocolTimeout = 10 * time.Second

func NewSession(hub *Hub, store *Store, settings *Settings, publisher *mqttPublisher) *Session {
	return &Session{
		hub:       hub,
		store:     store,
		settings:  settings,
		publisher: publisher,
		status:    sessionIdle,
		power:     newPowerTrack(),
	}
}

// interval is read for every wait so a change in the settings applies at once.
func (s *Session) interval() time.Duration {
	if s.settings == nil {
		return defaultIntervalS * time.Second
	}
	value := s.settings.Interval()
	if value < time.Second {
		return time.Second
	}
	return value
}

// Release lets the battery go for the given time: the connection is closed and
// monitoring stays away, because a BMS stops advertising while it is connected —
// without this, no other device (a phone, a second computer) could find it.
func (s *Session) Release(within time.Duration) {
	if within <= 0 {
		within = 5 * time.Minute
	}
	s.mu.Lock()
	s.pauseUntil = time.Now().Add(within)
	cancel := s.cancel
	s.cancel = nil
	s.address = ""
	s.status = sessionIdle
	s.errMsg = ""
	s.errCode = ""
	s.errCode = ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	log.Printf("battery released for %s: another device can connect now", within)
}

// ReleaseLeft reports how long the battery stays released (zero when monitoring
// is active).
func (s *Session) ReleaseLeft() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pauseUntil.IsZero() {
		return 0
	}
	left := time.Until(s.pauseUntil)
	if left <= 0 {
		s.pauseUntil = time.Time{}
		return 0
	}
	return left
}

func (s *Session) pauseActive() bool {
	return s.ReleaseLeft() > 0
}

// Select switches the session to another device (empty address stops it). The
// manual flag marks a choice made by the user, which disables automatic
// monitoring of that address.
func (s *Session) Select(address string, manual bool) {
	s.mu.Lock()
	s.pauseUntil = time.Time{}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.address = address
	s.manual = manual && address != ""
	s.attempt = 0
	s.sample = nil
	s.errMsg = ""
	s.errCode = ""
	s.brand = ""
	s.model = ""
	s.firmware = ""
	s.protocol = ""
	s.samples = 0
	s.updated = time.Time{}
	s.takeover = false
	if address == "" {
		s.status = sessionIdle
		s.manual = false
		s.mu.Unlock()
		return
	}
	s.status = sessionConnecting
	if info, ok := s.hub.Device(address); ok {
		s.protocol = info.Protocol
		s.brand = info.Brand
		s.model = info.Model
		s.firmware = info.Firmware
	}
	if s.store != nil {
		s.samples = s.store.SampleCount(address)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	// Fill the power graph with what was recorded in an earlier run.
	s.power.Seed(address, s.store.RecentPower(address, time.Now().Add(-powerWindow)), time.Now())

	go s.run(ctx, address)
}

// StartAutoSelect keeps the best available battery monitored without any user
// interaction: as soon as a battery shows up in the list (or a remembered one
// can be reached again) the session connects to it.
func (s *Session) StartAutoSelect(ctx context.Context) {
	ticker := time.NewTicker(autoSelectEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.autoSelect()
		}
	}
}

func (s *Session) autoSelect() {
	if s.pauseActive() {
		// The battery was released on purpose: another device is using it.
		return
	}
	battery, ok := s.hub.BestBattery()
	if !ok {
		return
	}

	s.mu.Lock()
	current := s.address
	manual := s.manual
	s.mu.Unlock()

	if current == battery.Address {
		return
	}
	if manual && current != "" {
		// Keep the user's choice while that battery is still reachable.
		if info, found := s.hub.Device(current); found && info.Online {
			return
		}
	}
	log.Printf("monitoring %s automatically", battery.Address)
	s.Select(battery.Address, false)
}

func (s *Session) run(ctx context.Context, address string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	attempt := 0
	// everConnected remembers that this run once had a working link. A later
	// failure then means the link was taken away (a phone or a second computer
	// connected to the same battery) instead of the battery simply being asleep.
	everConnected := false
	failures := 0
	for ctx.Err() == nil {
		attempt++
		s.setAttempt(attempt)
		btAddress, ok := s.hub.AddressOf(address)
		if !ok {
			// The device is not part of the scan results yet: retry quickly so
			// the session picks it up as soon as it advertises again.
			s.fail(ctx, errCodeNoDevice, "device is not part of the scan results")
			s.noteTakeover(everConnected, failures+1)
			failures++
			if !sleepCtx(ctx, s.retryDelay(attempt)) {
				return
			}
			continue
		}
		device, err := s.hub.adapter.Connect(btAddress, bluetooth.ConnectionParams{
			ConnectionTimeout: bluetooth.NewDuration(connectTimeout),
		})
		if err != nil {
			s.fail(ctx, errCodeConnect, "connection failed: "+err.Error())
			s.noteTakeover(everConnected, failures+1)
			failures++
			if !sleepCtx(ctx, s.retryDelay(attempt)) {
				return
			}
			continue
		}
		attempt = 0
		failures = 0
		s.setAttempt(0)
		s.markConnected()
		everConnected = true
		s.runConnected(ctx, address, device)
		s.markDisconnected()
		device.Disconnect()
		if ctx.Err() != nil {
			return
		}
		if !sleepCtx(ctx, s.interval()) {
			return
		}
	}
}

// takeoverAttempts is how many reconnection attempts are made before a lost
// link is explained as taken over. A short burst covers a Bluetooth hiccup.
const takeoverAttempts = 3

// noteTakeover marks that a working link was taken away from this application —
// in practice a phone application or another computer connected to the same
// battery, which makes the BMS drop the older connection. Monitoring keeps
// trying to take the battery back (exactly like the other application does)
// instead of giving up, and the interface explains what is happening meanwhile.
func (s *Session) noteTakeover(everConnected bool, failures int) {
	if !everConnected || failures < takeoverAttempts {
		return
	}
	s.mu.Lock()
	first := !s.takeover
	s.takeover = true
	address := s.address
	s.mu.Unlock()
	if first {
		log.Printf("%s: the link was taken away, trying to take the battery back", address)
	}
}

// Takeover reports whether the link was taken by another application.
func (s *Session) Takeover() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeover
}

// retryDelay shortens the wait after a failed connection: the first attempts
// are repeated quickly (a sleeping BMS answers as soon as it wakes up), later
// ones back off to avoid hammering the adapter.
func (s *Session) retryDelay(attempt int) time.Duration {
	if attempt <= maxFastAttempts {
		return fastRetryDelay
	}
	return slowRetryDelay
}

func (s *Session) setAttempt(attempt int) {
	s.mu.Lock()
	s.attempt = attempt
	s.mu.Unlock()
}

func (s *Session) markConnected() {
	s.mu.Lock()
	s.connectedAt = time.Now()
	s.takeover = false
	s.mu.Unlock()
}

func (s *Session) markDisconnected() {
	s.mu.Lock()
	s.connectedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Session) runConnected(ctx context.Context, address string, device bluetooth.Device) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.fail(ctx, errCodeBLE, fmt.Sprintf("BLE library error: %v", recovered))
		}
	}()

	protocol := s.protocol
	if protocol == "" {
		protocol = s.hub.ProtocolOf(address)
	}

	notifications := make(chan []byte, 64)
	switch protocol {
	case "HS04":
		notifyChar, writeChar, authChar, err := hs04Characteristics(device)
		if err != nil {
			s.fail(ctx, errCodeProtocol, err.Error())
			return
		}
		if err := notifyChar.EnableNotifications(func(data []byte) {
			pushNotification(notifications, data)
		}); err != nil {
			s.fail(ctx, errCodeNotify, "could not enable notifications: "+err.Error())
			return
		}
		if _, err := authChar.Write([]byte("HiLink")); err != nil {
			s.fail(ctx, errCodeAuth, "HiLink authentication failed: "+err.Error())
			return
		}
		if !sleepCtx(ctx, 500*time.Millisecond) {
			return
		}
		authBuffer := make([]byte, 4)
		count := 0
		readErr := safeBLE(func() error {
			var err error
			count, err = authChar.Read(authBuffer)
			return err
		})
		if readErr != nil || count < 1 || authBuffer[0] != 0x01 {
			s.fail(ctx, errCodeAuth, "the BMS rejected the HiLink authentication")
			return
		}
		if payload, err := exchangeHS04(notifications, writeChar, productPoint, false); err == nil {
			s.setMeta("", decodeHS04Firmware(payload))
		}
		s.setProtocol("HS04")
		s.setConnected()
		for ctx.Err() == nil {
			payload, err := exchangeHS04(notifications, writeChar, analogPoint, true)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			sample, err := decodeHS04Snapshot(payload)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			s.setSample(sample)
			if !sleepCtx(ctx, s.interval()) {
				return
			}
		}
	case "HS03":
		writeChar, notifyChar, err := hs03Characteristics(device)
		if err != nil {
			s.fail(ctx, errCodeProtocol, err.Error())
			return
		}
		if err := notifyChar.EnableNotifications(func(data []byte) {
			pushNotification(notifications, data)
		}); err != nil {
			s.fail(ctx, errCodeNotify, "could not enable notifications: "+err.Error())
			return
		}
		if _, err := exchangeHS03(notifications, writeChar, hs03InitCommand); err != nil {
			s.fail(ctx, errCodeProtocol, err.Error())
			return
		}
		if payload, err := exchangeHS03(notifications, writeChar, hs03ModelCommand); err == nil {
			s.setMeta(decodeHS03Model(payload), "")
		}
		s.setProtocol("HS03")
		s.setConnected()
		for ctx.Err() == nil {
			analog, err := exchangeHS03(notifications, writeChar, hs03AnalogCommand)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			cells, err := exchangeHS03(notifications, writeChar, hs03CellsCommand)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			status, err := exchangeHS03(notifications, writeChar, hs03StatusCommand)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			sample, err := decodeHS03Snapshot(analog, status, cells)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			s.setSample(sample)
			if !sleepCtx(ctx, s.interval()) {
				return
			}
		}
	case "JBD":
		writeChar, notifyChar, err := jbdCharacteristics(device)
		if err != nil {
			s.fail(ctx, errCodeProtocol, err.Error())
			return
		}
		if err := notifyChar.EnableNotifications(func(data []byte) {
			pushNotification(notifications, data)
		}); err != nil {
			s.fail(ctx, errCodeNotify, "could not enable notifications: "+err.Error())
			return
		}
		if payload, err := exchangeJBD(notifications, writeChar, jbdRegisterName, protocolTimeout); err == nil {
			s.setMeta(decodeJBDName(payload), "")
		}
		s.setProtocol("JBD")
		s.setConnected()
		for ctx.Err() == nil {
			basic, err := exchangeJBD(notifications, writeChar, jbdRegisterBasic, protocolTimeout)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			cells, err := exchangeJBD(notifications, writeChar, jbdRegisterCells, protocolTimeout)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			sample, err := decodeJBDSnapshot(basic, cells)
			if err != nil {
				s.fail(ctx, errCodeProtocol, err.Error())
				return
			}
			s.setSample(sample)
			if !sleepCtx(ctx, s.interval()) {
				return
			}
		}
	default:
		s.fail(ctx, errCodeUnsupportedDevice, "the device does not use a supported protocol (HS03/HS04/JBD)")
	}
}

func protocolLabel(protocol string) string {
	switch protocol {
	case "HS03":
		return "HS03/HiLink"
	case "HS04":
		return "HS04/WATT"
	case "JBD":
		return "JBD/Xiaoxiang"
	}
	return protocol
}
func hs04Characteristics(device bluetooth.Device) (notify, write, auth bluetooth.DeviceCharacteristic, err error) {
	services, err := device.DiscoverServices([]bluetooth.UUID{uuidServiceHS04})
	if err != nil || len(services) == 0 {
		return notify, write, auth, errors.New("the device does not expose the FFF0 service")
	}
	chars, err := services[0].DiscoverCharacteristics(
		[]bluetooth.UUID{uuidNotifyHS04, uuidWriteHS04, uuidAuthHS04},
	)
	if err != nil {
		return notify, write, auth, err
	}
	hasNotify, hasWrite, hasAuth := false, false, false
	for _, characteristic := range chars {
		switch characteristic.UUID() {
		case uuidNotifyHS04:
			notify, hasNotify = characteristic, true
		case uuidWriteHS04:
			write, hasWrite = characteristic, true
		case uuidAuthHS04:
			auth, hasAuth = characteristic, true
		}
	}
	if !hasNotify || !hasWrite || !hasAuth {
		return notify, write, auth, errors.New("required HS04 characteristics are missing")
	}
	return notify, write, auth, nil
}

func hs03Characteristics(device bluetooth.Device) (write, notify bluetooth.DeviceCharacteristic, err error) {
	services, err := device.DiscoverServices([]bluetooth.UUID{uuidServiceHS03})
	if err != nil || len(services) == 0 {
		return write, notify, errors.New("the device does not expose the 0001 service")
	}
	chars, err := services[0].DiscoverCharacteristics(
		[]bluetooth.UUID{uuidWriteHS03, uuidNotifyHS03},
	)
	if err != nil {
		return write, notify, err
	}
	hasWrite, hasNotify := false, false
	for _, characteristic := range chars {
		switch characteristic.UUID() {
		case uuidWriteHS03:
			write, hasWrite = characteristic, true
		case uuidNotifyHS03:
			notify, hasNotify = characteristic, true
		}
	}
	if !hasWrite || !hasNotify {
		return write, notify, errors.New("required HS03 characteristics are missing")
	}
	return write, notify, nil
}

func pushNotification(channel chan []byte, data []byte) {
	copied := make([]byte, len(data))
	copy(copied, data)
	select {
	case channel <- copied:
	default:
	}
}

func drainNotifications(channel chan []byte) {
	for {
		select {
		case <-channel:
		default:
			return
		}
	}
}

func exchangeHS04(channel chan []byte, write bluetooth.DeviceCharacteristic, point byte, extended bool) ([]byte, error) {
	drainNotifications(channel)
	writeErr := safeBLE(func() error {
		_, err := write.Write(buildHS04Request(point, extended))
		return err
	})
	if writeErr != nil {
		return nil, fmt.Errorf("writing to the BMS failed: %w", writeErr)
	}
	data := make([]byte, 0, 64)
	deadline := time.Now().Add(protocolTimeout)
	for time.Now().Before(deadline) {
		chunk, ok := waitChunk(channel, time.Until(deadline))
		if !ok {
			break
		}
		data = append(data, chunk...)
		if len(data) > maxFrameSize {
			return nil, errors.New("the BMS response exceeds the allowed size")
		}
		if len(data) >= 8 {
			total := int(binary.BigEndian.Uint16(data[6:8])) + 11
			if len(data) >= total {
				if len(data) != total {
					return nil, errors.New("received extra bytes in the HS04 response")
				}
				return validateHS04Response(data, point)
			}
		}
	}
	return nil, errors.New("timed out waiting for the BMS response")
}

func exchangeHS03(channel chan []byte, write bluetooth.DeviceCharacteristic, command byte) ([]byte, error) {
	drainNotifications(channel)
	writeErr := safeBLE(func() error {
		_, err := write.Write(buildHS03Command(command))
		return err
	})
	if writeErr != nil {
		return nil, fmt.Errorf("writing to the BMS failed: %w", writeErr)
	}
	data := make([]byte, 0, 64)
	deadline := time.Now().Add(protocolTimeout)
	for time.Now().Before(deadline) {
		chunk, ok := waitChunk(channel, time.Until(deadline))
		if !ok {
			break
		}
		data = append(data, chunk...)
		if len(data) > maxFrameSize {
			return nil, errors.New("the BMS response exceeds the allowed size")
		}
		if len(data) >= 3 {
			total := int(data[2]) + hs03MinFrame
			if len(data) >= total {
				if len(data) != total {
					return nil, errors.New("received extra bytes in the HS03 response")
				}
				return validateHS03Response(data, command)
			}
		}
	}
	return nil, errors.New("timed out waiting for the HS03 response")
}

func waitChunk(channel chan []byte, timeout time.Duration) ([]byte, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case chunk := <-channel:
		return chunk, true
	case <-timer.C:
		return nil, false
	}
}

func sleepCtx(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Session) fail(ctx context.Context, code, message string) {
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	first := s.attempt <= 1
	address := s.address
	s.status = sessionError
	s.errCode = code
	s.errMsg = message
	s.mu.Unlock()
	if first {
		// Log only the first failure of a retry series, the rest would be noise.
		log.Printf("%s: %s", address, message)
	}
}

func (s *Session) setConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = sessionConnected
	s.errMsg = ""
	s.errCode = ""
}

func (s *Session) setProtocol(protocol string) {
	s.mu.Lock()
	s.protocol = protocol
	s.mu.Unlock()
}

func (s *Session) setMeta(model, firmware string) {
	s.mu.Lock()
	if model != "" {
		s.model = model
	}
	if firmware != "" {
		s.firmware = firmware
	}
	address := s.address
	modelValue := s.model
	firmwareValue := s.firmware
	s.mu.Unlock()
	if s.hub != nil {
		s.hub.SetDeviceMeta(address, "", modelValue, firmwareValue)
	}
}

// setSample publishes a fresh reading and appends it to the history file.
func (s *Session) setSample(sample *Sample) {
	s.mu.Lock()
	sample.Timestamp = time.Now()
	sample.Balance = analyseBalance(sample.CellsMv)
	s.sample = sample
	s.updated = sample.Timestamp
	s.status = sessionConnected
	s.errMsg = ""
	s.errCode = ""
	address := s.address
	protocol := s.protocol
	s.mu.Unlock()

	if s.power != nil {
		s.power.Add(address, sample.Timestamp, samplePower(sample), sample.SoC)
		sample.EtaS = estimateTimeToEdge(s.power.Points(address, sample.Timestamp), sample)
	}

	device, _ := s.hub.Device(address)
	if protocol != "" {
		device.Protocol = protocol
	}
	s.hub.Touch(address)
	if s.publisher != nil {
		s.publisher.Sample(storeDevice{
			Address:    address,
			Name:       device.Name,
			Model:      device.Model,
			Brand:      device.Brand,
			Protocol:   protocol,
			Status:     "bms",
			LastSample: sample,
		})
	}
	if !s.store.Enabled() {
		return
	}
	if err := s.store.AppendSample(address, device, sample); err != nil {
		s.noteProblem(errCodeBLE, "cannot write the history file: "+err.Error())
		return
	}
	s.store.SaveLastSample(address, sample)
	s.mu.Lock()
	s.samples++
	s.mu.Unlock()
}

// noteProblem adds a warning without clearing the last reading.
func (s *Session) noteProblem(code, message string) {
	s.mu.Lock()
	s.errCode = code
	s.errMsg = message
	s.mu.Unlock()
}

// SessionState is the JSON snapshot of the monitoring session.
type SessionState struct {
	Address     string       `json:"addr"`
	Status      string       `json:"status"`
	Error       string       `json:"error"`
	ErrorCode   string       `json:"errorCode,omitempty"`
	Brand       string       `json:"brand,omitempty"`
	Model       string       `json:"model"`
	Firmware    string       `json:"firmware"`
	Protocol    string       `json:"protocol"`
	Samples     int          `json:"samples"`
	Attempt     int          `json:"attempt"`
	Auto        bool         `json:"auto"`
	UpdatedAgoS float64      `json:"updatedAgoS"`
	Sample      *Sample      `json:"sample"`
	Power       []powerPoint `json:"power,omitempty"`
	// Takeover is true while another application holds the battery and this one
	// is trying to take it back.
	Takeover bool `json:"takeover,omitempty"`
	// ReleasedS is the remaining time the battery is deliberately left free for
	// another device (zero while monitoring is active).
	ReleasedS float64 `json:"releasedS,omitempty"`
}

func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := SessionState{
		Address:   s.address,
		Status:    s.status,
		Error:     s.errMsg,
		ErrorCode: s.errCode,
		Brand:     s.brand,
		Model:     s.model,
		Firmware:  s.firmware,
		Protocol:  s.protocol,
		Samples:   s.samples,
		Attempt:   s.attempt,
		Auto:      !s.manual && s.address != "",
		Sample:    s.sample,
	}
	if !s.updated.IsZero() {
		state.UpdatedAgoS = time.Since(s.updated).Seconds()
	}
	state.Takeover = s.takeover
	if left := time.Until(s.pauseUntil); !s.pauseUntil.IsZero() && left > 0 {
		state.ReleasedS = left.Seconds()
	}
	state.Power = s.power.Points(s.address, time.Now())
	return state
}
