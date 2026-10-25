package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelemetryOnlyHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := os.WriteFile(path, []byte(`[{"id":"laptop","name":"laptop"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	hosts, err := loadHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	a.hosts = hosts
	if w := request(a, "GET", "/api/hosts", "", nil, false); w.Code != 200 || !strings.Contains(w.Body.String(), `"can_wake":false`) {
		t.Fatalf("telemetry-only host missing: %s", w.Body)
	}
	sent := false
	a.send = func(host) error { sent = true; return nil }
	cookie := loginCookie(t, a)
	if w := request(a, "POST", "/api/hosts/laptop/wake", "", cookie, true); w.Code != 409 || sent {
		t.Fatal("telemetry-only host accepted a wake action")
	}
}

func testApp(t *testing.T) *app {
	t.Helper()
	hosts, err := loadHosts("")
	if err != nil {
		t.Fatal(err)
	}
	return &app{hosts: hosts, password: "a-long-test-password", secure: true, sessions: map[string]session{}, lastWake: map[string]time.Time{}, send: func(host) error { return nil }}
}
func request(a *app, method, path, body string, cookie *http.Cookie, header bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if header {
		r.Header.Set("X-Homelab-Request", "1")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}
func loginCookie(t *testing.T, a *app) *http.Cookie {
	t.Helper()
	w := request(a, "POST", "/api/login", `{"password":"a-long-test-password"}`, nil, true)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	return cookies[0]
}
func TestPublicDashboard(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/", "/style.css", "/app.js", "/api/hosts", "/api/session"} {
		w := request(a, "GET", path, "", nil, false)
		if w.Code != 200 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	w := request(a, "GET", "/api/hosts", "", nil, false)
	if strings.Contains(w.Body.String(), a.hosts[0].MAC) || strings.Contains(w.Body.String(), "broadcast") {
		t.Fatal("public API leaks network config")
	}
	if !strings.Contains(w.Body.String(), `"cpu_percent":null`) {
		t.Fatal("expected unknown telemetry")
	}
}
func TestProtectedWakeAndSession(t *testing.T) {
	a := testApp(t)
	sent := 0
	a.send = func(h host) error {
		sent++
		if h.MAC != "F4:93:9F:F5:7B:CC" {
			t.Fatal("wrong target")
		}
		return nil
	}
	for _, path := range []string{"/wake", "/api/hosts/petra/wake"} {
		if w := request(a, "POST", path, "", nil, true); w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
	}
	if w := request(a, "POST", "/api/login", `{"password":"wrong"}`, nil, true); w.Code != 401 {
		t.Fatal("wrong password accepted")
	}
	cookie := loginCookie(t, a)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	if w := request(a, "POST", "/api/hosts/petra/wake", "", cookie, true); w.Code != 200 {
		t.Fatalf("wake: %d %s", w.Code, w.Body)
	}
	if w := request(a, "POST", "/wake", "", cookie, true); w.Code != 429 {
		t.Fatal("wake throttle missing")
	}
	if sent != 1 {
		t.Fatalf("sent %d packets", sent)
	}
	if w := request(a, "POST", "/api/hosts/missing/wake", "", cookie, true); w.Code != 404 {
		t.Fatal("unknown target accepted")
	}
	request(a, "POST", "/api/logout", "", cookie, true)
	if w := request(a, "POST", "/wake", "", cookie, true); w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
}
func TestExpiredSessionAndDisabledLogin(t *testing.T) {
	a := testApp(t)
	a.sessions[sessionKey("expired")] = session{Expires: time.Now().Add(-time.Second)}
	if w := request(a, "POST", "/wake", "", &http.Cookie{Name: "homelab_session", Value: "expired"}, true); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	a.password = ""
	if w := request(a, "POST", "/api/login", `{"password":""}`, nil, true); w.Code != 503 {
		t.Fatal("unconfigured login enabled")
	}
}
func TestCSRFAndMethods(t *testing.T) {
	a := testApp(t)
	cookie := loginCookie(t, a)
	for _, path := range []string{"/api/login", "/api/logout", "/api/hosts/petra/wake", "/wake"} {
		if w := request(a, "POST", path, "{}", cookie, false); w.Code != 403 {
			t.Errorf("missing header accepted for %s", path)
		}
		r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("X-Homelab-Request", "1")
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("foreign origin accepted for %s", path)
		}
	}
	if w := request(a, "GET", "/wake", "", cookie, false); w.Code == 200 {
		t.Fatal("GET wake accepted")
	}
}
func TestLoginRateLimit(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 10; i++ {
		request(a, "POST", "/api/login", `{"password":"wrong"}`, nil, true)
	}
	w := request(a, "POST", "/api/login", `{"password":"wrong"}`, nil, true)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("missing login throttle")
	}
}
func TestMagicPacket(t *testing.T) {
	packet, err := magicPacket("F4:93:9F:F5:7B:CC")
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 102 || !bytes.Equal(packet[:6], bytes.Repeat([]byte{255}, 6)) {
		t.Fatal("invalid magic packet prefix")
	}
	for i := 6; i < 102; i += 6 {
		if !bytes.Equal(packet[i:i+6], []byte{0xf4, 0x93, 0x9f, 0xf5, 0x7b, 0xcc}) {
			t.Fatal("invalid target repetition")
		}
	}
	if _, err := magicPacket("bad"); err == nil {
		t.Fatal("invalid MAC accepted")
	}
}
