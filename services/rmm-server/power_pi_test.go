package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func piADCFixture() string {
	var lines []string
	for i, rail := range piPowerRails {
		lines = append(lines, fmt.Sprintf("  %s_A current(%d)=0.10000000A", rail, i))
	}
	// The firmware prints all currents before all voltages, not paired lines.
	for i, rail := range piPowerRails {
		lines = append(lines, fmt.Sprintf("  %s_V volt(%d)=1.00000000V", rail, i+8))
	}
	return strings.Join(lines, "\n") + "\nEXT5V_V volt(24)=5.10000000V\nBATT_V volt(25)=3.00000000V\n"
}

func TestPiPMICPower(t *testing.T) {
	readings, err := parsePiPower(piADCFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 2 || readings[0].Watts == nil || math.Abs(*readings[0].Watts-1.2) > 1e-9 || readings[1].Watts == nil || *readings[1].Watts != 0.1 {
		t.Fatalf("unexpected PMIC sum/core %+v", readings)
	}
	if err := validateTelemetry(telemetry{Power: readings}); err != nil {
		t.Fatal(err)
	}
	zero := strings.ReplaceAll(piADCFixture(), "0.10000000A", "0.00000000A")
	readings, err = parsePiPower(zero)
	if err != nil || readings[0].Watts == nil || *readings[0].Watts != 0 {
		t.Fatal("real zero must remain zero", err)
	}
}

func TestPiPMICUnavailableChannels(t *testing.T) {
	for _, value := range []string{"NaNA", "-1A", "+InfA", "0.1V", "nonsense"} {
		data := strings.Replace(piADCFixture(), "3V3_SYS_A current(1)=0.10000000A", "3V3_SYS_A current(1)="+value, 1)
		readings, err := parsePiPower(data)
		if err == nil || readings[0].Watts != nil || readings[1].Watts == nil {
			t.Fatalf("%s: must preserve core but leave incomplete sum unavailable: %+v %v", value, readings, err)
		}
	}
	readings, err := parsePiPower("VDD_CORE_A current(7)=2A\nVDD_CORE_V volt(15)=0.8V")
	if err == nil || readings[0].Watts != nil || readings[1].Watts == nil || *readings[1].Watts != 1.6 {
		t.Fatal("partial sum was reported as total", readings, err)
	}
	if _, err = parsePiPower(piADCFixture() + "VDD_CORE_A current(7)=1A"); err == nil {
		t.Fatal("ambiguous duplicate channel accepted")
	}
	readings, err = parsePiPower("error=unknown command")
	if err == nil || readings[0].Watts != nil || readings[1].Watts != nil {
		t.Fatal("unsupported command became watts")
	}
}

func TestPiPowerDetectionAndRecovery(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "firmware/devicetree/base/model")
	if err := os.MkdirAll(filepath.Dir(model), 0700); err != nil {
		t.Fatal(err)
	}
	if newPiPowerSource(root) != nil {
		t.Fatal("enabled Pi power without a model")
	}
	for _, name := range []string{"Raspberry Pi 4 Model B Rev 1.5\x00", "Raspberry Pi 5 Model B Rev 1.0\x00"} {
		if err := os.WriteFile(model, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		if (newPiPowerSource(root) != nil) != strings.HasPrefix(name, "Raspberry Pi 5") {
			t.Fatal("wrong model detection", name)
		}
	}
	s := sampler{sysRoot: root, procRoot: root}
	s.piPower = func(context.Context) ([]processorPower, error) { return nil, errors.New("PMIC inaccessible") }
	first := s.sampleLinux(context.Background())
	if len(first.Power) != 2 || first.Power[0].Watts != nil || s.piPowerError == "" {
		t.Fatal("lost unavailable power metadata")
	}
	s.piPower = func(context.Context) ([]processorPower, error) { return parsePiPower(piADCFixture()) }
	next := s.sampleLinux(context.Background())
	if len(next.Power) != 2 || next.Power[0].Watts == nil || s.piPowerError != "" {
		t.Fatal("did not recover from a failed command")
	}
	for _, p := range next.Power {
		if p.Kind == "cpu" || p.Kind == "gpu" {
			t.Fatal("shared rail mislabeled as isolated processor")
		}
	}
	for _, kind := range []string{"cpu", "gpu"} {
		if validPowerScope(kind, "rails") || validPowerScope(kind, "rail") {
			t.Fatal("accepted mislabeled shared power")
		}
	}
	if validPowerScope("system", "package") {
		t.Fatal("accepted mislabeled CPU package")
	}
}

func TestPiPowerLocalHistory(t *testing.T) {
	h, err := openHistory(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.db.Close()
	readings, err := parsePiPower(piADCFixture())
	if err != nil {
		t.Fatal(err)
	}
	a := app{hostname: "artemis", history: h}
	now := time.Now().UTC()
	a.updateLocalTelemetry(telemetry{Power: readings}, now)
	result, err := h.query(context.Background(), controllerHistoryID, "1h", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Processors) != 2 {
		t.Fatal("PMIC metadata missing from history", result.Processors)
	}
	last := result.Points[len(result.Points)-1]
	for _, p := range result.Processors {
		if p.Kind != "system" || last.PowerWatts[p.ID] == nil {
			t.Fatalf("PMIC reading missing from history %+v %+v", p, last.PowerWatts)
		}
	}
}
