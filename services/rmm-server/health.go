package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type serverStatus struct {
	Build          string    `json:"build"`
	Hostname       string    `json:"hostname"`
	StartedAt      time.Time `json:"started_at"`
	UptimeSeconds  int64     `json:"uptime_seconds"`
	Release        string    `json:"release"`
	Telemetry      telemetry `json:"telemetry"`
	TelemetryStale bool      `json:"telemetry_stale"`
}

func (a *app) serverStatus() serverStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	uptime := int64(0)
	if !a.startedAt.IsZero() {
		uptime = max(0, int64(time.Since(a.startedAt).Seconds()))
	}
	stale := a.localReading.UpdatedAt == nil || time.Since(*a.localReading.UpdatedAt) > telemetryStaleAfter
	return serverStatus{Build: a.buildID, Hostname: strings.ToLower(a.hostname), StartedAt: a.startedAt, UptimeSeconds: uptime, Release: a.release, Telemetry: a.localReading, TelemetryStale: stale}
}

func executableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func checkHealth() error {
	expected, err := executableDigest()
	if err != nil {
		return err
	}
	address := os.Getenv("HOMELAB_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	var lastError error
	for attempt := 0; attempt < 20; attempt++ {
		response, err := client.Get("http://" + address + "/api/health")
		if err == nil {
			var data struct {
				Build string `json:"build"`
			}
			err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&data)
			response.Body.Close()
			if response.StatusCode == 200 && err == nil && data.Build == expected {
				return nil
			}
			switch {
			case response.StatusCode != http.StatusOK:
				lastError = fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
			case err != nil:
				lastError = fmt.Errorf("invalid health response: %w", err)
			default:
				lastError = fmt.Errorf("a different dashboard build is already responding on %s", address)
			}
		} else {
			lastError = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("dashboard did not become ready at %s: %w", address, lastError)
}
