package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

// IOReport Energy Model reports the M3 CPU and GPU separately. This does not
// include display, RAM, charging, or the machine's total electrical input.
type reportAPI struct {
	stringNew                           func(uintptr, string, uint32) uintptr
	stringRead                          func(uintptr, *byte, int64, uint32) bool
	dictValue                           func(uintptr, uintptr) uintptr
	count                               func(uintptr) int64
	item                                func(uintptr, int64) uintptr
	release                             func(uintptr)
	channels                            func(uintptr, uintptr, uint64, uint64, uint64) uintptr
	subscribe                           func(uintptr, uintptr, *uintptr, uint64, uintptr) uintptr
	samples                             func(uintptr, uintptr, uintptr) uintptr
	delta                               func(uintptr, uintptr, uintptr) uintptr
	group                               func(uintptr) uintptr
	name                                func(uintptr) uintptr
	unit                                func(uintptr) uintptr
	value                               func(uintptr, int32) int64
	subscription, actual, key, previous uintptr
	at                                  time.Time
}

var macPower struct {
	sync.Mutex
	once      sync.Once
	api       *reportAPI
	err       error
	lastError string
}

func loadReportAPI() (*reportAPI, error) {
	a := new(reportAPI)
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	lib, err := purego.Dlopen("/usr/lib/libIOReport.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	for _, s := range []struct {
		lib  uintptr
		name string
		fn   any
	}{
		{cf, "CFStringCreateWithCString", &a.stringNew}, {cf, "CFStringGetCString", &a.stringRead},
		{cf, "CFDictionaryGetValue", &a.dictValue}, {cf, "CFArrayGetCount", &a.count}, {cf, "CFArrayGetValueAtIndex", &a.item}, {cf, "CFRelease", &a.release},
		{lib, "IOReportCopyChannelsInGroup", &a.channels}, {lib, "IOReportCreateSubscription", &a.subscribe},
		{lib, "IOReportCreateSamples", &a.samples}, {lib, "IOReportCreateSamplesDelta", &a.delta},
		{lib, "IOReportChannelGetGroup", &a.group}, {lib, "IOReportChannelGetChannelName", &a.name},
		{lib, "IOReportChannelGetUnitLabel", &a.unit}, {lib, "IOReportSimpleGetIntegerValue", &a.value},
	} {
		symbol, err := purego.Dlsym(s.lib, s.name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		purego.RegisterFunc(s.fn, symbol)
	}
	group := a.stringNew(0, "Energy Model", 0x08000100)
	if group == 0 {
		return nil, fmt.Errorf("create Energy Model string")
	}
	defer a.release(group)
	desired := a.channels(group, 0, 0, 0, 0)
	if desired == 0 {
		return nil, fmt.Errorf("Energy Model counters unavailable")
	}
	defer a.release(desired)
	a.subscription = a.subscribe(0, desired, &a.actual, 0, 0)
	if a.subscription == 0 || a.actual == 0 {
		return nil, fmt.Errorf("Energy Model subscription unavailable")
	}
	// These retained objects and framework handles live for the collector lifetime.
	a.key = a.stringNew(0, "IOReportChannels", 0x08000100)
	if a.key == 0 {
		return nil, fmt.Errorf("create IOReportChannels string")
	}
	return a, nil
}

func (a *reportAPI) text(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	var buf [256]byte
	if !a.stringRead(ref, &buf[0], int64(len(buf)), 0x08000100) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return ""
}

func (a *reportAPI) read(now time.Time) ([]processorPower, error) {
	current := a.samples(a.subscription, a.actual, 0)
	if current == 0 {
		return nil, fmt.Errorf("Energy Model sample unavailable")
	}
	previous, at := a.previous, a.at
	a.previous, a.at = current, now
	if previous == 0 {
		return nil, nil
	}
	defer a.release(previous)
	seconds := now.Sub(at).Seconds()
	if seconds <= 0 || seconds > telemetryStaleAfter.Seconds() {
		return nil, nil
	}
	delta := a.delta(previous, current, 0)
	if delta == 0 {
		return nil, fmt.Errorf("Energy Model delta unavailable")
	}
	defer a.release(delta)
	channels := a.dictValue(delta, a.key)
	if channels == 0 {
		return nil, fmt.Errorf("Energy Model channels unavailable")
	}
	var result []processorPower
	for i, n := int64(0), a.count(channels); i < n; i++ {
		ch := a.item(channels, i)
		if ch == 0 || a.text(a.group(ch)) != "Energy Model" {
			continue
		}
		name := a.text(a.name(ch))
		kind := ""
		label := ""
		if name == "CPU Energy" || strings.HasSuffix(name, "_CPU Energy") {
			kind = "cpu"
			label = strings.TrimSuffix(name, " Energy")
		}
		if name == "GPU Energy" || strings.HasSuffix(name, "_GPU Energy") {
			kind = "gpu"
			label = strings.TrimSuffix(name, " Energy")
		}
		if kind == "" {
			continue
		}
		watts := reportedEnergyWatts(a.value(ch, 0), a.text(a.unit(ch)), seconds)
		result = append(result, processorPower{ID: kind + "/" + name, Name: label, Kind: kind, Scope: "processor", Watts: watts})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("CPU/GPU Energy Model counters unavailable")
	}
	return result, nil
}

func readMacPower(now time.Time) []processorPower {
	macPower.Lock()
	defer macPower.Unlock()
	macPower.once.Do(func() { macPower.api, macPower.err = loadReportAPI() })
	var result []processorPower
	err := macPower.err
	if err == nil {
		result, err = macPower.api.read(now)
	}
	if err != nil {
		if err.Error() != macPower.lastError {
			log.Printf("Processor power: %v", err)
			macPower.lastError = err.Error()
		}
		return nil
	}
	if macPower.lastError != "" {
		log.Print("Processor power readings recovered.")
		macPower.lastError = ""
	}
	return result
}
