package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnergyWatts(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name                       string
		previous, current, maximum uint64
		elapsed                    time.Duration
		want                       *float64
	}{
		{"normal", 1e6, 26e6, 1e9, 5 * time.Second, floatPtr(5)},
		{"idle zero", 1e6, 1e6, 1e9, time.Second, floatPtr(0)},
		{"wrap", 990e6, 10e6, 1e9, 5 * time.Second, floatPtr(4)},
		{"reset", 500e6, 10e6, 1e9, time.Second, nil},
		{"long gap", 1e6, 2e6, 1e9, time.Minute, nil},
		{"same instant", 1e6, 2e6, 1e9, 0, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := energyWatts(energyCounter{test.previous, test.maximum, now}, energyCounter{test.current, test.maximum, now.Add(test.elapsed)})
			if (got == nil) != (test.want == nil) || got != nil && math.Abs(*got-*test.want) > 1e-8 {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}
func floatPtr(v float64) *float64 { return &v }

func TestLinuxPackagePower(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	s := sampler{sysRoot: root}
	write := func(zone, name, energy string) {
		dir := filepath.Join(root, "class/powercap", zone)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for k, v := range map[string]string{"name": name, "energy_uj": energy, "max_energy_range_uj": "1000000000"} {
			if err := os.WriteFile(filepath.Join(dir, k), []byte(v), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("intel-rapl:0", "package-0", "1000000")
	write("intel-rapl:0:0", "core", "1000000")
	write("intel-rapl-mmio:0", "package-0", "9000000")
	first := s.linuxPower(now)
	if len(first) != 1 || first[0].Watts != nil {
		t.Fatalf("first %+v", first)
	}
	write("intel-rapl:0", "package-0", "26000000")
	got := s.linuxPower(now.Add(5 * time.Second))
	if len(got) != 1 || got[0].Watts == nil || *got[0].Watts != 5 {
		t.Fatalf("power %+v", got)
	}
	// Missing/unreadable counters cannot become a zero or carry an old baseline.
	os.Remove(filepath.Join(root, "class/powercap/intel-rapl:0/energy_uj"))
	got = s.linuxPower(now.Add(10 * time.Second))
	if len(got) != 1 || got[0].Watts != nil {
		t.Fatalf("missing %+v", got)
	}
	write("intel-rapl:0", "package-0", "50000000")
	if s.linuxPower(now.Add(15 * time.Second))[0].Watts != nil {
		t.Fatal("reused baseline after unavailable counter")
	}
}

func TestGPUAndIOReportPower(t *testing.T) {
	g, err := parseGPUs("GPU-a,RTX,50,10,100,200,42.75\nGPU-b,RTX,50,10,100,200,N/A")
	if err != nil || len(g) != 2 || g[0].PowerWatts == nil || *g[0].PowerWatts != 42.75 || g[1].PowerWatts != nil {
		t.Fatalf("%+v %v", g, err)
	}
	for unit, energy := range map[string]int64{"J": 10, "mJ": 10000, "uJ": 10000000, "µJ": 10000000, "nJ": 10000000000} {
		p := reportedEnergyWatts(energy, unit, 2)
		if p == nil || *p != 5 {
			t.Fatalf("%s: %v", unit, p)
		}
	}
	if reportedEnergyWatts(-1, "mJ", 1) != nil || reportedEnergyWatts(10, "watts", 1) != nil {
		t.Fatal("accepted invalid IOReport energy")
	}
}

func TestPowerValidation(t *testing.T) {
	p := processorPower{ID: "cpu/0", Name: "CPU 0", Kind: "cpu", Scope: "package", Watts: floatPtr(0)}
	if err := validateTelemetry(telemetry{Power: []processorPower{p}}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 20001} {
		p.Watts = &v
		if validateTelemetry(telemetry{Power: []processorPower{p}}) == nil {
			t.Fatalf("accepted %v", v)
		}
	}
	p.Watts = nil
	if validateTelemetry(telemetry{Power: []processorPower{p, p}}) == nil {
		t.Fatal("accepted duplicate processor")
	}
}

func TestPowerHistoryPersistenceAndGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	h, err := openHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(15 * time.Second).Add(time.Second)
	p := processorPower{ID: "gpu/a", Name: "GPU", Kind: "gpu", Scope: "board", Watts: floatPtr(42)}
	reading := telemetry{UpdatedAt: &now, Power: []processorPower{p}}
	if err = h.record("petra", reading); err != nil {
		t.Fatal(err)
	}
	later := now.Add(30 * time.Second)
	reading.UpdatedAt = &later
	reading.Power[0].Watts = floatPtr(0)
	if err = h.record("petra", reading); err != nil {
		t.Fatal(err)
	}
	// An older report in the same storage bucket cannot replace either table.
	older := later.Add(-time.Millisecond)
	reading.UpdatedAt = &older
	reading.Power[0].Watts = floatPtr(99)
	if err = h.record("petra", reading); err != nil {
		t.Fatal(err)
	}
	h.db.Close()
	h, err = openHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.db.Close()
	result, err := h.query(context.Background(), "petra", "1h", later.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Processors) != 1 || result.Processors[0].ID != "gpu/a" {
		t.Fatalf("metadata %+v", result.Processors)
	}
	n := len(result.Points)
	a, b, c := result.Points[n-3], result.Points[n-2], result.Points[n-1]
	if a.PowerWatts[p.ID] == nil || *a.PowerWatts[p.ID] != 42 || b.PowerWatts[p.ID] != nil || c.PowerWatts[p.ID] == nil || *c.PowerWatts[p.ID] != 0 {
		t.Fatalf("points %+v %+v %+v", a, b, c)
	}
	other, err := h.query(context.Background(), "babylon", "1h", later)
	if err != nil || len(other.Processors) != 0 {
		t.Fatalf("host isolation %+v %v", other.Processors, err)
	}
}

func TestPowerHistoryRollbackAndRetention(t *testing.T) {
	h, err := openHistory(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.db.Close()
	now := time.Now().UTC().Truncate(5 * time.Second).Add(time.Second)
	p := processorPower{ID: "cpu/0", Name: "CPU", Kind: "cpu", Scope: "package", Watts: floatPtr(12)}
	sample := telemetry{UpdatedAt: &now, CPUPercent: floatPtr(20), Power: []processorPower{p}}
	if err = h.record("petra", sample); err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Exec(`CREATE TRIGGER power_failure BEFORE INSERT ON processor_power_history BEGIN SELECT RAISE(FAIL,'power-write-failed'); END`)
	if err != nil {
		t.Fatal(err)
	}
	newer := now.Add(time.Millisecond)
	sample.UpdatedAt = &newer
	sample.CPUPercent = floatPtr(99)
	if h.record("petra", sample) == nil {
		t.Fatal("expected power write failure")
	}
	result, err := h.query(context.Background(), "petra", "1h", newer)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Points[len(result.Points)-1]
	if last.CPUPercent == nil || *last.CPUPercent != 20 || last.PowerWatts[p.ID] == nil || *last.PowerWatts[p.ID] != 12 {
		t.Fatal("partially committed sample", last)
	}
	if _, err = h.db.Exec("DROP TRIGGER power_failure"); err != nil {
		t.Fatal(err)
	}
	stale := now.Add(-49 * time.Hour)
	sample.UpdatedAt = &stale
	if err = h.record("petra", sample); err != nil {
		t.Fatal(err)
	}
	if err = h.prune(now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = h.db.QueryRow("SELECT COUNT(*) FROM processor_power_history").Scan(&count); err != nil || count != 1 {
		t.Fatalf("power retention count %d: %v", count, err)
	}
}
