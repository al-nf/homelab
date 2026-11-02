package main

import (
	"context"
	"time"
)

func (a *app) updateLocalTelemetry(reading telemetry, now time.Time) {
	reading.Hostname = a.hostname
	reading.UpdatedAt = &now
	a.mu.Lock()
	linkRates(a.localReading, &reading, now)
	a.localReading = reading
	a.mu.Unlock()
	a.recordHistory(controllerHistoryID, reading)
}

// The management host samples itself without HTTP, ingestion tokens, or a
// separate collector process. Reuse the same metric sources as remote agents.
func (a *app) collectLocalTelemetry(ctx context.Context, collect func(context.Context) telemetry, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		reading := collect(ctx)
		if ctx.Err() != nil {
			return
		}
		a.updateLocalTelemetry(reading, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
