package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

// JBD / Xiaoxiang ("Smart BMS") protocol, used by Overkill Solar units and by
// many rebranded LiFePO4 batteries.
//
// Frames are 7 bytes for a read request and payload_len + 7 bytes for a reply:
//
//	request:  0xDD 0xA5 <register> <length> [data…] crc_hi crc_lo 0x77
//	response: 0xDD <register> <status> <length> [data…] crc_hi crc_lo 0x77
//
// The CRC is a 16-bit two's complement of the byte sum starting right after the
// first two bytes (that is the register/status byte onwards) up to the CRC
// field, stored big-endian. Verified against real captures of both directions.

const (
	jbdStart      = 0xDD
	jbdRead       = 0xA5
	jbdTerminator = 0x77

	jbdStatusOK    = 0x00
	jbdStatusError = 0x80

	jbdRegisterBasic = 0x03
	jbdRegisterCells = 0x04
	jbdRegisterName  = 0x05

	jbdMaxCells      = 32
	jbdBasicMinBytes = 23
	jbdConfirmWindow = 2 * time.Second
)

// buildJBDRequest builds a read request without payload data.
func buildJBDRequest(register byte) []byte {
	frame := []byte{jbdStart, jbdRead, register, 0x00, 0x00, 0x00, jbdTerminator}
	checksum := jbdChecksum(frame[2 : len(frame)-3])
	frame[4] = byte(checksum >> 8)
	frame[5] = byte(checksum)
	return frame
}

// jbdChecksum sums the given bytes and returns their 16-bit two's complement.
func jbdChecksum(data []byte) uint16 {
	sum := 0
	for _, value := range data {
		sum += int(value)
	}
	return uint16((0x10000 - sum) & 0xFFFF)
}

// validateJBDResponse checks framing, status and checksum, and returns the
// payload for the requested register.
func validateJBDResponse(frame []byte, register byte) ([]byte, error) {
	if len(frame) < 7 || len(frame) > maxFrameSize {
		return nil, errors.New("invalid JBD frame length")
	}
	if frame[0] != jbdStart || frame[len(frame)-1] != jbdTerminator {
		return nil, errors.New("invalid JBD frame start or end")
	}
	length := int(frame[3])
	if len(frame) != length+7 {
		return nil, errors.New("JBD frame length does not match the header")
	}
	if frame[1] != register {
		return nil, errors.New("the JBD BMS answered a different register")
	}
	if frame[2] != jbdStatusOK && frame[2] != jbdStatusError {
		return nil, errors.New("unknown JBD response status")
	}
	want := binary.BigEndian.Uint16(frame[len(frame)-3 : len(frame)-1])
	if got := jbdChecksum(frame[2 : len(frame)-3]); got != want {
		return nil, errors.New("invalid JBD checksum")
	}
	if frame[2] == jbdStatusError {
		return nil, errors.New("the JBD BMS refused the request")
	}
	return frame[4 : 4+length], nil
}

// exchangeJBD writes a read request and waits for the matching reply.
func exchangeJBD(channel chan []byte, write bluetooth.DeviceCharacteristic, register byte, timeout time.Duration) ([]byte, error) {
	drainNotifications(channel)
	writeErr := safeBLE(func() error {
		_, err := write.Write(buildJBDRequest(register))
		return err
	})
	if writeErr != nil {
		return nil, fmt.Errorf("writing to the BMS failed: %w", writeErr)
	}
	data := make([]byte, 0, 64)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		chunk, ok := waitChunk(channel, time.Until(deadline))
		if !ok {
			break
		}
		data = append(data, chunk...)
		if len(data) > maxFrameSize {
			return nil, errors.New("the BMS response exceeds the allowed size")
		}
		if len(data) >= 4 {
			total := int(data[3]) + 7
			if len(data) >= total {
				if len(data) != total {
					return nil, errors.New("received extra bytes in the JBD response")
				}
				return validateJBDResponse(data, register)
			}
		}
	}
	return nil, errors.New("timed out waiting for the JBD response")
}

// confirmJBD verifies that a device really speaks the JBD protocol by asking
// for the basic info register and validating the reply. The model name is read
// as well, but it is optional.
func confirmJBD(write, notify bluetooth.DeviceCharacteristic) (string, bool) {
	channel := make(chan []byte, 32)
	if err := notify.EnableNotifications(func(data []byte) {
		pushNotification(channel, data)
	}); err != nil {
		return "", false
	}

	if _, err := exchangeJBD(channel, write, jbdRegisterBasic, jbdConfirmWindow); err != nil {
		return "", false
	}
	payload, err := exchangeJBD(channel, write, jbdRegisterName, jbdConfirmWindow)
	if err != nil {
		return "", true
	}
	return decodeJBDName(payload), true
}

