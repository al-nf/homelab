package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type energyCounter struct {
	value, maximum uint64
	at             time.Time
}

// Energy counters measure an interval, not instantaneous wall-socket draw.
func energyWatts(previous, current energyCounter) *float64 {
	seconds := current.at.Sub(previous.at).Seconds()
	if seconds <= 0 || seconds > telemetryStaleAfter.Seconds() || previous.maximum != current.maximum {
		return nil
	}
	var delta uint64
	if current.value >= previous.value {
		delta = current.value - previous.value
	} else {
		// Only interpret a decrease as wrap near the ends of the counter range.
		// A driver reset should produce a gap rather than a huge spike.
		if current.maximum == 0 || previous.value > current.maximum || current.value > current.maximum || previous.value < current.maximum-current.maximum/4 || current.value > current.maximum/4 {
			return nil
		}
		delta = current.maximum - previous.value + current.value
	}
	watts := float64(delta) / 1e6 / seconds
	if !validFloat(&watts, 0, 20000) {
		return nil
	}
	return &watts
}

func (s *sampler) linuxPower(now time.Time) []processorPower {
	if s.energy == nil {
		s.energy = map[string]energyCounter{}
	}
	base := filepath.Join(s.sysRoot, "class/powercap")
	paths, _ := filepath.Glob(filepath.Join(base, "*:*"))
	nested, _ := filepath.Glob(filepath.Join(base, "*", "*:*"))
	paths = append(paths, nested...)
	// Prefer the standard meter when a second MMIO driver exposes the same package.
	sort.SliceStable(paths, func(i, j int) bool { return !strings.Contains(paths[i], "mmio") && strings.Contains(paths[j], "mmio") })
	seen := map[string]bool{}
	var result []processorPower
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(path, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(data))
		// Package includes cores, uncore, etc. Never add its child domains again.
		if !strings.HasPrefix(name, "package-") || seen[name] {
			continue
		}
		seen[name] = true
		id := "cpu/" + name
		p := processorPower{ID: id, Name: "CPU " + strings.TrimPrefix(name, "package-"), Kind: "cpu", Scope: "package"}
		value := readUint(filepath.Join(path, "energy_uj"))
		maximum := readUint(filepath.Join(path, "max_energy_range_uj"))
		if value != nil && maximum != nil && *maximum > 0 && *value <= *maximum {
			current := energyCounter{*value, *maximum, now}
			if previous, ok := s.energy[id]; ok {
				p.Watts = energyWatts(previous, current)
			}
			s.energy[id] = current
		} else {
			delete(s.energy, id)
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func reportedEnergyWatts(energy int64, unit string, seconds float64) *float64 {
	if energy < 0 || seconds <= 0 || seconds > telemetryStaleAfter.Seconds() {
		return nil
	}
	scale := 0.0
	switch unit {
	case "J":
		scale = 1
	case "mJ":
		scale = 1e3
	case "uJ", "µJ", "μJ":
		scale = 1e6
	case "nJ":
		scale = 1e9
	default:
		return nil
	}
	watts := float64(energy) / scale / seconds
	if !validFloat(&watts, 0, 20000) {
		return nil
	}
	return &watts
}

// Shared rails must not be presented as CPU-only or GPU-only power.
func validPowerScope(kind, scope string) bool {
	switch kind {
	case "cpu":
		return scope == "package" || scope == "processor"
	case "gpu":
		return scope == "board" || scope == "processor"
	case "system":
		return scope == "rails" || scope == "rail"
	}
	return false
}
