package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var assets embed.FS

type host struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MAC         string `json:"mac"`
	Broadcast   string `json:"broadcast"`
}

// A nil metric means no collector could report a value.
type telemetry struct {
	Hostname      string         `json:"hostname,omitempty"`
	CPUPercent    *float64       `json:"cpu_percent"`
	RAMPercent    *float64       `json:"ram_percent"`
	TemperatureC  *float64       `json:"temperature_c"`
	UpdatedAt     *time.Time     `json:"updated_at"`
	RAMUsedBytes  *uint64        `json:"ram_used_bytes"`
	RAMTotalBytes *uint64        `json:"ram_total_bytes"`
	GPUs          []gpuTelemetry `json:"gpus"`
	Link          *linkTelemetry `json:"link"`
}
type session struct {
	Expires time.Time `json:"expires"`
}
type app struct {
	hosts           []host
	password        string
	secure          bool
	sessionFile     string
	mu              sync.Mutex
	sessions        map[string]session
	attempts        []time.Time
	lastWake        map[string]time.Time
	send            func(host) error
	readings        map[string]telemetry
	telemetryTokens map[string]string
	buildID         string
	hostname        string
	startedAt       time.Time
	release         string
	localReading    telemetry
	history         *historyStore
}

func loadHosts(path string) ([]host, error) {
	hosts := []host{{ID: "petra", Name: "petra", Description: "Ethernet · Wake on LAN", MAC: "F4:93:9F:F5:7B:CC", Broadcast: "255.255.255.255:9"}}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		hosts = nil // Do not inherit default fields when decoding shorter host records.
		if err := json.Unmarshal(data, &hosts); err != nil {
			return nil, err
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("configure at least one host")
	}
	seen := map[string]bool{}
	for i := range hosts {
		h := &hosts[i]
		if h.ID == "" || h.Name == "" || strings.ContainsAny(h.ID, "/ ") || seen[h.ID] {
			return nil, errors.New("host IDs must be unique, nonempty and contain no slash or space; names are required")
		}
		seen[h.ID] = true
		if h.MAC == "" {
			if h.Broadcast != "" {
				return nil, fmt.Errorf("broadcast requires an Ethernet MAC for %s", h.ID)
			}
			continue
		}
		mac, err := net.ParseMAC(h.MAC)
		if err != nil || len(mac) != 6 {
			return nil, fmt.Errorf("invalid Ethernet MAC for %s", h.ID)
		}
		if h.Broadcast == "" {
			h.Broadcast = "255.255.255.255:9"
		}
		addr, err := net.ResolveUDPAddr("udp4", h.Broadcast)
		if err != nil || addr.IP.To4() == nil || addr.Port < 1 {
			return nil, fmt.Errorf("invalid IPv4 broadcast address for %s", h.ID)
		}
	}
	return hosts, nil
}
func magicPacket(mac string) ([]byte, error) {
	addr, err := net.ParseMAC(mac)
	if err != nil || len(addr) != 6 {
		return nil, errors.New("invalid Ethernet MAC")
	}
	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 255
	}
	for i := 6; i < len(packet); i += 6 {
		copy(packet[i:], addr)
	}
	return packet, nil
}
func sendWake(h host) error {
	packet, err := magicPacket(h.MAC)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("udp4", h.Broadcast, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err = conn.Write(packet)
	return err
}
func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}
func (a *app) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie("homelab_session")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[sessionKey(cookie.Value)]
	return a.password != "" && ok && time.Now().Before(s.Expires)
}
func (a *app) cookie(w http.ResponseWriter, token string, maxAge int) {
	expires := time.Now().Add(time.Duration(maxAge) * time.Second)
	if maxAge < 0 {
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: "homelab_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires})
}
func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if a.password == "" {
		fail(w, 503, "Login is not configured on this server.")
		return
	}
	a.mu.Lock()
	now := time.Now()
	recent := a.attempts[:0]
	for _, t := range a.attempts {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	a.attempts = recent
	if len(recent) >= 10 {
		a.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "Too many login attempts. Try again in a minute.")
		return
	}
	a.attempts = append(a.attempts, now)
	a.mu.Unlock()
	var body struct {
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		fail(w, 400, "Invalid login request.")
		return
	}
	expected, actual := sha256.Sum256([]byte(a.password)), sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
		fail(w, 401, "Incorrect password.")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		fail(w, 500, "Could not create session.")
		return
	}
	token := hex.EncodeToString(tokenBytes)
	a.mu.Lock()
	for key, s := range a.sessions {
		if now.After(s.Expires) {
			delete(a.sessions, key)
		}
	}
	previous := cloneSessions(a.sessions)
	if old, err := r.Cookie("homelab_session"); err == nil {
		delete(a.sessions, sessionKey(old.Value))
	}
	// Bound memory even if a valid user repeatedly creates sessions.
	if len(a.sessions) >= 100 {
		a.sessions = previous
		a.mu.Unlock()
		fail(w, 429, "Too many active sessions.")
		return
	}
	a.sessions[sessionKey(token)] = session{Expires: now.Add(sessionLifetime)}
	if err := a.saveSessions(); err != nil {
		a.sessions = previous
		a.mu.Unlock()
		log.Printf("persist login: %v", err)
		fail(w, 500, "Could not save login. Please try again.")
		return
	}
	a.mu.Unlock()
	a.cookie(w, token, int(sessionLifetime/time.Second))
	writeJSON(w, 200, map[string]bool{"authenticated": true})
}
func (a *app) wake(w http.ResponseWriter, r *http.Request) {
	if !a.authenticated(r) {
		fail(w, 401, "Log in to wake a machine.")
		return
	}
	id := r.PathValue("id")
	if r.URL.Path == "/wake" {
		id = a.hosts[0].ID
	}
	for _, h := range a.hosts {
		if h.ID != id {
			continue
		}
		if h.MAC == "" {
			fail(w, 409, "Wake on LAN is not configured for this machine.")
			return
		}
		a.mu.Lock()
		if time.Since(a.lastWake[id]) < 10*time.Second {
			a.mu.Unlock()
			fail(w, 429, "Please wait a few seconds before sending another packet.")
			return
		}
		a.lastWake[id] = time.Now()
		a.mu.Unlock()
		if err := a.send(h); err != nil {
			log.Printf("wake %s: %v", id, err)
			fail(w, 502, "Could not send wake packet.")
			return
		}
		log.Printf("wake packet sent: %s", id)
		writeJSON(w, 200, map[string]string{"message": "Wake packet sent. Awaiting telemetry to confirm the machine is reporting."})
		return
	}
	fail(w, 404, "Machine not found.")
}
func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.serverStatus()) })
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"authenticated": a.authenticated(r), "login_available": a.password != ""})
	})
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("homelab_session"); err == nil {
			a.mu.Lock()
			previous := cloneSessions(a.sessions)
			delete(a.sessions, sessionKey(c.Value))
			if err := a.saveSessions(); err != nil {
				a.sessions = previous
				a.mu.Unlock()
				log.Printf("persist logout: %v", err)
				fail(w, 500, "Could not save logout. Please try again.")
				return
			}
			a.mu.Unlock()
		}
		a.cookie(w, "", -1)
		writeJSON(w, 200, map[string]bool{"authenticated": false})
	})
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		// Keep hardware addresses and network configuration off the public API.
		type publicHost struct {
			ID          string    `json:"id"`
			Name        string    `json:"name"`
			Description string    `json:"description"`
			CanWake     bool      `json:"can_wake"`
			Telemetry   telemetry `json:"telemetry"`
			Stale       bool      `json:"stale"`
		}
		result := make([]publicHost, 0, len(a.hosts))
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, h := range a.hosts {
			reading := a.readings[h.ID]
			stale := reading.UpdatedAt == nil || time.Since(*reading.UpdatedAt) > telemetryStaleAfter
			name := strings.ToLower(h.Name)
			if reading.Hostname != "" {
				name = reading.Hostname
			}
			result = append(result, publicHost{ID: h.ID, Name: name, Description: h.Description, CanWake: h.MAC != "", Telemetry: reading, Stale: stale})
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("POST /api/hosts/{id}/wake", a.wake)
	mux.HandleFunc("GET /api/hosts/{id}/history", a.hostHistory)
	mux.HandleFunc("POST /wake", a.wake)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Endpoint not found.") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// A custom header forces cross-origin clients through a CORS preflight,
			// which this server does not permit. Also verify Origin when present.
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
					fail(w, 403, "Cross-origin request rejected.")
					return
				}
			}
			if r.Header.Get("X-Homelab-Request") != "1" {
				fail(w, 403, "Missing request header.")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func main() {
	setPassword := flag.Bool("set-password", false, "Read a dashboard password from stdin and save it privately")
	healthCheck := flag.Bool("health-check", false, "Wait for the locally running dashboard to serve this binary's build")
	mode := flag.String("mode", "server", "server, collector, or sample")
	once := flag.Bool("once", false, "Send one collector sample and exit")
	installCollector := flag.Bool("install-collector", false, "Enable collector autostart and automatic restart for this user")
	historyPath := flag.String("history-db", "", "SQLite telemetry history path; defaults to history.sqlite in the dashboard state directory")
	configureTelemetry := flag.Bool("configure-telemetry", false, "Configure telemetry transport while preserving host tokens")
	register := flag.Bool("register-host", false, "Add a telemetry-only host to the persistent host list")
	collectorInterface := flag.String("collector-interface", "", "Interface to monitor on the collector, or auto to follow the route at startup")
	linkKind := flag.String("link-type", "", "ethernet, wifi, tailscale, or other")
	initTelemetry := flag.Bool("init-telemetry", false, "Create private telemetry receiver and collector configuration files")
	listen := flag.String("telemetry-listen", "", "Interface address:port for telemetry setup")
	target := flag.String("collector-target", "", "Pi HTTP URL as reached from the collector, including any local interface scope")
	hostID := flag.String("host", "petra", "Configured host ID for initial telemetry setup")
	telemetryPath := flag.String("telemetry-config", "", "Receiver configuration path; defaults to ~/.config/homelab/telemetry.json")
	collectorPath := flag.String("collector-config", "", "Collector configuration path; defaults to ~/.config/homelab/collector.json")
	flag.Parse()
	if *mode == "server" && !*installCollector && (runtime.GOOS != "linux" || runtime.GOARCH != "arm64") {
		log.Fatal("The dashboard server requires Linux ARM64. Use collector or sample mode on this machine.")
	}
	hostsPath := os.Getenv("HOMELAB_HOSTS_FILE")
	if hostsPath == "" {
		hostsPath = configPath("hosts.json")
		if _, err := os.Stat(hostsPath); errors.Is(err, os.ErrNotExist) && !*register {
			hostsPath = ""
		}
	}
	if *healthCheck {
		if err := checkHealth(); err != nil {
			log.Fatal(err)
		}
		log.Print("New dashboard build is responding.")
		return
	}
	if *telemetryPath == "" {
		*telemetryPath = configPath("telemetry.json")
	}
	if *collectorPath == "" {
		*collectorPath = configPath("collector.json")
	}
	if *installCollector {
		if err := installCollectorAutostart(*collectorPath); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *initTelemetry || *configureTelemetry {
		if *register {
			if err := registerTelemetryHost(hostsPath, *hostID); err != nil {
				log.Fatal(err)
			}
		}
		if err := configureTelemetryConfig(*telemetryPath, *collectorPath, *listen, *target, *hostID, *collectorInterface, *linkKind, *configureTelemetry); err != nil {
			log.Fatal(err)
		}
		log.Print("Telemetry setup saved. Copy the collector configuration privately to its machine; restart the Pi dashboard.")
		return
	}
	if *mode == "sample" {
		writeSample()
		return
	}
	if *mode == "collector" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runCollector(ctx, *collectorPath, *once); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *mode != "server" {
		log.Fatal("mode must be server, collector, or sample")
	}
	passwordPath, statePath, err := authPaths()
	if err != nil {
		log.Fatal(err)
	}
	if *setPassword {
		if err := setPasswordInteractive(os.Stdin, passwordPath); err != nil {
			log.Fatal(err)
		}
		log.Print("Password saved. Restart the dashboard to enable it. Existing sessions will be invalidated.")
		return
	}
	hosts, err := loadHosts(hostsPath)
	if err != nil {
		log.Fatal(err)
	}
	password := os.Getenv("HOMELAB_PASSWORD")
	if password == "" {
		data, err := os.ReadFile(passwordPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Fatal(err)
		}
		password = strings.TrimRight(string(data), "\r\n")
	}
	if password != "" && len(password) < 16 {
		log.Fatal("HOMELAB_PASSWORD must have at least 16 characters")
	}
	a := &app{hosts: hosts, password: password, secure: os.Getenv("HOMELAB_INSECURE_COOKIES") != "1", sessions: map[string]session{}, lastWake: map[string]time.Time{}, send: sendWake}
	a.buildID, err = executableDigest()
	if err != nil {
		log.Fatal(err)
	}
	a.hostname, err = os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	a.hostname = strings.ToLower(a.hostname)
	a.startedAt = time.Now().UTC()
	a.release = "local"
	if data, err := os.ReadFile("release.txt"); err == nil {
		a.release = strings.TrimSpace(string(data))
	}
	a.sessionFile = statePath
	if *historyPath == "" {
		*historyPath = filepath.Join(filepath.Dir(statePath), "history.sqlite")
	}
	a.history, err = openHistory(*historyPath)
	if err != nil {
		log.Printf("Telemetry history unavailable: %v", err)
	} else {
		defer a.history.db.Close()
	}
	receiver, err := loadReceiverConfig(*telemetryPath, hosts)
	if err != nil {
		log.Fatal(err)
	}
	a.telemetryTokens = receiver.Tokens
	a.readings = map[string]telemetry{}
	if err := a.loadSessions(); err != nil {
		log.Fatalf("load sessions: %v", err)
	}
	addr := os.Getenv("HOMELAB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: addr, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	localSampler := newSampler("tailscale0")
	go a.collectLocalTelemetry(ctx, localSampler.sample, 5*time.Second)
	if receiver.Listen != "" {
		if err := startTelemetryServer(ctx, receiver.Listen, a.telemetryHandler()); err != nil {
			log.Fatal(err)
		}
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if password == "" {
		log.Print("No dashboard password configured: public view only. Run with -set-password to enable login.")
	}
	log.Printf("Homelab dashboard listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
