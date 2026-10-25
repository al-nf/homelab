package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMacCPUUsesIntervalSample(t *testing.T) {
	data := "CPU usage: 1.0% user, 2.0% sys, 97.0% idle\nCPU usage: 12.50% user, 7.50% sys, 80.00% idle\n"
	if cpu := parseMacCPU(data); cpu == nil || *cpu != 20 {
		t.Fatalf("expected interval CPU 20%%, got %v", cpu)
	}
	for _, data := range []string{"", "CPU usage: 0% user, 0% sys, 100% idle", "CPU usage: 0% user, 0% sys, 100% idle\nCPU usage: 0% user, 0% sys, 101% idle"} {
		if parseMacCPU(data) != nil {
			t.Fatal("invalid or initial CPU sample accepted")
		}
	}
}

func TestMacMemoryPageSizesAndValidation(t *testing.T) {
	for _, size := range []string{"4096", "16384"} {
		vm := "Mach Virtual Memory Statistics: (page size of " + size + " bytes)\nPages free: 100.\nPages inactive: 200.\nPages speculative: 50.\nPages occupied by compressor: 700.\n"
		used, total, err := parseMacMemory(vm, "17179869184\n")
		if err != nil || total != 16<<30 {
			t.Fatalf("parse memory: %v", err)
		}
		pageSize := uint64(4096)
		if size == "16384" {
			pageSize = 16384
		}
		if used != total-350*pageSize {
			t.Fatal("wrong page size or available memory calculation")
		}
		if _, _, err := parseMacMemory(vm, "1000"); err == nil {
			t.Fatal("available bytes exceeding total accepted")
		}
	}
	if _, _, err := parseMacMemory("", "17179869184"); err == nil {
		t.Fatal("missing vm_stat accepted")
	}
}

func TestMacNetworkCountersNotDoubleCounted(t *testing.T) {
	data := "Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nutun4 1380 <Link#20> - 5 0 1234 6 0 5678 0\nutun4 1380 100.64.0 100.64.0.1 5 0 1234 6 0 5678 0\n"
	link := parseMacLink(data, "utun4")
	if link == nil || *link.RXBytes != 1234 || *link.TXBytes != 5678 || link.SpeedMbps != nil {
		t.Fatalf("incorrect interface counters: %+v", link)
	}
	if parseMacLink(data, "utun5") != nil {
		t.Fatal("accepted another interface's counters")
	}
}

func TestRegisterLaptopPreservesPetra(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := registerTelemetryHost(path, "babylon"); err != nil {
		t.Fatal(err)
	}
	if err := registerTelemetryHost(path, "babylon"); err != nil {
		t.Fatal(err)
	}
	hosts, err := loadHosts(path)
	if err != nil || len(hosts) != 2 || hosts[0].ID != "petra" || hosts[0].MAC != "F4:93:9F:F5:7B:CC" || hosts[1].ID != "babylon" || hosts[1].MAC != "" {
		t.Fatalf("host registration damaged existing configuration: %+v, %v", hosts, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("host config is not private")
	}
	if err := registerTelemetryHost(path, "../bad"); err == nil {
		t.Fatal("invalid host ID accepted")
	}
}
