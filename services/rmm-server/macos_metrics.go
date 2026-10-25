package main

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var macCPUIdle = regexp.MustCompile(`(?m)^CPU usage:.*?([0-9]+(?:\.[0-9]+)?)% idle`)
var macPageSize = regexp.MustCompile(`page size of ([0-9]+) bytes`)

func parseMacCPU(data string) *float64 {
	matches := macCPUIdle.FindAllStringSubmatch(data, -1)
	// top's initial sample has no preceding interval. Use the second sample.
	if len(matches) < 2 {
		return nil
	}
	idle, err := strconv.ParseFloat(matches[len(matches)-1][1], 64)
	if err != nil || idle < 0 || idle > 100 {
		return nil
	}
	busy := 100 - idle
	return &busy
}

func parseMacMemory(vmstat, memsize string) (uint64, uint64, error) {
	total, err := strconv.ParseUint(strings.TrimSpace(memsize), 10, 64)
	if err != nil || total == 0 {
		return 0, 0, errors.New("invalid macOS memory size")
	}
	match := macPageSize.FindStringSubmatch(vmstat)
	if len(match) != 2 {
		return 0, 0, errors.New("missing macOS page size")
	}
	pageSize, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil || pageSize == 0 {
		return 0, 0, errors.New("invalid macOS page size")
	}
	pages := map[string]uint64{}
	for _, line := range strings.Split(vmstat, "\n") {
		key, text, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if key != "Pages free" && key != "Pages inactive" && key != "Pages speculative" {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(text), "."), 10, 64)
		if err != nil {
			return 0, 0, err
		}
		pages[key] = value
	}
	var available uint64
	for _, key := range []string{"Pages free", "Pages inactive", "Pages speculative"} {
		value, ok := pages[key]
		if !ok || value > math.MaxUint64/pageSize || value*pageSize > total-available {
			return 0, 0, errors.New("missing or invalid macOS memory counters")
		}
		available += value * pageSize
	}
	// Treat free, inactive, and speculative pages as available. Includes physical
	// compressor storage in used RAM, not the uncompressed logical page count.
	return total - available, total, nil
}

func parseMacLink(data, iface string) *linkTelemetry {
	var header []string
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "Name" {
			header = fields
			continue
		}
		if strings.TrimSuffix(fields[0], "*") != iface || len(fields) != len(header) {
			continue
		}
		values := map[string]string{}
		for i, key := range header {
			values[key] = fields[i]
		}
		// netstat repeats per-interface counters on address rows. Read exactly one.
		rx, rxErr := strconv.ParseUint(values["Ibytes"], 10, 64)
		tx, txErr := strconv.ParseUint(values["Obytes"], 10, 64)
		if rxErr != nil || txErr != nil {
			continue
		}
		return &linkTelemetry{Interface: iface, Type: "other", RXBytes: &rx, TXBytes: &tx}
	}
	return nil
}
