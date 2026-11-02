package main

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func macCommand(ctx context.Context, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(data)
}

func (s *sampler) sample(ctx context.Context) telemetry {
	reading := telemetry{Hostname: s.hostname}
	reading.CPUPercent = parseMacCPU(macCommand(ctx, "/usr/bin/top", "-l", "2", "-s", "1", "-n", "0", "-F", "-R"))
	vm := macCommand(ctx, "/usr/bin/vm_stat")
	totalText := macCommand(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if used, total, err := parseMacMemory(vm, totalText); err == nil {
		percent := 100 * float64(used) / float64(total)
		reading.RAMUsedBytes, reading.RAMTotalBytes, reading.RAMPercent = &used, &total, &percent
	}
	reading.Link = parseMacLink(macCommand(ctx, "/usr/sbin/netstat", "-ibn", "-I", s.iface), s.iface)
	reading.TemperatureC = readMacCPUTemperature(ctx)
	// GPU metrics and missing/unsupported temperature sensors remain unavailable.
	return reading
}
