package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func ingest(a *app, id, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/ingest/"+id, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.telemetryHandler().ServeHTTP(w, r)
	return w
}
func receiverApp(t *testing.T) *app {
	a := testApp(t)
	a.telemetryTokens = map[string]string{"petra": strings.Repeat("a", 64)}
	return a
}
func TestTelemetryAuthenticationAndIsolation(t *testing.T) {
	a := receiverApp(t)
	for _, token := range []string{"", "wrong"} {
		if w := ingest(a, "petra", token, `{"cpu_percent":25}`); w.Code != 401 {
			t.Fatal("unauthorized telemetry accepted")
		}
	}
	if w := ingest(a, "unknown", a.telemetryTokens["petra"], `{"cpu_percent":25}`); w.Code != 401 {
		t.Fatal("unknown target accepted")
	}
	cookie := loginCookie(t, a)
	r := httptest.NewRequest("POST", "/ingest/petra", strings.NewReader(`{"cpu_percent":25}`))
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.telemetryHandler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("browser cookie accepted as collector credential")
	}
	w = request(a, "POST", "/ingest/petra", `{"cpu_percent":25}`, cookie, true)
	if w.Code != 404 {
		t.Fatal("ingestion exposed through public handler")
	}
}
func TestTelemetryValidationAndFreshness(t *testing.T) {
	a := receiverApp(t)
	token := a.telemetryTokens["petra"]
	for _, body := range []string{`{}`, `{"cpu_percent":101}`, `{"temperature_c":1000}`, `{"ram_used_bytes":2,"ram_total_bytes":1}`, `{"cpu_percent":2,"unknown":3}`, `{"cpu_percent":2}{}`, `{"cpu_percent":NaN}`, `{"link":{"interface":"../bad"}}`, strings.Repeat("x", 70<<10)} {
		if w := ingest(a, "petra", token, body); w.Code != 400 {
			t.Fatalf("accepted malformed telemetry %q: %d", body[:min(len(body), 100)], w.Code)
		}
	}
	before := time.Now()
	w := ingest(a, "petra", token, `{"cpu_percent":25,"ram_percent":50,"updated_at":"2000-01-01T00:00:00Z","link":{"interface":"eno1","speed_mbps":1000,"carrier":true}}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	reading := a.readings["petra"]
	if reading.UpdatedAt == nil || reading.UpdatedAt.Before(before) {
		t.Fatal("collector could spoof sample freshness")
	}
	response := request(a, "GET", "/api/hosts", "", nil, false)
	if !strings.Contains(response.Body.String(), `"stale":false`) || !strings.Contains(response.Body.String(), `"speed_mbps":1000`) {
		t.Fatal("missing public reading")
	}
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("token leaked into public response")
	}
	old := time.Now().Add(-31 * time.Second)
	reading.UpdatedAt = &old
	a.readings["petra"] = reading
	response = request(a, "GET", "/api/hosts", "", nil, false)
	if !strings.Contains(response.Body.String(), `"stale":true`) {
		t.Fatal("stale reading not marked")
	}
}

type localTransport struct{ handler http.Handler }

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}
func TestCollectorToDashboard(t *testing.T) {
	a := receiverApp(t)
	client := &http.Client{Transport: localTransport{a.telemetryHandler()}}
	c := collectorConfig{Target: "http://[fe80::1%25eno1]:8081", HostID: "petra", Token: a.telemetryTokens["petra"], Interface: "eno1"}
	cpu := 12.5
	if err := postSample(context.Background(), client, c, telemetry{CPUPercent: &cpu}); err != nil {
		t.Fatal(err)
	}
	w := request(a, "GET", "/api/hosts", "", nil, false)
	if !strings.Contains(w.Body.String(), `"cpu_percent":12.5`) {
		t.Fatal("sample did not reach dashboard")
	}
	c.Token = "wrong-token-that-is-long-enough-to-validate"
	if err := postSample(context.Background(), client, c, telemetry{CPUPercent: &cpu}); err == nil {
		t.Fatal("invalid agent token accepted")
	}
}
func TestConcurrentTelemetryAndReaders(t *testing.T) {
	a := receiverApp(t)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ingest(a, "petra", a.telemetryTokens["petra"], `{"cpu_percent":20}`)
			request(a, "GET", "/api/hosts", "", nil, false)
		}()
	}
	group.Wait()
}
func TestEthernetConfig(t *testing.T) {
	for _, address := range []string{":8081", "0.0.0.0:8081", "[::]:8081", "[fe80::1]:8081", "host:8081"} {
		if validateListen(address) == nil {
			t.Errorf("accepted unsafe listen %s", address)
		}
	}
	if err := validateListen("[fe80::1%eth0]:8081"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	receiver, collector := filepath.Join(dir, "telemetry.json"), filepath.Join(dir, "collector.json")
	if err := initTelemetryConfig(receiver, collector, "[fe80::1%eth0]:8081", "http://[fe80::1%25eno1]:8081", "petra"); err != nil {
		t.Fatal(err)
	}
	c, err := loadCollectorConfig(collector)
	if err != nil {
		t.Fatal(err)
	}
	if c.Interface != "eno1" {
		t.Fatal("wrong sending interface")
	}
	a := testApp(t)
	r, err := loadReceiverConfig(receiver, a.hosts)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != r.Tokens["petra"] {
		t.Fatal("receiver and collector tokens differ")
	}
	for _, path := range []string{receiver, collector} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("config not private")
		}
	}
	if err := initTelemetryConfig(receiver, collector, r.Listen, c.Target, "petra"); err == nil {
		t.Fatal("configuration overwritten")
	}
	for _, target := range []string{"http://example.com:8081", "http://[fe80::1]:8081", "http://[fe80::1%25eno1]:8081/path", "http://user:pass@127.0.0.1:8081"} {
		c.Target = target
		if _, err := collectorURL(c); err == nil {
			t.Errorf("accepted invalid target %s", target)
		}
	}
}
func TestLinuxCounters(t *testing.T) {
	before, err := readCPU("cpu 100 10 20 200 30 10 20 10 80 10\n")
	if err != nil {
		t.Fatal(err)
	}
	if before.total != 400 || before.idle != 230 {
		t.Fatal("guest CPU time counted twice")
	}
	after := cpuCounters{total: 500, idle: 280}
	if value := cpuUsage(before, after); value == nil || *value != 50 {
		t.Fatal("CPU delta incorrect")
	}
	if cpuUsage(after, before) != nil {
		t.Fatal("reset counters accepted")
	}
	used, total, err := readMemory("MemTotal: 1000 kB\nMemAvailable: 600 kB\nMemFree: 100 kB\n")
	if err != nil || used != 400*1024 || total != 1000*1024 {
		t.Fatal("available memory not used")
	}
	if _, _, err := readMemory("MemTotal: 1000 kB\n"); err == nil {
		t.Fatal("missing available memory accepted")
	}
}
func TestFilesystemCollector(t *testing.T) {
	root := t.TempDir()
	write := func(path, data string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/stat", "cpu 10 0 0 90 0 0 0 0\n")
	write("proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 600 kB\n")
	write("sys/class/thermal/thermal_zone0/type", "x86_pkg_temp")
	write("sys/class/thermal/thermal_zone0/temp", "42000")
	write("sys/class/thermal/thermal_zone1/type", "acpitz")
	write("sys/class/thermal/thermal_zone1/temp", "90000")
	write("sys/class/net/eno1/speed", "1000")
	write("sys/class/net/eno1/carrier", "1")
	write("sys/class/net/eno1/statistics/rx_bytes", "100")
	write("sys/class/net/eno1/statistics/tx_bytes", "200")
	s := sampler{procRoot: filepath.Join(root, "proc"), sysRoot: filepath.Join(root, "sys"), iface: "eno1", gpu: func(context.Context) ([]gpuTelemetry, error) { return nil, errors.New("GPU unavailable") }}
	first := s.sample(context.Background())
	if first.CPUPercent != nil {
		t.Fatal("CPU usage invented without baseline")
	}
	write("proc/stat", "cpu 60 0 0 140 0 0 0 0\n")
	reading := s.sample(context.Background())
	if reading.CPUPercent == nil || *reading.CPUPercent != 50 || reading.TemperatureC == nil || *reading.TemperatureC != 42 {
		t.Fatal("wrong sampled values")
	}
	if reading.Link == nil || reading.Link.SpeedMbps == nil || *reading.Link.SpeedMbps != 1000 {
		t.Fatal("missing negotiated speed")
	}
	if err := validateTelemetry(reading); err != nil {
		t.Fatal(err)
	}
	write("sys/class/net/eno1/speed", "-1")
	write("sys/class/net/eno1/carrier", "0")
	reading = s.sample(context.Background())
	if reading.Link.SpeedMbps != nil || reading.Link.Carrier == nil || *reading.Link.Carrier {
		t.Fatal("down link has speed")
	}
}
func TestNVIDIAParser(t *testing.T) {
	gpus, err := parseGPUs("GPU-123, NVIDIA Example, 45, 12, 512, 8192\nGPU-456, Other GPU, [N/A], [N/A], [N/A], [N/A]\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 || *gpus[0].VRAMUsedBytes != 512*1024*1024 || gpus[1].VRAMUsedBytes != nil {
		t.Fatal("incorrect GPU values")
	}
	if err := validateTelemetry(telemetry{GPUs: gpus}); err != nil {
		t.Fatal(err)
	}
}
func TestCorruptCollectorConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "collector.json")
	c := collectorConfig{Target: "http://[fe80::1%25eno1]:8081", HostID: "petra", Token: strings.Repeat("a", 64), Interface: "eth0"}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCollectorConfig(p); err == nil {
		t.Fatal("mismatching interface and zone accepted")
	}
}

func TestReportedHostnames(t *testing.T) {
	a := receiverApp(t)
	a.hosts[0].Name = "PETRA"
	response := request(a, "GET", "/api/hosts", "", nil, false)
	if !strings.Contains(response.Body.String(), `"name":"petra"`) {
		t.Fatal("configured fallback name not lowercased")
	}
	w := ingest(a, "petra", a.telemetryTokens["petra"], `{"hostname":"PETRA.EXAMPLE.","cpu_percent":20}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	response = request(a, "GET", "/api/hosts", "", nil, false)
	if !strings.Contains(response.Body.String(), `"name":"petra.example"`) || !strings.Contains(response.Body.String(), `"id":"petra"`) {
		t.Fatal("reported hostname not normalized or stable target ID lost")
	}
	w = ingest(a, "petra", a.telemetryTokens["petra"], `{"hostname":"bad hostname","cpu_percent":20}`)
	if w.Code != 400 {
		t.Fatal("invalid hostname accepted")
	}
}

func TestMeasuredTrafficRates(t *testing.T) {
	now := time.Now()
	last := now.Add(-2 * time.Second)
	oldRX, newRX, oldTX, newTX := uint64(1000), uint64(3000), uint64(2000), uint64(3000)
	up := true
	previous := telemetry{UpdatedAt: &last, Link: &linkTelemetry{Interface: "eno1", RXBytes: &oldRX, TXBytes: &oldTX, Carrier: &up}}
	current := telemetry{Link: &linkTelemetry{Interface: "eno1", RXBytes: &newRX, TXBytes: &newTX, Carrier: &up}}
	linkRates(previous, &current, now)
	if current.Link.RXBPS == nil || *current.Link.RXBPS != 8000 || current.Link.TXBPS == nil || *current.Link.TXBPS != 4000 {
		t.Fatal("incorrect bit rates")
	}
	current.Link.RXBytes = &oldRX
	previous.Link.RXBytes = &newRX
	linkRates(previous, &current, now)
	if current.Link.RXBPS != nil {
		t.Fatal("counter reset generated a rate")
	}
	old := now.Add(-time.Minute)
	previous.UpdatedAt = &old
	linkRates(previous, &current, now)
	if current.Link.TXBPS != nil {
		t.Fatal("rate calculated across a stale interval")
	}
	previous.UpdatedAt = &last
	current.Link.Interface = "eth1"
	linkRates(previous, &current, now)
	if current.Link.TXBPS != nil {
		t.Fatal("rate calculated across different interfaces")
	}
	forged := 1234.0
	current.Link.TXBPS = &forged
	linkRates(telemetry{}, &current, now)
	if current.Link.TXBPS != nil {
		t.Fatal("collector supplied rates were trusted")
	}
}

func TestPublicDeploymentStatus(t *testing.T) {
	a := testApp(t)
	a.buildID = "binary-fingerprint"
	a.hostname = "ARTEMIS"
	a.release = "release-123"
	a.startedAt = time.Now().Add(-65 * time.Second).UTC()
	w := request(a, "GET", "/api/health", "", nil, false)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var status serverStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Hostname != "artemis" || status.Build != a.buildID || status.Release != a.release || status.UptimeSeconds < 65 || status.UptimeSeconds > 70 || !status.StartedAt.Equal(a.startedAt) {
		t.Fatal("missing deployment metadata")
	}
	if strings.Contains(w.Body.String(), a.password) {
		t.Fatal("deployment status leaks credentials")
	}
}

func TestConnectionTypes(t *testing.T) {
	for _, kind := range []string{"ethernet", "wifi", "tailscale", "other", ""} {
		if err := validateTelemetry(telemetry{Link: &linkTelemetry{Type: kind, Interface: "wlan0"}}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if err := validateTelemetry(telemetry{Link: &linkTelemetry{Type: "invalid", Interface: "wlan0"}}); err == nil {
		t.Fatal("unknown connection type accepted")
	}
	root := t.TempDir()
	base := filepath.Join(root, "class/net/wlan0/wireless")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	s := sampler{sysRoot: root, iface: "wlan0"}
	link := s.link()
	if link == nil || link.Type != "wifi" || link.SpeedMbps != nil {
		t.Fatal("Wi-Fi interface mislabeled or speed fabricated")
	}
}

func TestMigrateTelemetryToTailscale(t *testing.T) {
	dir := t.TempDir()
	receiver, collector := filepath.Join(dir, "telemetry.json"), filepath.Join(dir, "collector.json")
	if err := initTelemetryConfig(receiver, collector, "[fe80::1%eth0]:8081", "http://[fe80::1%25eno1]:8081", "petra"); err != nil {
		t.Fatal(err)
	}
	original, err := loadCollectorConfig(collector)
	if err != nil {
		t.Fatal(err)
	}
	var before receiverConfig
	data, _ := os.ReadFile(receiver)
	if err := json.Unmarshal(data, &before); err != nil {
		t.Fatal(err)
	}
	before.Tokens["laptop"] = strings.Repeat("b", 64)
	data, _ = json.Marshal(before)
	if err := os.WriteFile(receiver, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureTelemetryConfig(receiver, collector, "100.98.188.45:8081", "http://100.98.188.45:8081", "petra", "tailscale0", "tailscale", true); err != nil {
		t.Fatal(err)
	}
	after, err := loadCollectorConfig(collector)
	if err != nil {
		t.Fatal(err)
	}
	if after.Token != original.Token || after.Interface != "tailscale0" || after.LinkType != "tailscale" || after.Target != "http://100.98.188.45:8081" {
		t.Fatal("transport migration lost token or kept Ethernet settings")
	}
	data, _ = os.ReadFile(receiver)
	var saved receiverConfig
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tokens["laptop"] != before.Tokens["laptop"] || saved.Listen != "100.98.188.45:8081" {
		t.Fatal("unrelated host token lost or listener unchanged")
	}
	if err := configureTelemetryConfig(receiver, collector, saved.Listen, after.Target, "petra", "tailscale0", "tailscale", true); err != nil {
		t.Fatal(err)
	}
	again, err := loadCollectorConfig(collector)
	if err != nil || again.Token != original.Token {
		t.Fatal("repeated setup rotated credentials")
	}
}

func TestVirtualLinkHasNoPhysicalSpeed(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "class/net/tailscale0")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "speed"), []byte("10"), 0600); err != nil {
		t.Fatal(err)
	}
	s := sampler{sysRoot: root, iface: "tailscale0"}
	link := s.link()
	if link == nil || link.Type != "tailscale" || link.SpeedMbps != nil {
		t.Fatal("virtual link exposed a synthetic NIC speed")
	}
}
