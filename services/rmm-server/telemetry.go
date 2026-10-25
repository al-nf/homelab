package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const telemetryStaleAfter = 30 * time.Second

type gpuTelemetry struct {
	Name               string   `json:"name"`
	ID                 string   `json:"id"`
	UtilizationPercent *float64 `json:"utilization_percent"`
	TemperatureC       *float64 `json:"temperature_c"`
	VRAMUsedBytes      *uint64  `json:"vram_used_bytes"`
	VRAMTotalBytes     *uint64  `json:"vram_total_bytes"`
}
type receiverConfig struct {
	Listen string            `json:"listen"`
	Tokens map[string]string `json:"tokens"`
}
type collectorConfig struct {
	Target          string `json:"target"`
	HostID          string `json:"host_id"`
	Token           string `json:"token"`
	IntervalSeconds int    `json:"interval_seconds"`
	Interface       string `json:"interface"`
	LinkType        string `json:"link_type,omitempty"`
}
type linkTelemetry struct {
	Type      string   `json:"type,omitempty"`
	Interface string   `json:"interface"`
	SpeedMbps *uint64  `json:"speed_mbps"`
	Carrier   *bool    `json:"carrier"`
	RXBytes   *uint64  `json:"rx_bytes"`
	TXBytes   *uint64  `json:"tx_bytes"`
	RXBPS     *float64 `json:"rx_bps"`
	TXBPS     *float64 `json:"tx_bps"`
}

func validLinkType(kind string) bool {
	switch kind {
	case "", "ethernet", "wifi", "tailscale", "other":
		return true
	}
	return false
}

func validHostname(name string) bool {
	if len(name) > 253 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func trafficRate(previous, current *uint64, seconds float64) *float64 {
	if previous == nil || current == nil || *current < *previous || seconds <= 0 || seconds > telemetryStaleAfter.Seconds() {
		return nil
	}
	rate := float64(*current-*previous) * 8 / seconds
	return &rate
}

func linkRates(previous telemetry, current *telemetry, now time.Time) {
	if current.Link == nil {
		return
	}
	current.Link.RXBPS = nil
	current.Link.TXBPS = nil
	if previous.Link == nil || previous.UpdatedAt == nil || previous.Link.Interface != current.Link.Interface || previous.Link.Type != current.Link.Type {
		return
	}
	if current.Link.Carrier != nil && !*current.Link.Carrier || previous.Link.Carrier != nil && !*previous.Link.Carrier {
		return
	}
	seconds := now.Sub(*previous.UpdatedAt).Seconds()
	current.Link.RXBPS = trafficRate(previous.Link.RXBytes, current.Link.RXBytes, seconds)
	current.Link.TXBPS = trafficRate(previous.Link.TXBytes, current.Link.TXBytes, seconds)
}

func configPath(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(dir, "homelab", name)
}
func validateListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("telemetry listen must be a literal IP address and port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("invalid telemetry port")
	}
	ipPart, zone, _ := strings.Cut(host, "%")
	ip := net.ParseIP(ipPart)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return errors.New("bind telemetry to a specific interface IP address, not a wildcard or hostname")
	}
	if ip.IsLinkLocalUnicast() && ip.To4() == nil && zone == "" {
		return errors.New("IPv6 link-local listen address needs the Pi interface, e.g. %eth0")
	}
	return nil
}
func collectorURL(c collectorConfig) (*url.URL, error) {
	u, err := url.Parse(c.Target)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("collector target must be an HTTP network base URL")
	}
	hostname, zone, _ := strings.Cut(u.Hostname(), "%")
	ip := net.ParseIP(hostname)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return nil, errors.New("collector target must use the Pi's literal receiver IP, not a hostname")
	}
	if ip.IsLinkLocalUnicast() && ip.To4() == nil && zone == "" {
		return nil, errors.New("IPv6 link-local target needs petra's interface scope, e.g. %25eno1 in the URL")
	}
	if c.HostID == "" || strings.ContainsAny(c.HostID, "/ ") {
		return nil, errors.New("collector host_id is required and must contain no slash or space")
	}
	if len(c.Token) < 32 {
		return nil, errors.New("collector token must have at least 32 characters")
	}
	u.Path = "/ingest/" + c.HostID
	return u, nil
}
func initTelemetryConfig(receiverPath, collectorPath, listen, target, id string) error {
	return configureTelemetryConfig(receiverPath, collectorPath, listen, target, id, "", "", false)
}

