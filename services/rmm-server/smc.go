package main

import (
	"encoding/binary"
	"math"
	"strings"
)

// The AppleSMC user-client ABI is an 80-byte structure. Explicit byte offsets
// avoid depending on Go struct padding when passing it to IOKit.
type smcPacket [80]byte

func smcFourCC(key string) uint32 {
	if len(key) != 4 {
		return 0
	}
	return binary.BigEndian.Uint32([]byte(key))
}

func smcRequest(key string, command byte) smcPacket {
	var p smcPacket
	binary.LittleEndian.PutUint32(p[0:4], smcFourCC(key))
	p[42] = command
	return p
}

func smcCPUKey(key string) bool {
	return len(key) == 4 && (strings.HasPrefix(key, "Tp") || strings.HasPrefix(key, "Te") || strings.HasPrefix(key, "Ts"))
}

func smcTemperature(kind uint32, data []byte) *float64 {
	var value float64
	switch {
	case kind == smcFourCC("flt ") && len(data) == 4:
		value = float64(math.Float32frombits(binary.LittleEndian.Uint32(data)))
	case kind == smcFourCC("sp78") && len(data) == 2:
		value = float64(int16(binary.BigEndian.Uint16(data))) / 256
	default:
		return nil
	}
	if value <= 0 || !validFloat(&value, 0, 150) {
		return nil
	}
	return &value
}
