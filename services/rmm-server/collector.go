package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type cpuCounters struct{ total, idle uint64 }
type sampler struct {
	hostname          string
	procRoot, sysRoot string
	previous          *cpuCounters
	iface             string
	gpu               func(context.Context) ([]gpuTelemetry, error)
}

func validInterface(name string) bool {
	return name != "" && len(name) <= 15 && name != "." && name != ".." && !strings.ContainsAny(name, "/\\ \t\r\n\x00")
}
func readCPU(data string) (cpuCounters, error) {
	fields := strings.Fields(strings.SplitN(data, "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounters{}, errors.New("missing aggregate CPU counters")
	}
	var result cpuCounters
	// guest and guest_nice are already included in user/nice. Do not count twice.
	for i := 1; i < len(fields) && i <= 8; i++ {
		value, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return result, err
		}
		result.total += value
		if i == 4 || i == 5 {
			result.idle += value
		}
	}
	return result, nil
}
func cpuUsage(previous, current cpuCounters) *float64 {
	if current.total <= previous.total || current.idle < previous.idle {
		return nil
	}
	total, idle := current.total-previous.total, current.idle-previous.idle
	if idle > total {
		return nil
	}
	result := 100 * float64(total-idle) / float64(total)
	return &result
}
func readMemory(data string) (used, total uint64, err error) {
	values := map[string]uint64{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		if fields[2] != "kB" {
			return 0, 0, errors.New("invalid memory units")
		}
		value, e := strconv.ParseUint(fields[1], 10, 64)
		if e != nil || value > math.MaxUint64/1024 {
			return 0, 0, errors.New("invalid memory counter")
		}
		values[fields[0]] = value * 1024
	}
	total = values["MemTotal:"]
	available, ok := values["MemAvailable:"]
	if total == 0 || !ok || available > total {
		return 0, 0, errors.New("missing or invalid RAM counters")
	}
	return total - available, total, nil
}
func readUint(path string) *uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return nil
	}
	return &value
}
func readTemperature(path string) *float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return nil
	}
	value /= 1000
	if !validFloat(&value, -100, 250) {
		return nil
	}
	return &value
}
func (s *sampler) temperature() *float64 {
	var hottest *float64
	take := func(value *float64) {
		if value != nil && (hottest == nil || *value > *hottest) {
			hottest = value
		}
	}
	zones, _ := filepath.Glob(filepath.Join(s.sysRoot, "class/thermal/thermal_zone*"))
	for _, zone := range zones {
		data, err := os.ReadFile(filepath.Join(zone, "type"))
		if err != nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(string(data)))
		if strings.Contains(kind, "cpu") || strings.Contains(kind, "soc") || kind == "x86_pkg_temp" {
			take(readTemperature(filepath.Join(zone, "temp")))
		}
	}
	// Prefer identifiable CPU sensors; never treat an arbitrary thermal zone as CPU.
	devices, _ := filepath.Glob(filepath.Join(s.sysRoot, "class/hwmon/hwmon*"))
	for _, device := range devices {
		data, err := os.ReadFile(filepath.Join(device, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(data))
		if name != "coretemp" && name != "k10temp" && name != "zenpower" && name != "cpu_thermal" {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(device, "temp*_input"))
		for _, input := range inputs {
			take(readTemperature(input))
		}
	}
	return hottest
}
func (s *sampler) link() *linkTelemetry {
	if !validInterface(s.iface) {
		return nil
	}
	base := filepath.Join(s.sysRoot, "class/net", s.iface)
	if _, err := os.Stat(base); err != nil {
		return nil
	}
	link := &linkTelemetry{Interface: s.iface, SpeedMbps: readUint(filepath.Join(base, "speed")), RXBytes: readUint(filepath.Join(base, "statistics/rx_bytes")), TXBytes: readUint(filepath.Join(base, "statistics/tx_bytes"))}
	link.Type = "ethernet"
	if _, err := os.Stat(filepath.Join(base, "wireless")); err == nil {
		link.Type = "wifi"
	}
	if strings.HasPrefix(s.iface, "tailscale") {
		link.Type = "tailscale"
		link.SpeedMbps = nil
	}
	if link.SpeedMbps != nil && (*link.SpeedMbps == 0 || *link.SpeedMbps > 10000000) {
		link.SpeedMbps = nil
	}
	if carrier := readUint(filepath.Join(base, "carrier")); carrier != nil && *carrier <= 1 {
		value := *carrier == 1
		link.Carrier = &value
	}
	if link.Carrier != nil && !*link.Carrier {
		link.SpeedMbps = nil
	}
	return link
}
func (s *sampler) sampleLinux(ctx context.Context) telemetry {
	var reading telemetry
	reading.Hostname = s.hostname
	if data, err := os.ReadFile(filepath.Join(s.procRoot, "stat")); err == nil {
		if current, err := readCPU(string(data)); err == nil {
			if s.previous != nil {
				reading.CPUPercent = cpuUsage(*s.previous, current)
			}
			s.previous = &current
		}
	}
	if data, err := os.ReadFile(filepath.Join(s.procRoot, "meminfo")); err == nil {
		if used, total, err := readMemory(string(data)); err == nil {
			percent := 100 * float64(used) / float64(total)
			reading.RAMPercent = &percent
			reading.RAMUsedBytes = &used
			reading.RAMTotalBytes = &total
		}
	}
	reading.TemperatureC = s.temperature()
	reading.Link = s.link()
	if s.gpu != nil {
		if gpus, err := s.gpu(ctx); err == nil {
			reading.GPUs = gpus
		}
	}
	return reading
}
func parseGPUFloat(text string) *float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}
func parseGPUMemory(text string) *uint64 {
	value := parseGPUFloat(text)
	if value == nil || *value < 0 || *value >= float64(math.MaxUint64)/(1024*1024) {
		return nil
	}
	bytes := uint64(*value * 1024 * 1024)
	return &bytes
}
func parseGPUs(data string) ([]gpuTelemetry, error) {
	rows, err := csv.NewReader(strings.NewReader(data)).ReadAll()
	if err != nil {
		return nil, err
	}
	var result []gpuTelemetry
	for _, row := range rows {
		if len(row) != 6 {
			return nil, errors.New("unexpected nvidia-smi columns")
		}
		g := gpuTelemetry{ID: strings.TrimSpace(row[0]), Name: strings.TrimSpace(row[1]), TemperatureC: parseGPUFloat(row[2]), UtilizationPercent: parseGPUFloat(row[3]), VRAMUsedBytes: parseGPUMemory(row[4]), VRAMTotalBytes: parseGPUMemory(row[5])}
		if !validMemory(g.VRAMUsedBytes, g.VRAMTotalBytes) {
			g.VRAMUsedBytes = nil
			g.VRAMTotalBytes = nil
		}
		if !validFloat(g.TemperatureC, -100, 250) {
			g.TemperatureC = nil
		}
		if !validFloat(g.UtilizationPercent, 0, 100) {
			g.UtilizationPercent = nil
		}
		result = append(result, g)
	}
	return result, nil
}
func queryGPUs(ctx context.Context) ([]gpuTelemetry, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=uuid,name,temperature.gpu,utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	return parseGPUs(string(data))
}
func newSampler(iface string) *sampler {
	hostname, _ := os.Hostname()
	return &sampler{procRoot: "/proc", sysRoot: "/sys", iface: iface, gpu: queryGPUs, hostname: strings.ToLower(hostname)}
}
func writeSample() {
	s := newSampler("eno1")
	s.sample(context.Background())
	time.Sleep(250 * time.Millisecond)
	reading := s.sample(context.Background())
	if err := json.NewEncoder(os.Stdout).Encode(reading); err != nil {
		log.Fatal(err)
	}
}
func loadCollectorConfig(path string) (collectorConfig, error) {
	var c collectorConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	u, err := collectorURL(c)
	if err != nil {
		return c, err
	}
	if c.Interface == "" {
		_, c.Interface, _ = strings.Cut(u.Hostname(), "%")
	}
	if !validInterface(c.Interface) {
		return c, errors.New("collector interface must name the network interface on this machine")
	}
	if !validLinkType(c.LinkType) {
		return c, errors.New("link_type must be ethernet, wifi, tailscale, or other")
	}
	if zone := strings.SplitN(u.Hostname(), "%", 2); len(zone) == 2 && zone[1] != c.Interface {
		return c, errors.New("collector interface must match the IPv6 URL scope")
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 5
	}
	if c.IntervalSeconds < 1 || c.IntervalSeconds > 10 {
		return c, errors.New("collector interval_seconds must be between 1 and 10")
	}
	return c, nil
}
func postSample(ctx context.Context, client *http.Client, c collectorConfig, reading telemetry) error {
	if err := validateTelemetry(reading); err != nil {
		return err
	}
	endpoint, err := collectorURL(c)
	if err != nil {
		return err
	}
	data, err := json.Marshal(reading)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("telemetry rejected (HTTP %d)", response.StatusCode)
	}
	return nil
}
func runCollector(ctx context.Context, path string, once bool) error {
	c, err := loadCollectorConfig(path)
	if err != nil {
		return err
	}
	autoInterface := c.Interface == "auto"
	if autoInterface {
		c.Interface, err = interfaceForTarget(c.Target)
		if err != nil {
			return err
		}
	}
	if _, err := net.InterfaceByName(c.Interface); err != nil {
		return fmt.Errorf("network interface %s: %w", c.Interface, err)
	}
	// No environment proxies or redirects: telemetry goes directly to the configured
	// Ethernet IP, with a mandatory interface scope for link-local IPv6.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s := newSampler(c.Interface)
	s.sample(ctx) // Establish CPU counters before the first reported sample.
	first := time.NewTimer(time.Second)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return nil
	case <-first.C:
	}
	ticker := time.NewTicker(time.Duration(c.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	var lastError string
	log.Printf("Collecting host %s on interface %s", c.HostID, c.Interface)
	for {
		if autoInterface {
			// Tailscale may start after login or recreate its utun interface on
			// reconnect. Follow the current route instead of pinning the first one.
			iface, err := interfaceForTarget(c.Target)
			if err != nil {
				s.iface = ""
			} else {
				s.iface = iface
			}
		}
		reading := s.sample(ctx)
		if reading.Link != nil && c.LinkType != "" {
			reading.Link.Type = c.LinkType
		}
		if reading.Link != nil && reading.Link.Type == "tailscale" {
			reading.Link.SpeedMbps = nil
		}
		err := postSample(ctx, client, c, reading)
		if once {
			if err == nil {
				log.Print("Telemetry sample accepted.")
			}
			return err
		}
		if err != nil {
			if err.Error() != lastError {
				log.Printf("telemetry send failed: %v (retrying)", err)
			}
			lastError = err.Error()
		} else if lastError != "" {
			log.Print("Telemetry delivery restored")
			lastError = ""
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
