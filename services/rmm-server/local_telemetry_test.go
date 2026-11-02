package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLocalTelemetryPublicStatusAndIsolation(t *testing.T) {
	a := receiverApp(t)
	a.hostname = "artemis"
	if !a.serverStatus().TelemetryStale {
		t.Fatal("missing local sample must be stale")
	}
	value := 42.5
	now := time.Now().UTC()
	a.updateLocalTelemetry(telemetry{Hostname: "wrong", TemperatureC: &value, CPUPercent: &value, RAMPercent: &value}, now)
	w := request(a, "GET", "/api/health", "", nil, false)
	var status serverStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || status.TelemetryStale || status.Telemetry.Hostname != "artemis" || status.Telemetry.TemperatureC == nil || *status.Telemetry.TemperatureC != value {
		t.Fatalf("missing public local telemetry: %s", w.Body)
	}
	if w := ingest(a, "petra", a.telemetryTokens["petra"], `{"cpu_percent":12}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if *a.serverStatus().Telemetry.CPUPercent != value || *a.readings["petra"].CPUPercent != 12 {
		t.Fatal("local and remote readings were mixed")
	}
	a.updateLocalTelemetry(telemetry{}, now.Add(-telemetryStaleAfter-time.Second))
	if !a.serverStatus().TelemetryStale {
		t.Fatal("old local sample must be stale")
	}
}

func TestLocalTelemetryStopsWithServer(t *testing.T) {
	a := testApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.collectLocalTelemetry(ctx, func(context.Context) telemetry {
		cancel()
		return telemetry{}
	}, time.Hour)
	if a.localReading.UpdatedAt != nil {
		t.Fatal("sample published after shutdown")
	}
}
