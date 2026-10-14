//go:build windows

package main

import (
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, text string) []byte {
	t.Helper()
	value, err := hex.DecodeString(text)
	if err != nil {
		t.Fatalf("invalid test fixture: %v", err)
	}
	return value
}

// basicPayload is the payload of a real 0x03 reply captured from an Overkill
// Solar / JBD SP04S034: 13.34 V, -0.70 A, 204.65 Ah remaining, 96 % SoC.
func basicPayload(t *testing.T) []byte {
	t.Helper()
	return mustHex(t,
		"0536"+"ffba"+"4ff1"+"5334"+"0001"+"309b"+ // voltage, current, capacities, cycles, date
			"0000"+"0000"+"0000"+ // no balancing, no protection flags
			"44"+"60"+"03"+"04"+"03"+ // version, SoC, FETs, cells, NTCs
			"0bb7"+"0bbf"+"0bb9", // 26.8 / 27.6 / 27.0 degrees
	)
}

func basicFrame(t *testing.T) []byte {
	t.Helper()
	return append(mustHex(t, "dd03001d"), append(basicPayload(t), mustHex(t, "f85e77")...)...)
}

// cellsFrame is a real 0x04 reply: 3910, 3902, 3896, 3902 mV.
func cellsFrame(t *testing.T) []byte {
	t.Helper()
	return mustHex(t, "dd040008"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec2"+"77")
}

func TestJBDBuildRequest(t *testing.T) {
	cases := map[byte]string{
		jbdRegisterBasic: "dda50300fffd77",
		jbdRegisterCells: "dda50400fffc77",
		jbdRegisterName:  "dda50500fffb77",
	}
	for register, want := range cases {
		if got := hex.EncodeToString(buildJBDRequest(register)); got != want {
			t.Errorf("register 0x%02X: got %s, want %s", register, got, want)
		}
	}
}

func TestJBDChecksumRule(t *testing.T) {
	// Documented coverage: every byte between the first two bytes and the
	// checksum field. Examples come from real request/reply captures.
	cases := []struct {
		covered []byte
		want    uint16
	}{
		{[]byte{0x03, 0x00}, 0xFFFD},                 // request, register 0x03
		{[]byte{0x04, 0x00}, 0xFFFC},                 // request, register 0x04
		{[]byte{0x05, 0x00}, 0xFFFB},                 // request, register 0x05
		{[]byte{0xAA, 0x00}, 0xFF56},                 // request, register 0xAA
		{mustHex(t, "00080f460f3e0f380f3e"), 0xFEC2}, // reply, register 0x04
	}
	for _, test := range cases {
		if got := jbdChecksum(test.covered); got != test.want {
			t.Errorf("checksum of %X: got 0x%04X, want 0x%04X", test.covered, got, test.want)
		}
	}
}

func TestJBDValidateResponses(t *testing.T) {
	payload, err := validateJBDResponse(basicFrame(t), jbdRegisterBasic)
	if err != nil {
		t.Fatalf("valid basic info frame rejected: %v", err)
	}
	if len(payload) != 29 {
		t.Fatalf("payload length: got %d, want 29", len(payload))
	}

	payload, err = validateJBDResponse(cellsFrame(t), jbdRegisterCells)
	if err != nil {
		t.Fatalf("valid cell frame rejected: %v", err)
	}
	if len(payload) != 8 {
		t.Fatalf("cell payload length: got %d, want 8", len(payload))
	}
}

func TestJBDRejectsDamagedFrames(t *testing.T) {
	cases := map[string][]byte{
		"checksum":    mustHex(t, "dd040008"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec3"+"77"),
		"terminator":  mustHex(t, "dd040008"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec2"+"00"),
		"start":       mustHex(t, "de040008"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec2"+"77"),
		"length":      mustHex(t, "dd040009"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec2"+"77"),
		"register":    mustHex(t, "dd050008"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec2"+"77"),
		"extra bytes": mustHex(t, "dd040008"+"0f46"+"0f3e"+"0f38"+"0f3e"+"fec2"+"7777"),
		"too short":   mustHex(t, "dd0400"),
	}
	for name, damaged := range cases {
		if _, err := validateJBDResponse(damaged, jbdRegisterCells); err == nil {
			t.Errorf("%s: damaged frame was accepted", name)
		}
	}
	if _, err := validateJBDResponse(cellsFrame(t), jbdRegisterBasic); err == nil {
		t.Error("a valid frame for another register must be rejected")
	}

	refused := append([]byte(nil), cellsFrame(t)...)
	refused[2] = 0x80
	if _, err := validateJBDResponse(refused, jbdRegisterCells); err == nil {
		t.Error("a refused request must be reported as an error")
	}
}

func TestJBDDecodeSnapshot(t *testing.T) {
	sample, err := decodeJBDSnapshot(basicPayload(t), mustHex(t, "0f46"+"0f3e"+"0f38"+"0f3e"))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if sample.Protocol != "JBD" {
		t.Errorf("protocol: got %q", sample.Protocol)
	}
	if sample.VoltageV != 13.34 {
		t.Errorf("voltage: got %v, want 13.34", sample.VoltageV)
	}
	if sample.CurrentA != -0.70 {
		t.Errorf("current: got %v, want -0.70 (discharging)", sample.CurrentA)
	}
	if sample.RemainingAh != 204.65 || sample.DesignAh != 213 {
		t.Errorf("capacities: got %v / %v", sample.RemainingAh, sample.DesignAh)
	}
	if sample.Cycles != 1 {
		t.Errorf("cycles: got %d, want 1", sample.Cycles)
	}
	if sample.SoC != 96 {
		t.Errorf("state of charge: got %d, want 96", sample.SoC)
	}
	if sample.ChargingMosfet == nil || !*sample.ChargingMosfet {
		t.Error("charge MOSFET should be conducting")
	}
	if sample.DischargingMosfet == nil || !*sample.DischargingMosfet {
		t.Error("discharge MOSFET should be conducting")
	}
	if sample.Balancer == nil || *sample.Balancer {
		t.Error("the balancer is off in this capture")
	}
	if sample.ProblemCode == nil || *sample.ProblemCode != 0 {
		t.Error("no protection flags are expected")
	}
	if len(sample.CellsMv) != 4 ||
		sample.CellsMv[0] != 3910 || sample.CellsMv[1] != 3902 ||
		sample.CellsMv[2] != 3896 || sample.CellsMv[3] != 3902 {
		t.Errorf("cell voltages: got %v", sample.CellsMv)
	}
	temperatures := sample.TemperaturesC
	if len(temperatures) != 3 {
		t.Fatalf("temperatures: got %v", temperatures)
	}
	for index, want := range []float64{26.8, 27.6, 27.0} {
		if temperatures[index] != want {
			t.Errorf("temperature %d: got %v, want %v", index, temperatures[index], want)
		}
	}
}

func TestJBDDecodeRejectsImpossibleData(t *testing.T) {
	basic := basicPayload(t)

	if _, err := decodeJBDSnapshot(basic[:10], nil); err == nil {
		t.Error("too short payload must be rejected")
	}

	badSoc := append([]byte(nil), basic...)
	badSoc[0x13] = 0xFF
	if _, err := decodeJBDSnapshot(badSoc, nil); err == nil {
		t.Error("state of charge above 100 must be rejected")
	}

	badCells := append([]byte(nil), basic...)
	badCells[0x15] = 0
	if _, err := decodeJBDSnapshot(badCells, nil); err == nil {
		t.Error("zero cell count must be rejected")
	}

	if _, err := decodeJBDSnapshot(basic, []byte{0x0F}); err == nil {
		t.Error("odd cell payload must be rejected")
	}
}

func TestJBDDecodeName(t *testing.T) {
	if got := decodeJBDName([]byte("JBD-SP04S034-L4S-200A-B-U")); got != "JBD-SP04S034-L4S-200A-B-U" {
		t.Errorf("got %q", got)
	}
	if got := decodeJBDName([]byte("SP04S034\x00\x00")); got != "SP04S034" {
		t.Errorf("NUL padding must be trimmed, got %q", got)
	}
}

func TestJBDCellCount(t *testing.T) {
	if got := jbdCellCount(basicPayload(t)); got != 4 {
		t.Errorf("cell count: got %d, want 4", got)
	}
	if got := jbdCellCount(basicPayload(t)[:8]); got != 0 {
		t.Errorf("short payload must report no cells, got %d", got)
	}
}
