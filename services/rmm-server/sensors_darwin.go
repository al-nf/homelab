package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/ebitengine/purego"
)

type smcFunctions struct {
	matching func(string) uintptr
	services func(uint32, uintptr, *uint32) int32
	next     func(uint32) uint32
	name     func(uint32, *byte) int32
	release  func(uint32) int32
	open     func(uint32, uint32, uint32, *uint32) int32
	close    func(uint32) int32
	call     func(uint32, uint32, *smcPacket, uintptr, *smcPacket, *uintptr) int32
	task     func() uint32
}

var macSensors struct {
	sync.Mutex
	init      sync.Once
	initError error
	api       *smcFunctions
	keys      map[string]smcPacket
	lastError string
	reported  bool
}

func loadSMCFunctions() (*smcFunctions, error) {
	// Keep frameworks loaded for the process lifetime: unloading a library while
	// Go still retains callable symbols can crash the process.
	lib, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	a := new(smcFunctions)
	for _, symbol := range []struct {
		name string
		fn   any
	}{
		{"IOServiceMatching", &a.matching}, {"IOServiceGetMatchingServices", &a.services},
		{"IOIteratorNext", &a.next}, {"IORegistryEntryGetName", &a.name},
		{"IOObjectRelease", &a.release}, {"IOServiceOpen", &a.open},
		{"IOServiceClose", &a.close}, {"IOConnectCallStructMethod", &a.call},
		{"mach_task_self", &a.task},
	} {
		address, err := purego.Dlsym(lib, symbol.name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", symbol.name, err)
		}
		purego.RegisterFunc(symbol.fn, address)
	}
	return a, nil
}

func (a *smcFunctions) connect() (uint32, error) {
	matching := a.matching("AppleSMC")
	if matching == 0 {
		return 0, fmt.Errorf("AppleSMC service is unavailable")
	}
	var iterator uint32
	if status := a.services(0, matching, &iterator); status != 0 {
		return 0, fmt.Errorf("AppleSMC service lookup failed: 0x%x", uint32(status))
	}
	defer a.release(iterator)
	for {
		service := a.next(iterator)
		if service == 0 {
			return 0, fmt.Errorf("AppleSMCKeysEndpoint is unavailable")
		}
		var name [128]byte
		status := a.name(service, &name[0])
		end := 0
		for end < len(name) && name[end] != 0 {
			end++
		}
		if status != 0 || string(name[:end]) != "AppleSMCKeysEndpoint" {
			a.release(service)
			continue
		}
		var conn uint32
		status = a.open(service, a.task(), 0, &conn)
		a.release(service)
		if status != 0 {
			return 0, fmt.Errorf("open AppleSMCKeysEndpoint: 0x%x", uint32(status))
		}
		return conn, nil
	}
}

func (a *smcFunctions) exchange(conn uint32, input smcPacket) (smcPacket, error) {
	var output smcPacket
	size := uintptr(len(output))
	status := a.call(conn, 2, &input, uintptr(len(input)), &output, &size)
	if status != 0 || size != uintptr(len(output)) || output[40] != 0 {
		return output, fmt.Errorf("SMC read failed (kernel=0x%x, result=%d, bytes=%d)", uint32(status), output[40], size)
	}
	return output, nil
}

func (a *smcFunctions) read(conn uint32, key string, info smcPacket) (smcPacket, error) {
	input := smcRequest(key, 5)
	copy(input[28:40], info[28:40])
	return a.exchange(conn, input)
}

func (a *smcFunctions) cpuKeys(ctx context.Context, conn uint32) (map[string]smcPacket, error) {
	info, err := a.exchange(conn, smcRequest("#KEY", 9))
	if err != nil {
		return nil, err
	}
	countData, err := a.read(conn, "#KEY", info)
	if err != nil {
		return nil, err
	}
	count := binary.BigEndian.Uint32(countData[48:52])
	if count == 0 || count > 16384 {
		return nil, fmt.Errorf("invalid SMC key count: %d", count)
	}
	keys := make(map[string]smcPacket)
	for i := uint32(0); i < count; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		input := smcRequest("", 8)
		binary.LittleEndian.PutUint32(input[44:48], i)
		output, err := a.exchange(conn, input)
		if err != nil {
			continue
		}
		var text [4]byte
		binary.BigEndian.PutUint32(text[:], binary.LittleEndian.Uint32(output[0:4]))
		key := string(text[:])
		if !smcCPUKey(key) {
			continue
		}
		info, err := a.exchange(conn, smcRequest(key, 9))
		if err != nil {
			continue
		}
		size, kind := binary.LittleEndian.Uint32(info[28:32]), binary.LittleEndian.Uint32(info[32:36])
		if kind == smcFourCC("flt ") && size == 4 || kind == smcFourCC("sp78") && size == 2 {
			keys[key] = info
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no CPU temperature keys exposed by AppleSMC")
	}
	return keys, nil
}

func sampleMacCPUTemperature(ctx context.Context) (*float64, error) {
	macSensors.init.Do(func() {
		macSensors.api, macSensors.initError = loadSMCFunctions()
	})
	if macSensors.initError != nil {
		return nil, macSensors.initError
	}
	a := macSensors.api
	conn, err := a.connect()
	if err != nil {
		return nil, err
	}
	defer a.close(conn)
	if len(macSensors.keys) == 0 {
		macSensors.keys, err = a.cpuKeys(ctx, conn)
		if err != nil {
			return nil, err
		}
	}
	var hottest *float64
	for key, info := range macSensors.keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		output, err := a.read(conn, key, info)
		if err != nil {
			continue
		}
		size, kind := binary.LittleEndian.Uint32(info[28:32]), binary.LittleEndian.Uint32(info[32:36])
		value := smcTemperature(kind, output[48:48+size])
		if value != nil && (hottest == nil || *value > *hottest) {
			hottest = value
		}
	}
	if hottest == nil {
		return nil, fmt.Errorf("CPU temperature sensors returned no valid readings")
	}
	return hottest, nil
}

func readMacCPUTemperature(ctx context.Context) *float64 {
	macSensors.Lock()
	defer macSensors.Unlock()
	value, err := sampleMacCPUTemperature(ctx)
	if err != nil {
		if ctx.Err() == nil && err.Error() != macSensors.lastError {
			log.Printf("CPU temperature unavailable: %v", err)
			macSensors.lastError = err.Error()
		}
		return nil
	}
	if !macSensors.reported || macSensors.lastError != "" {
		log.Printf("CPU temperature from AppleSMC: %.1f°C", *value)
	}
	macSensors.reported, macSensors.lastError = true, ""
	return value
}
