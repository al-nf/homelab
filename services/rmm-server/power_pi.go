package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Pi 5 PMIC output rails with both voltage and current ADCs. EXT5V and
// BATT are voltage-only inputs, so cannot supply an input-power measurement.
var piPowerRails = []string{"3V7_WL_SW", "3V3_SYS", "1V8_SYS", "DDR_VDD2", "DDR_VDDQ", "1V1_SYS", "0V8_SW", "VDD_CORE", "3V3_DAC", "3V3_ADC", "0V8_AON", "HDMI"}

func piPowerReadings() []processorPower {
	return []processorPower{
		{ID: "system/pmic", Name: "Measured rails", Kind: "system", Scope: "rails"},
		{ID: "system/vdd-core", Name: "CPU/GPU core", Kind: "system", Scope: "rail"},
	}
}

func parsePiPower(data string) ([]processorPower, error) {
	channels := map[string]*float64{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		channel := fields[0]
		isCurrent := strings.HasSuffix(channel, "_A")
		isVoltage := strings.HasSuffix(channel, "_V")
		if !isCurrent && !isVoltage {
			continue
		}
		if _, duplicate := channels[channel]; duplicate {
			return nil, fmt.Errorf("duplicate PMIC ADC channel %s", channel)
		}
		channels[channel] = nil
		descriptor, value, ok := strings.Cut(fields[1], "=")
		if !ok {
			continue
		}
		unit := "V"
		prefix := "volt("
		maximum := 20.0
		if isCurrent {
			unit = "A"
			prefix = "current("
			maximum = 100
		}
		if !strings.HasPrefix(descriptor, prefix) || !strings.HasSuffix(descriptor, ")") || !strings.HasSuffix(value, unit) {
			continue
		}
		number, err := strconv.ParseFloat(strings.TrimSuffix(value, unit), 64)
		if err != nil || !validFloat(&number, 0, maximum) {
			continue
		}
		channels[channel] = &number
	}
	result := piPowerReadings()
	total := 0.0
	complete := true
	for _, rail := range piPowerRails {
		current, voltage := channels[rail+"_A"], channels[rail+"_V"]
		if current == nil || voltage == nil {
			complete = false
			continue
		}
		watts := *current * *voltage
		total += watts
		if rail == "VDD_CORE" {
			result[1].Watts = &watts
		}
	}
	if complete {
		result[0].Watts = &total
		return result, nil
	}
	// Preserve a valid core reading, but never label a partial sum as rail power.
	return result, fmt.Errorf("incomplete Pi 5 PMIC ADC readings; run vcgencmd pmic_read_adc as the dashboard user")
}

func newPiPowerSource(sysRoot string) func(context.Context) ([]processorPower, error) {
	model, err := os.ReadFile(filepath.Join(sysRoot, "firmware/devicetree/base/model"))
	if err != nil || !strings.HasPrefix(strings.TrimRight(string(model), "\x00\n"), "Raspberry Pi 5") {
		return nil
	}
	return func(ctx context.Context) ([]processorPower, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		command, err := exec.LookPath("vcgencmd")
		if err != nil {
			// Older Raspberry Pi OS installations kept firmware tools outside PATH.
			command = "/opt/vc/bin/vcgencmd"
			if _, statErr := os.Stat(command); statErr != nil {
				return nil, fmt.Errorf("vcgencmd is unavailable; install Raspberry Pi OS firmware tools")
			}
		}
		cmd := exec.CommandContext(ctx, command, "pmic_read_adc")
		cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("read PMIC ADC: %w; check vcgencmd pmic_read_adc as the dashboard user", err)
		}
		return parsePiPower(string(output))
	}
}
