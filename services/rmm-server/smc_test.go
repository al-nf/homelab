package main

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestSMCReadPacketABI(t *testing.T) {
	p := smcRequest("Tp01", 9)
	if len(p) != 80 || binary.LittleEndian.Uint32(p[:4]) != 0x54703031 || p[42] != 9 {
		t.Fatal("wrong AppleSMC key encoding or command offset")
	}
	for i, value := range p {
		if i >= 4 && i != 42 && value != 0 {
			t.Fatal("unexpected data in SMC request")
		}
	}
}

func TestSMCCPUTemperatureKeys(t *testing.T) {
	for _, key := range []string{"Tp01", "Te05", "Ts0P"} {
		if !smcCPUKey(key) {
			t.Fatalf("CPU sensor rejected: %s", key)
		}
	}
	for _, key := range []string{"TB0T", "Tg01", "TA0P", "F0Ac", "Tp", "Tp000"} {
		if smcCPUKey(key) {
			t.Fatalf("non-CPU sensor accepted: %s", key)
		}
	}
}

func TestSMCTemperatureDecoding(t *testing.T) {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], math.Float32bits(48.25))
	if value := smcTemperature(smcFourCC("flt "), data[:]); value == nil || *value != 48.25 {
		t.Fatal("wrong Apple Silicon floating-point temperature")
	}
	if value := smcTemperature(smcFourCC("sp78"), []byte{48, 64}); value == nil || *value != 48.25 {
		t.Fatal("wrong signed fixed-point temperature")
	}
	for _, number := range []float32{0, -1, 151, float32(math.NaN()), float32(math.Inf(1))} {
		binary.LittleEndian.PutUint32(data[:], math.Float32bits(number))
		if smcTemperature(smcFourCC("flt "), data[:]) != nil {
			t.Fatalf("invalid temperature accepted: %v", number)
		}
	}
	if smcTemperature(smcFourCC("flt "), []byte{1}) != nil || smcTemperature(smcFourCC("ui32"), []byte{1, 2, 3, 4}) != nil || smcTemperature(smcFourCC("sp78"), []byte{255, 0}) != nil {
		t.Fatal("malformed, unknown, or negative SMC reading accepted")
	}
}