// decodeJBDName converts the register 0x05 payload (plain ASCII) to a string.
func decodeJBDName(payload []byte) string {
	text := strings.TrimRight(string(payload), "\x00 ")
	return strings.TrimSpace(text)
}

// decodeJBDTest verifies the basic-info payload length.
func decodeJBDTest(payload []byte) error {
	if len(payload) < jbdBasicMinBytes {
		return errors.New("JBD basic info payload too short")
	}
	return nil
}

// decodeJBDSnapshot combines the basic info and cell voltage payloads into a
// reading shared with the other protocols.
func decodeJBDSnapshot(basic, cells []byte) (*Sample, error) {
	if err := decodeJBDTest(basic); err != nil {
		return nil, err
	}

	sample := &Sample{
		Protocol:          "JBD",
		VoltageV:          float64(binary.BigEndian.Uint16(basic[0:2])) / 100,
		CurrentA:          float64(int16(binary.BigEndian.Uint16(basic[2:4]))) / 100,
		RemainingAh:       float64(binary.BigEndian.Uint16(basic[4:6])) / 100,
		DesignAh:          float64(binary.BigEndian.Uint16(basic[6:8])) / 100,
		Cycles:            int(binary.BigEndian.Uint16(basic[8:10])),
		SoC:               int(basic[0x13]),
		ChargingMosfet:    boolPtr(basic[0x14]&0x01 != 0),
		DischargingMosfet: boolPtr(basic[0x14]&0x02 != 0),
	}
	if sample.SoC < 0 || sample.SoC > 100 {
		return nil, fmt.Errorf("invalid state of charge: %d%%", sample.SoC)
	}

	balance := binary.BigEndian.Uint32(basic[0x0C:0x10])
	sample.Balancer = boolPtr(balance != 0)

	protection := binary.BigEndian.Uint16(basic[0x10:0x12])
	sample.ProblemCode = uint32Ptr(uint32(protection))
	sample.Problems = problemNames(uint32(protection))

	cellCount := int(basic[0x15])
	sensorCount := int(basic[0x16])
	if cellCount <= 0 || cellCount > jbdMaxCells {
		return nil, errors.New("invalid JBD cell count")
	}

	temperatures := make([]float64, 0, sensorCount)
	for index := 0; index < sensorCount; index++ {
		offset := 0x17 + 2*index
		if offset+2 > len(basic) {
			break
		}
		raw := int(binary.BigEndian.Uint16(basic[offset : offset+2]))
		temperatures = append(temperatures, float64(raw-2731)/10)
	}
	sample.TemperaturesC = temperatures

	if len(cells) == 0 {
		return sample, nil
	}
	if len(cells)%2 != 0 {
		return nil, errors.New("invalid JBD cell data length")
	}
	voltages := make([]int, 0, len(cells)/2)
	for offset := 0; offset+2 <= len(cells); offset += 2 {
		voltages = append(voltages, int(binary.BigEndian.Uint16(cells[offset:offset+2])))
	}
	if len(voltages) > jbdMaxCells {
		voltages = voltages[:jbdMaxCells]
	}
	sample.CellsMv = voltages
	return sample, nil
}

// jbdCellCount reads the number of cells from a basic info payload.
func jbdCellCount(basic []byte) int {
	if len(basic) <= 0x15 {
		return 0
	}
	count := int(basic[0x15])
	if count <= 0 || count > jbdMaxCells {
		return 0
	}
	return count
}

// jbdCharacteristics returns the write and notify characteristics of a device
// that exposes the JBD service.
func jbdCharacteristics(device bluetooth.Device) (write, notify bluetooth.DeviceCharacteristic, err error) {
	services, err := device.DiscoverServices([]bluetooth.UUID{uuidServiceJBD})
	if err != nil || len(services) == 0 {
		return write, notify, errors.New("the device does not expose the FF00 service")
	}
	found, ok := discoverCharacteristics(services[0], []bluetooth.UUID{uuidWriteJBD, uuidNotifyJBD})
	if !ok {
		return write, notify, errors.New("required JBD characteristics are missing")
	}
	return found[uuidWriteJBD], found[uuidNotifyJBD], nil
}
