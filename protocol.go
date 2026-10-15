package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

// Service and characteristic UUIDs of both Humsienk protocol variants.
var (
	uuidServiceHS04 = bluetooth.New16BitUUID(0xFFF0)
	uuidNotifyHS04  = bluetooth.New16BitUUID(0xFFF1)
	uuidWriteHS04   = bluetooth.New16BitUUID(0xFFF2)
	uuidAuthHS04    = bluetooth.New16BitUUID(0xFFFA)

	uuidServiceHS03 = bluetooth.New16BitUUID(0x0001)
	uuidWriteHS03   = bluetooth.New16BitUUID(0x0002)
	uuidNotifyHS03  = bluetooth.New16BitUUID(0x0003)

	uuidGenericAccess = bluetooth.New16BitUUID(0x1800)
	uuidDeviceName    = bluetooth.New16BitUUID(0x2A00)
)

const (
	productPoint = 0x92
	boardsPoint  = 0x1E
	analogPoint  = 0x8C
	maxFrameSize = 512

	hs03Head          = 0xAA
	hs03MinFrame      = 5
	hs03InitCommand   = 0x00
	hs03ModelCommand  = 0x11
	hs03StatusCommand = 0x20
	hs03AnalogCommand = 0x21
	hs03CellsCommand  = 0x22
	hs03MaxCells      = 24
	hs03TempCount     = 6
)

// Sample is a single BMS state reading, shared by both protocols.
type Sample struct {
	Protocol          string    `json:"protocol"`
	VoltageV          float64   `json:"voltageV"`
	CurrentA          float64   `json:"currentA"`
	SoC               int       `json:"soc"`
	SoH               *int      `json:"soh,omitempty"`
	RemainingAh       float64   `json:"remainingAh"`
	TotalAh           *float64  `json:"totalAh,omitempty"`
	DesignAh          float64   `json:"designAh"`
	Cycles            int       `json:"cycles"`
	CellsMv           []int     `json:"cellsMv"`
	TemperaturesC     []float64 `json:"temperaturesC"`
	ChargingMosfet    *bool     `json:"chargingMosfet,omitempty"`
	DischargingMosfet *bool     `json:"dischargingMosfet,omitempty"`
	Balancer          *bool     `json:"balancer,omitempty"`
	ProblemCode       *uint32   `json:"problemCode,omitempty"`
	Timestamp         time.Time `json:"timestamp"`
	// Balance compares the cells of this reading (nil when there is only one
	// cell or none).
	Balance *Balance `json:"balance,omitempty"`
	// EtaS is the estimated seconds until the battery is full (while charging)
	// or empty (while discharging); zero means the estimate is not available.
	EtaS float64 `json:"etaS,omitempty"`
}

func boolPtr(value bool) *bool        { return &value }
func intPtr(value int) *int           { return &value }
func floatPtr(value float64) *float64 { return &value }
func uint32Ptr(value uint32) *uint32  { return &value }

// --- HS04 / WATT v4 (FFF0 service, 0x7E frames, Modbus CRC) ---

func crc16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for bit := 0; bit < 8; bit++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func buildHS04Request(point byte, extended bool) []byte {
	frame := []byte{0x7E, 0x00, 0x01, 0x03, 0x00, point, 0x00, 0x00}
	if extended {
		frame[1] = 0x01
		frame = append(frame, 0x00, 0x05, 0x01, 0x00, 0x20, 0x00, 0x20)
	}
	crc := crc16Modbus(frame)
	return append(append(frame, byte(crc>>8), byte(crc)), 0x0D)
}

func validateHS04Response(frame []byte, point byte) ([]byte, error) {
	if len(frame) < 11 || len(frame) > maxFrameSize {
		return nil, errors.New("invalid HS04 frame length")
	}
	if frame[0] != 0x7E || frame[len(frame)-1] != 0x0D {
		return nil, errors.New("invalid HS04 frame start or end")
	}
	if int(binary.BigEndian.Uint16(frame[6:8]))+11 != len(frame) {
		return nil, errors.New("HS04 frame length does not match the header")
	}
	want := binary.BigEndian.Uint16(frame[len(frame)-3 : len(frame)-1])
	if crc16Modbus(frame[:len(frame)-3]) != want {
		return nil, errors.New("invalid HS04 CRC checksum")
	}
	if frame[2] != 1 || int(binary.BigEndian.Uint16(frame[4:6])) != int(point) {
		return nil, errors.New("HS04 BMS responded to a different request")
	}
	if frame[1] != 4 {
		return nil, fmt.Errorf("unsupported HS04 protocol version: %d (expected 4)", frame[1])
	}
	if frame[3] == 0x86 {
		return nil, errors.New("HS04 BMS rejected the read request")
	}
	if frame[3] != 3 {
		return nil, errors.New("unknown HS04 response type")
	}
	return frame[8 : len(frame)-3], nil
}

