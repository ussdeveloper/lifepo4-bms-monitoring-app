package main

import (
	"fmt"
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

// identification is the outcome of probing a single device.
type identification struct {
	Protocol string
	Brand    string
	Model    string
	Name     string
}

const probeTimeout = 6 * time.Second

// JBD / Xiaoxiang devices (Overkill Solar and many budget LiFePO4 batteries)
// expose a vendor service on FF00.
var (
	uuidServiceJBD = bluetooth.New16BitUUID(0xFF00)
	uuidNotifyJBD  = bluetooth.New16BitUUID(0xFF01)
	uuidWriteJBD   = bluetooth.New16BitUUID(0xFF02)
)

// protocolProbe describes how to recognise one BMS protocol family. Detection
// stays vendor neutral: a device is only claimed when it exposes the expected
// service with the expected characteristics and — for protocols that need it —
// answers a real protocol round-trip.
type protocolProbe struct {
	protocol   string
	brand      string
	service    bluetooth.UUID
	chars      []bluetooth.UUID
	writeUUID  bluetooth.UUID
	notifyUUID bluetooth.UUID
	confirm    func(write, notify bluetooth.DeviceCharacteristic) (string, bool)
}

var protocolProbes = []protocolProbe{
	{
		protocol: "HS03",
		brand:    "HiLink",
		service:  uuidServiceHS03,
		chars:    []bluetooth.UUID{uuidWriteHS03, uuidNotifyHS03},
	},
	{
		protocol: "HS04",
		brand:    "WATT",
		service:  uuidServiceHS04,
		chars:    []bluetooth.UUID{uuidNotifyHS04, uuidWriteHS04, uuidAuthHS04},
	},
	{
		protocol:   "JBD",
		brand:      "JBD/Xiaoxiang",
		service:    uuidServiceJBD,
		chars:      []bluetooth.UUID{uuidWriteJBD, uuidNotifyJBD},
		writeUUID:  uuidWriteJBD,
		notifyUUID: uuidNotifyJBD,
		confirm:    confirmJBD,
	},
}

// identifyDevice connects once and checks every known protocol family. The
// GATT name is only read when the device did not advertise a name, because
// every extra GATT round trip is a chance to get stuck.
func identifyDevice(adapter *bluetooth.Adapter, address bluetooth.Address, knownName string) (identification, error) {
	device, err := adapter.Connect(address, bluetooth.ConnectionParams{
		ConnectionTimeout: bluetooth.NewDuration(probeTimeout),
	})
	if err != nil {
		return identification{}, err
	}
	defer device.Disconnect()

	services, err := device.DiscoverServices(probeServiceUUIDs())
	if err != nil {
		return identification{}, err
	}

	result := identification{Name: knownName}
	if result.Name == "" {
		result.Name = readDeviceName(device)
	}
	for _, probe := range protocolProbes {
		service, ok := findService(services, probe.service)
		if !ok {
			continue
		}
		found, ok := discoverCharacteristics(service, probe.chars)
		if !ok {
			continue
		}
		model := ""
		if probe.confirm != nil {
			if model, ok = probe.confirm(found[probe.writeUUID], found[probe.notifyUUID]); !ok {
				continue
			}
		}
		result.Protocol = probe.protocol
		result.Brand = probe.brand
		result.Model = model
		return result, nil
	}
	return result, nil
}

func probeServiceUUIDs() []bluetooth.UUID {
	uuids := make([]bluetooth.UUID, 0, len(protocolProbes))
	for _, probe := range protocolProbes {
		uuids = append(uuids, probe.service)
	}
	return uuids
}

func findService(services []bluetooth.DeviceService, uuid bluetooth.UUID) (bluetooth.DeviceService, bool) {
	for _, service := range services {
		if service.UUID() == uuid {
			return service, true
		}
	}
	return bluetooth.DeviceService{}, false
}

func discoverCharacteristics(service bluetooth.DeviceService, uuids []bluetooth.UUID) (map[bluetooth.UUID]bluetooth.DeviceCharacteristic, bool) {
	chars, err := service.DiscoverCharacteristics(uuids)
	if err != nil {
		return nil, false
	}
	found := make(map[bluetooth.UUID]bluetooth.DeviceCharacteristic, len(uuids))
	for _, characteristic := range chars {
		found[characteristic.UUID()] = characteristic
	}
	for _, uuid := range uuids {
		if _, ok := found[uuid]; !ok {
			return nil, false
		}
	}
	return found, true
}

// readDeviceName reads the standard GATT device name characteristic, which is
// what the phone shows in its Bluetooth list.
func readDeviceName(device bluetooth.Device) string {
	services, err := device.DiscoverServices([]bluetooth.UUID{uuidGenericAccess})
	if err != nil || len(services) == 0 {
		return ""
	}
	chars, err := services[0].DiscoverCharacteristics([]bluetooth.UUID{uuidDeviceName})
	if err != nil || len(chars) == 0 {
		return ""
	}
	buffer := make([]byte, 64)
	count := 0
	readErr := safeBLE(func() error {
		var err error
		count, err = chars[0].Read(buffer)
		return err
	})
	if readErr != nil || count <= 0 {
		return ""
	}
	name := strings.TrimRight(string(buffer[:count]), "\x00 ")
	return strings.TrimSpace(name)
}

// batteryBrands lists name fragments used by battery BMS advertisements. The
// list only affects probing order — the protocol probe decides what a device
// really is.
var batteryBrands = []string{
	"humsienk", "hilink", "watt", "eco-worthy", "ecoworthy", "litime",
	"jbd", "xiaoxiang", "daly", "jk-bms", "jkbms", "overkill", "redodo",
	"bms", "lifepo4", "li-ion", "liion", "balancer",
}

var batteryPrefixes = []string{"hs", "eco", "dch", "bms", "jbd", "sp0", "j-", "watt"}

// isBatteryName is a fast heuristic used to give a name match priority. It is
// deliberately tolerant: a false positive only costs a probe.
func isBatteryName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || isSyntheticName(lower) {
		return false
	}
	for _, brand := range batteryBrands {
		if strings.Contains(lower, brand) {
			return true
		}
	}
	for _, prefix := range batteryPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// isSyntheticName reports whether Windows invented the label because the device
// did not advertise a name (for example "Bluetooth AA:BB:CC:DD:EE:FF").
func isSyntheticName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasPrefix(lower, "bluetooth ") {
		return false
	}
	rest := strings.TrimPrefix(lower, "bluetooth ")
	parts := strings.Split(rest, ":")
	if len(parts) != 6 {
		return false
	}
	for _, part := range parts {
		if len(part) != 2 {
			return false
		}
		for index := 0; index < 2; index++ {
			if !isHexDigit(part[index]) {
				return false
			}
		}
	}
	return true
}

// displayName returns the friendly name or an empty string when the device has
// no usable name.
func displayName(name string) string {
	trimmed := strings.TrimSpace(name)
	if isSyntheticName(trimmed) {
		return ""
	}
	return trimmed
}

// safeBLE runs a BLE library call and turns panics into errors — winrt-go can
// panic for example when a characteristic returns an empty value.
func safeBLE(operation func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("BLE library error: %v", recovered)
		}
	}()
	return operation()
}
