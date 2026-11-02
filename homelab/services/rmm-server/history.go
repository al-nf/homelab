package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const controllerHistoryID = "__controller__"
const historyRetention = 48 * time.Hour

type historyStore struct {
	db        *sql.DB
	mu        sync.Mutex
	lastPrune time.Time
	lastError string
}

type historyPoint struct {
	At           time.Time `json:"at"`
	CPUPercent   *float64  `json:"cpu_percent"`
	RAMPercent   *float64  `json:"ram_percent"`
	TemperatureC *float64  `json:"temperature_c"`
}

type historyResponse struct {
	HostID      string         `json:"host_id"`
	Range       string         `json:"range"`
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	StepSeconds int64          `json:"step_seconds"`
	Points      []historyPoint `json:"points"`
	Warning     string         `json:"warning,omitempty"`
}

func openHistory(path string) (*historyStore, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = file.Chmod(0600)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout = 2000", "PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL",
		`CREATE TABLE IF NOT EXISTS telemetry_history (
host_id TEXT NOT NULL, bucket INTEGER NOT NULL, observed_ms INTEGER NOT NULL,
cpu_percent REAL, ram_percent REAL, temperature_c REAL,
PRIMARY KEY (host_id, bucket)) WITHOUT ROWID`,
		"CREATE INDEX IF NOT EXISTS history_observed ON telemetry_history(observed_ms)",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	h := &historyStore{db: db}
	if err := h.prune(time.Now()); err != nil {
		db.Close()
		return nil, err
	}
	return h, nil
}

func (h *historyStore) prune(now time.Time) error {
	_, err := h.db.Exec("DELETE FROM telemetry_history WHERE observed_ms < ?", now.Add(-historyRetention).UnixMilli())
	if err == nil {
		h.lastPrune = now
	}
	return err
}

func (h *historyStore) record(id string, reading telemetry) error {
	if reading.UpdatedAt == nil {
		return fmt.Errorf("history sample has no receive timestamp")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	at := reading.UpdatedAt.UnixMilli()
	// Bound storage to one sample per five seconds per host. Upserts also make
	// repeated reports idempotent and prevent late writes replacing newer ones.
	_, err := h.db.Exec(`INSERT INTO telemetry_history
(host_id, bucket, observed_ms, cpu_percent, ram_percent, temperature_c) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(host_id, bucket) DO UPDATE SET observed_ms=excluded.observed_ms,
cpu_percent=excluded.cpu_percent, ram_percent=excluded.ram_percent, temperature_c=excluded.temperature_c
WHERE excluded.observed_ms >= telemetry_history.observed_ms`,
		id, at/5000, at, reading.CPUPercent, reading.RAMPercent, reading.TemperatureC)
	if err == nil && time.Since(h.lastPrune) >= time.Hour {
		err = h.prune(time.Now())
	}
	return err
}

func historyWindow(span string) (time.Duration, time.Duration, bool) {
	switch span {
	case "1h":
		return time.Hour, 15 * time.Second, true
	case "24h":
		return 24 * time.Hour, 2 * time.Minute, true
	default:
		return 0, 0, false
	}
}

func (h *historyStore) query(ctx context.Context, id, span string, now time.Time) (historyResponse, error) {
	window, step, ok := historyWindow(span)
	if !ok {
		return historyResponse{}, fmt.Errorf("unsupported history range")
	}
	stepMS := step.Milliseconds()
	first := now.Add(-window).UnixMilli() / stepMS
	last := now.UnixMilli() / stepMS
	result := historyResponse{HostID: id, Range: span, From: time.UnixMilli(first * stepMS).UTC(), To: now.UTC(), StepSeconds: int64(step.Seconds()), Points: make([]historyPoint, last-first+1)}
	for i := range result.Points {
		result.Points[i].At = time.UnixMilli((first + int64(i)) * stepMS).UTC()
	}
	rows, err := h.db.QueryContext(ctx, `SELECT observed_ms / ?, AVG(cpu_percent), AVG(ram_percent), AVG(temperature_c)
FROM telemetry_history WHERE host_id = ? AND observed_ms >= ? AND observed_ms <= ?
GROUP BY observed_ms / ? ORDER BY observed_ms / ?`, stepMS, id, first*stepMS, now.UnixMilli(), stepMS, stepMS)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket int64
		var cpu, ram, temperature *float64
		if err := rows.Scan(&bucket, &cpu, &ram, &temperature); err != nil {
			return result, err
		}
		index := bucket - first
		if index >= 0 && index < int64(len(result.Points)) {
			result.Points[index].CPUPercent, result.Points[index].RAMPercent, result.Points[index].TemperatureC = cpu, ram, temperature
		}
	}
	return result, rows.Err()
}

func (a *app) recordHistory(id string, reading telemetry) {
	if a.history == nil {
		return
	}
	err := a.history.record(id, reading)
	a.history.mu.Lock()
	defer a.history.mu.Unlock()
	if err != nil {
		if err.Error() != a.history.lastError {
			log.Printf("record telemetry history: %v", err)
			a.history.lastError = err.Error()
		}
	} else if a.history.lastError != "" {
		log.Print("Telemetry history writes recovered.")
		a.history.lastError = ""
	}
}

func (a *app) hostHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	known := id == controllerHistoryID
	for _, h := range a.hosts {
		known = known || id == h.ID
	}
	if !known {
		fail(w, 404, "Machine not found.")
		return
	}
	span := r.URL.Query().Get("range")
	if span == "" {
		span = "1h"
	}
	if _, _, ok := historyWindow(span); !ok {
		fail(w, 400, "Choose the 1h or 24h history range.")
		return
	}
	if a.history == nil {
		fail(w, 503, "History is unavailable.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := a.history.query(ctx, id, span, time.Now())
	if err != nil {
		log.Printf("read telemetry history: %v", err)
		fail(w, 503, "History could not be loaded.")
		return
	}
	a.history.mu.Lock()
	if a.history.lastError != "" {
		result.Warning = "New readings could not be saved."
	}
	a.history.mu.Unlock()
	writeJSON(w, 200, result)
}