func decodeHS04Snapshot(payload []byte) (*Sample, error) {
	if len(payload) == 0 {
		return nil, errors.New("HS04 BMS returned an empty data set")
	}
	cellCount := int(payload[0])
	if cellCount < 1 || cellCount > 32 || len(payload) < 2+2*cellCount {
		return nil, errors.New("invalid HS04 cell count or cell data")
	}
	tempCount := int(payload[1+2*cellCount])
	offset := 2 + 2*cellCount + 2*tempCount
	if tempCount < 2 || tempCount > 32 || len(payload) < offset+14 {
		return nil, errors.New("invalid HS04 temperature sensor data")
	}
	soc := int(binary.BigEndian.Uint16(payload[offset+12 : offset+14]))
	if soc > 100 {
		return nil, fmt.Errorf("invalid state of charge: %d%%", soc)
	}
	rawCurrent := binary.BigEndian.Uint16(payload[offset : offset+2])
	current := float64(rawCurrent & 0x3FFF)
	if rawCurrent&0x4000 != 0 {
		current /= 10
	}
	if rawCurrent&0x8000 != 0 {
		current = -current
	}
	cells := make([]int, cellCount)
	for i := 0; i < cellCount; i++ {
		cells[i] = int(binary.BigEndian.Uint16(payload[1+2*i : 3+2*i]))
	}
	tempsStart := 2 + 2*cellCount
	temperatures := make([]float64, tempCount)
	for i := 0; i < tempCount; i++ {
		raw := int(binary.BigEndian.Uint16(payload[tempsStart+2*i : tempsStart+2*i+2]))
		temperatures[i] = float64(raw-2730) / 10
	}
	return &Sample{
		Protocol:      "HS04/WATT",
		VoltageV:      float64(binary.BigEndian.Uint16(payload[offset+2:offset+4])) / 100,
		CurrentA:      current,
		SoC:           soc,
		RemainingAh:   float64(binary.BigEndian.Uint16(payload[offset+4:offset+6])) / 10,
		TotalAh:       floatPtr(float64(binary.BigEndian.Uint16(payload[offset+6:offset+8])) / 10),
		DesignAh:      float64(binary.BigEndian.Uint16(payload[offset+10:offset+12])) / 10,
		Cycles:        int(binary.BigEndian.Uint16(payload[offset+8 : offset+10])),
		CellsMv:       cells,
		TemperaturesC: temperatures,
	}, nil
}

func decodeHS04Firmware(payload []byte) string {
	if len(payload) != 60 {
		return ""
	}
	text := payload[:20]
	if index := indexByte(text, 0); index >= 0 {
		text = text[:index]
	}
	return strings.TrimSpace(string(text))
}

// --- HS03 / HiLink (0001 service, 0xAA frames, checksum) ---

func buildHS03Command(command byte) []byte {
	return []byte{hs03Head, command, 0x00, command, 0x00}
}

func validateHS03Response(frame []byte, command byte) ([]byte, error) {
	if len(frame) < hs03MinFrame || len(frame) > maxFrameSize {
		return nil, errors.New("invalid HS03 frame length")
	}
	if frame[0] != hs03Head {
		return nil, errors.New("invalid HS03 frame start")
	}
	if len(frame) != int(frame[2])+hs03MinFrame {
		return nil, errors.New("HS03 frame length does not match the header")
	}
	if frame[1] != command {
		return nil, errors.New("HS03 BMS responded to a different command")
	}
	sum := uint16(0)
	for _, b := range frame[1 : len(frame)-2] {
		sum += uint16(b)
	}
	want := binary.LittleEndian.Uint16(frame[len(frame)-2:])
	if sum != want {
		return nil, errors.New("invalid HS03 checksum")
	}
	return frame[3 : len(frame)-2], nil
}

func decodeHS03Snapshot(analog, status, cells []byte) (*Sample, error) {
	if len(analog) < 26 {
		return nil, errors.New("HS03 analog data response too short")
	}
	if len(status) < 8 {
		return nil, errors.New("HS03 status response too short")
	}
	soc := int(analog[8])
	if soc > 100 {
		return nil, fmt.Errorf("invalid state of charge: %d%%", soc)
	}
	slots := len(cells) / 2
	if slots > hs03MaxCells {
		slots = hs03MaxCells
	}
	milli := make([]int, 0, slots)
	for i := 0; i < slots; i++ {
		value := int(binary.LittleEndian.Uint16(cells[2*i : 2*i+2]))
		if value > 0 {
			milli = append(milli, value)
		}
	}
	if len(milli) == 0 {
		return nil, errors.New("HS03 BMS did not report any active cell")
	}
	temperatures := make([]float64, 0, hs03TempCount)
	for i := 0; i < hs03TempCount; i++ {
		temperatures = append(temperatures, float64(int8(analog[20+i])))
	}
	problem := binary.LittleEndian.Uint32(status[4:8]) & 0xFF7F7F7F
	return &Sample{
		Protocol:          "HS03/HiLink",
		VoltageV:          float64(binary.LittleEndian.Uint32(analog[0:4])) / 1000,
		CurrentA:          float64(int32(binary.LittleEndian.Uint32(analog[4:8]))) / 1000,
		SoC:               soc,
		SoH:               intPtr(int(analog[9])),
		RemainingAh:       float64(binary.LittleEndian.Uint32(analog[10:14])) / 1000,
		DesignAh:          float64(binary.LittleEndian.Uint32(analog[14:18])) / 1000,
		Cycles:            int(binary.LittleEndian.Uint16(analog[18:20])),
		CellsMv:           milli,
		TemperaturesC:     temperatures,
		ChargingMosfet:    boolPtr(status[4]&0x80 != 0),
		DischargingMosfet: boolPtr(status[6]&0x80 != 0),
		Balancer:          boolPtr(status[5]&0x80 != 0),
		ProblemCode:       uint32Ptr(problem),
	}, nil
}

func decodeHS03Model(payload []byte) string {
	text := payload
	if index := indexByte(text, 0); index >= 0 {
		text = text[:index]
	}
	return strings.TrimSpace(string(text))
}

func indexByte(data []byte, needle byte) int {
	for i, b := range data {
		if b == needle {
			return i
		}
	}
	return -1
}
