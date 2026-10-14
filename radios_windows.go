//go:build windows

package main

import (
	"log"
	"strings"
	"syscall"
	"unsafe"
)

// Bluetooth radios of this computer.
//
// The BLE library talks to the Windows default radio only, so a computer with
// two radios (an internal one and a dongle, for example) can miss a battery that
// the other radio hears. The Win32 Bluetooth API is used here to enumerate every
// radio with the devices it knows: those addresses and names are handed to the
// hub, which then reaches them by address — Windows routes the connection over
// whichever radio can actually see the device.
//
// The functions live in bthprops.cpl; when the library is not readable the whole
// helper degrades to "no radios known".

var (
	bthprops = syscall.NewLazyDLL("bthprops.cpl")

	procFindFirstRadio  = bthprops.NewProc("BluetoothFindFirstRadio")
	procFindNextRadio   = bthprops.NewProc("BluetoothFindNextRadio")
	procFindRadioClose  = bthprops.NewProc("BluetoothFindRadioClose")
	procGetRadioInfo    = bthprops.NewProc("BluetoothGetRadioInfo")
	procFindFirstDevice = bthprops.NewProc("BluetoothFindFirstDevice")
	procFindNextDevice  = bthprops.NewProc("BluetoothFindNextDevice")
	procFindDeviceClose = bthprops.NewProc("BluetoothFindDeviceClose")

	procCloseHandle = kernel32.NewProc("CloseHandle")
)

const bluetoothNameLength = 248

type bluetoothFindRadioParams struct {
	size uint32
}

type bluetoothRadioInfo struct {
	size           uint32
	address        uint64
	name           [bluetoothNameLength]uint16
	classOfDevice  uint32
	lmpSubversion  uint64
	manufacturerID uint16
}

type systemTime struct {
	year, month, dayOfWeek, day, hour, minute, second, milliseconds uint16
}

type bluetoothDeviceInfo struct {
	size          uint32
	address       uint64
	classOfDevice uint32
	connected     int32
	remembered    int32
	authenticated int32
	lastSeen      systemTime
	lastUsed      systemTime
	name          [bluetoothNameLength]uint16
}

type bluetoothDeviceSearchParams struct {
	size                uint32
	returnAuthenticated int32
	returnRemembered    int32
	returnUnknown       int32
	returnConnected     int32
	issueInquiry        int32
	timeoutMultiplier   uint8
	radio               uintptr
	find                uintptr
}

// radioSummary is one Bluetooth radio with the devices it knows about.
type radioSummary struct {
	Name    string        `json:"name"`
	Address string        `json:"address"`
	Devices []radioDevice `json:"-"`
}

// radioDevice is one device a radio knows (paired, remembered or connected).
type radioDevice struct {
	Address   string `json:"addr"`
	Name      string `json:"name,omitempty"`
	Connected bool   `json:"connected,omitempty"`
}

// enumerateRadios lists every Bluetooth radio together with its known devices.
func enumerateRadios() []radioSummary {
	if err := bthprops.Load(); err != nil {
		return nil
	}
	params := bluetoothFindRadioParams{size: uint32(unsafe.Sizeof(bluetoothFindRadioParams{}))}
	var radio uintptr
	find, _, _ := procFindFirstRadio.Call(
		uintptr(unsafe.Pointer(&params)),
		uintptr(unsafe.Pointer(&radio)),
	)
	if find == 0 {
		return nil
	}
	defer procFindRadioClose.Call(find)

	radios := make([]radioSummary, 0, 2)
	unnamedRadios := 0
	for radio != 0 {
		summary := radioSummary{}
		info := bluetoothRadioInfo{size: uint32(unsafe.Sizeof(bluetoothRadioInfo{}))}
		// BluetoothGetRadioInfo returns an error code: zero means success. Some
		// systems refuse it (error 1306), in which case the radio is still counted
		// but stays unnamed.
		result, _, _ := procGetRadioInfo.Call(radio, uintptr(unsafe.Pointer(&info)))
		if result == 0 {
			summary.Name = decodeBluetoothName(info.name)
			summary.Address = formatBluetoothAddress(info.address)
		} else if unnamedRadios == 0 {
			log.Printf("the Bluetooth radio name is not available (error %d); the radio is still used", result)
		}
		unnamedRadios++
		summary.Devices = enumerateRadioDevices(radio)
		radios = append(radios, summary)

		procCloseHandle.Call(radio)
		var next uintptr
		if ok, _, _ := procFindNextRadio.Call(find, uintptr(unsafe.Pointer(&next))); ok == 0 {
			break
		}
		radio = next
	}
	return radios
}

// enumerateRadioDevices lists the devices one radio knows. No inquiry is issued:
// only devices Windows already knows are returned, which keeps the call fast and
// does not interfere with the advertisement scan.
func enumerateRadioDevices(radio uintptr) []radioDevice {
	search := bluetoothDeviceSearchParams{
		size:                uint32(unsafe.Sizeof(bluetoothDeviceSearchParams{})),
		returnAuthenticated: 1,
		returnRemembered:    1,
		returnUnknown:       1,
		returnConnected:     1,
		timeoutMultiplier:   1,
		radio:               radio,
	}
	info := bluetoothDeviceInfo{size: uint32(unsafe.Sizeof(bluetoothDeviceInfo{}))}
	find, _, _ := procFindFirstDevice.Call(
		uintptr(unsafe.Pointer(&search)),
		uintptr(unsafe.Pointer(&info)),
	)
	if find == 0 {
		return nil
	}
	defer procFindDeviceClose.Call(find)

	devices := make([]radioDevice, 0, 16)
	for {
		name := decodeBluetoothName(info.name)
		if address := formatBluetoothAddress(info.address); address != "" {
			devices = append(devices, radioDevice{
				Address:   address,
				Name:      name,
				Connected: info.connected != 0,
			})
		}
		info = bluetoothDeviceInfo{size: uint32(unsafe.Sizeof(bluetoothDeviceInfo{}))}
		if ok, _, _ := procFindNextDevice.Call(find, uintptr(unsafe.Pointer(&info))); ok == 0 {
			break
		}
	}
	return devices
}

// decodeBluetoothName turns the fixed size UTF-16 name of a device or radio into
// a string.
func decodeBluetoothName(raw [bluetoothNameLength]uint16) string {
	length := 0
	for length < len(raw) && raw[length] != 0 {
		length++
	}
	if length == 0 {
		return ""
	}
	return strings.TrimSpace(syscall.UTF16ToString(raw[:length]))
}

// formatBluetoothAddress renders a Bluetooth address the same way the scanner
// does (AA:BB:CC:DD:EE:FF). The addresses in the Win32 structures are stored
// little endian, so the bytes are read from the least significant one.
func formatBluetoothAddress(address uint64) string {
	const hex = "0123456789ABCDEF"
	if address == 0 {
		return ""
	}
	out := make([]byte, 0, 17)
	for index := 0; index < 6; index++ {
		if index > 0 {
			out = append(out, ':')
		}
		value := byte(address >> (8 * index))
		out = append(out, hex[value>>4], hex[value&0x0F])
	}
	return string(out)
}

// isRadioBatteryCandidate reports whether a device known to a radio is worth
// adopting as a battery: either its name looks like a battery, or the address is
// already known as a battery.
func isRadioBatteryCandidate(device radioDevice, knownBattery func(string) bool) bool {
	if strings.TrimSpace(device.Address) == "" {
		return false
	}
	if knownBattery != nil && knownBattery(device.Address) {
		return true
	}
	return isBatteryName(device.Name)
}
