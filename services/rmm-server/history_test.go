package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func historyForTest(t *testing.T) *historyStore {
	t.Helper()
	h, err := openHistory(filepath.Join(t.TempDir(), "history & charts.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.db.Close() })
	return h
}

func historySample(at time.Time, value float64) telemetry {
	return telemetry{UpdatedAt: &at, CPUPercent: &value, RAMPercent: &value}
}

func TestHistoryPersistsAndKeepsMissingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	h, err := openHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(15 * time.Second)
	for _, sample := range []telemetry{historySample(now.Add(-44*time.Second), 0), historySample(now.Add(-39*time.Second), 100)} {
		if err := h.record("petra", sample); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.record(controllerHistoryID, historySample(now.Add(-44*time.Second), 90)); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	h, err = openHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.db.Close()
	result, err := h.query(context.Background(), "petra", "1h", now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, point := range result.Points {
		if point.At.Equal(now.Add(-45 * time.Second)) {
			found = true
			if point.CPUPercent == nil || *point.CPUPercent != 50 || point.RAMPercent == nil || *point.RAMPercent != 50 || point.TemperatureC != nil {
				t.Fatalf("wrong average, host isolation, or null handling: %+v", point)
			}
		} else if point.CPUPercent != nil || point.RAMPercent != nil || point.TemperatureC != nil {
			t.Fatal("missing history was filled with a measurement")
		}
	}
	if !found || len(result.Points) != 241 || result.StepSeconds != 15 {
		t.Fatalf("wrong hour window: %d points", len(result.Points))
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("history database is not private")
	}
}

func TestHistoryRateBoundAndRetention(t *testing.T) {
	h := historyForTest(t)
	now := time.Now().UTC().Truncate(15 * time.Second)
	for _, sample := range []telemetry{historySample(now.Add(time.Second), 20), historySample(now.Add(2*time.Second), 30), historySample(now, 10)} {
		if err := h.record("petra", sample); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.record("petra", historySample(now.Add(-49*time.Hour), 99)); err != nil {
		t.Fatal(err)
	}
	if err := h.prune(now); err != nil {
		t.Fatal(err)
	}
	var count int
	var cpu float64
	if err := h.db.QueryRow("SELECT COUNT(*), cpu_percent FROM telemetry_history").Scan(&count, &cpu); err != nil || count != 1 || cpu != 30 {
		t.Fatalf("five-second cap, late-write guard, or retention failed: %d %f %v", count, cpu, err)
	}
	result, err := h.query(context.Background(), "petra", "24h", now.Add(3*time.Second))
	if err != nil || len(result.Points) != 721 || result.StepSeconds != 120 {
		t.Fatalf("day view is not bounded: %d %v", len(result.Points), err)
	}
}

func TestPublicHistoryAPIAndIngestion(t *testing.T) {
	a := receiverApp(t)
	a.history = historyForTest(t)
	a.hostname = "artemis"
	if w := ingest(a, "petra", "wrong", `{"cpu_percent":25}`); w.Code != 401 {
		t.Fatal("unauthorized ingestion accepted")
	}
	if w := ingest(a, "petra", a.telemetryTokens["petra"], `{"cpu_percent":25,"temperature_c":42}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	a.updateLocalTelemetry(historySample(time.Now(), 12), time.Now())
	for _, id := range []string{"petra", controllerHistoryID} {
		w := request(a, "GET", "/api/hosts/"+id+"/history?range=1h", "", nil, false)
		var result historyResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.HostID != id {
			t.Fatalf("public history missing: %d %s", w.Code, w.Body)
		}
		count := 0
		for _, point := range result.Points {
			if point.CPUPercent == nil {
				continue
			}
			count++
			expected := float64(25)
			if id == controllerHistoryID {
				expected = 12
			}
			if *point.CPUPercent != expected {
				t.Fatal("local/remote history mixed")
			}
		}
		if count != 1 || strings.Contains(w.Body.String(), a.telemetryTokens["petra"]) || strings.Contains(w.Body.String(), "F4:93") {
			t.Fatal("unexpected stored samples or private metadata leaked")
		}
	}
	for path, expected := range map[string]int{"/api/hosts/unknown/history": 404, "/api/hosts/petra/history?range=365d": 400} {
		if w := request(a, "GET", path, "", nil, false); w.Code != expected {
			t.Fatalf("%s: expected %d, got %d", path, expected, w.Code)
		}
	}
	a.history = nil
	if w := request(a, "GET", "/api/hosts/petra/history", "", nil, false); w.Code != 503 {
		t.Fatal("missing history storage should return 503")
	}
	if w := request(a, "GET", "/api/hosts", "", nil, false); w.Code != 200 {
		t.Fatal("live telemetry depends on history availability")
	}
}

func TestHistoryConcurrentReadsAndWrites(t *testing.T) {
	h := historyForTest(t)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := h.record("petra", historySample(time.Now(), float64(j))); err != nil {
					t.Error(err)
				}
				if _, err := h.query(context.Background(), "petra", "1h", time.Now()); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestHistoryWriteFailureIsVisibleWithoutBreakingLiveTelemetry(t *testing.T) {
	a := testApp(t)
	a.history = historyForTest(t)
	if _, err := a.history.db.Exec(`CREATE TRIGGER fail_history BEFORE INSERT ON telemetry_history BEGIN SELECT RAISE(FAIL, 'private-storage-error'); END`); err != nil {
		t.Fatal(err)
	}
	a.updateLocalTelemetry(historySample(time.Now(), 42), time.Now())
	if a.serverStatus().Telemetry.CPUPercent == nil || *a.serverStatus().Telemetry.CPUPercent != 42 {
		t.Fatal("history failure broke current telemetry")
	}
	w := request(a, "GET", "/api/hosts/__controller__/history", "", nil, false)
	var result historyResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Warning == "" || strings.Contains(w.Body.String(), "private-storage-error") {
		t.Fatalf("write failure was hidden or leaked diagnostics: %s", w.Body)
	}
	if _, err := a.history.db.Exec("DROP TRIGGER fail_history"); err != nil {
		t.Fatal(err)
	}
	a.updateLocalTelemetry(historySample(time.Now(), 42), time.Now())
	w = request(a, "GET", "/api/hosts/__controller__/history", "", nil, false)
	if strings.Contains(w.Body.String(), `"warning"`) {
		t.Fatal("history write recovery did not clear the warning")
	}
}