// Reconfiguration preserves per-host tokens, including credentials for other hosts.
func configureTelemetryConfig(receiverPath, collectorPath, listen, target, id, iface, kind string, replace bool) error {
	if err := validateListen(listen); err != nil {
		return err
	}
	if !validLinkType(kind) {
		return errors.New("invalid collector link type")
	}
	if !replace {
		for _, path := range []string{receiverPath, collectorPath} {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("configuration already exists: %s", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	receiver := receiverConfig{Listen: listen, Tokens: map[string]string{}}
	collector := collectorConfig{HostID: id, IntervalSeconds: 5}
	if replace {
		if data, err := os.ReadFile(receiverPath); err == nil {
			if err = json.Unmarshal(data, &receiver); err != nil {
				return fmt.Errorf("read existing receiver config: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if data, err := os.ReadFile(collectorPath); err == nil {
			if err = json.Unmarshal(data, &collector); err != nil {
				return fmt.Errorf("read existing collector config: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if receiver.Tokens == nil {
		receiver.Tokens = map[string]string{}
	}
	token := receiver.Tokens[id]
	if token == "" {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		token = hex.EncodeToString(random)
	}
	collector.Target = target
	collector.HostID = id
	collector.Token = token
	collector.LinkType = kind
	if collector.IntervalSeconds == 0 {
		collector.IntervalSeconds = 5
	}
	if collector.IntervalSeconds < 1 || collector.IntervalSeconds > 10 {
		return errors.New("collector interval_seconds must be between 1 and 10")
	}
	u, err := collectorURL(collector)
	if err != nil {
		return err
	}
	if iface == "" {
		_, iface, _ = strings.Cut(u.Hostname(), "%")
	}
	if !validInterface(iface) {
		return errors.New("collector-interface is required for unscoped target addresses")
	}
	collector.Interface = iface
	receiver.Listen = listen
	receiver.Tokens[id] = token
	a, _ := json.MarshalIndent(receiver, "", "  ")
	b, _ := json.MarshalIndent(collector, "", "  ")
	if err := writePrivateFile(receiverPath, a); err != nil {
		return err
	}
	return writePrivateFile(collectorPath, b)
}
func loadReceiverConfig(path string, hosts []host) (receiverConfig, error) {
	var c receiverConfig
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if err = validateListen(c.Listen); err != nil {
		return c, err
	}
	configured := map[string]bool{}
	for _, h := range hosts {
		configured[h.ID] = true
	}
	if len(c.Tokens) == 0 {
		return c, errors.New("telemetry receiver needs at least one host token")
	}
	for id, token := range c.Tokens {
		if !configured[id] || len(token) < 32 {
			return c, fmt.Errorf("invalid telemetry token or unknown host: %s", id)
		}
	}
	return c, nil
}
func validFloat(value *float64, min, max float64) bool {
	return value == nil || !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= min && *value <= max
}
func validMemory(used, total *uint64) bool {
	return (used == nil && total == nil) || (used != nil && total != nil && *total > 0 && *used <= *total)
}
func validateTelemetry(t telemetry) error {
	if !validHostname(t.Hostname) {
		return errors.New("invalid hostname")
	}
	if !validFloat(t.CPUPercent, 0, 100) || !validFloat(t.RAMPercent, 0, 100) || !validFloat(t.TemperatureC, -100, 250) || !validMemory(t.RAMUsedBytes, t.RAMTotalBytes) || len(t.GPUs) > 16 {
		return errors.New("invalid telemetry values")
	}
	if t.CPUPercent == nil && t.RAMPercent == nil && t.TemperatureC == nil && t.RAMTotalBytes == nil && len(t.GPUs) == 0 && t.Link == nil {
		return errors.New("at least one metric is required")
	}
	if t.Link != nil {
		if !validLinkType(t.Link.Type) || !validInterface(t.Link.Interface) || (t.Link.SpeedMbps != nil && (*t.Link.SpeedMbps == 0 || *t.Link.SpeedMbps > 10000000)) {
			return errors.New("invalid network link")
		}
	}
	seen := map[string]bool{}
	for _, g := range t.GPUs {
		if g.ID == "" || len(g.ID) > 128 || len(g.Name) > 128 || seen[g.ID] || !validFloat(g.UtilizationPercent, 0, 100) || !validFloat(g.TemperatureC, -100, 250) || !validMemory(g.VRAMUsedBytes, g.VRAMTotalBytes) {
			return errors.New("invalid GPU values")
		}
		seen[g.ID] = true
	}
	return nil
}
func (a *app) telemetryHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		token, ok := a.telemetryTokens[id]
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		expectedHash, providedHash := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(provided))
		if !ok || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
			fail(w, 401, "Invalid collector credentials.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var reading telemetry
		if err := decoder.Decode(&reading); err != nil {
			fail(w, 400, "Invalid telemetry JSON.")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			fail(w, 400, "Expected one telemetry sample.")
			return
		}
		if err := validateTelemetry(reading); err != nil {
			fail(w, 400, err.Error())
			return
		}
		now := time.Now().UTC()
		reading.Hostname = strings.ToLower(strings.TrimSuffix(reading.Hostname, "."))
		reading.UpdatedAt = &now
		a.mu.Lock()
		if a.readings == nil {
			a.readings = map[string]telemetry{}
		}
		linkRates(a.readings[id], &reading, now)
		a.readings[id] = reading
		a.mu.Unlock()
		a.recordHistory(id, reading)
		writeJSON(w, 200, map[string]bool{"accepted": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func startTelemetryServer(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("telemetry listen: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("telemetry receiver stopped: %v", err)
		}
	}()
	log.Printf("Telemetry receiver listening on %s", address)
	return nil
}
