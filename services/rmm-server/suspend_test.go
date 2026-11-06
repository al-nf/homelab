package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSuspendExistingLocker(t *testing.T) {
	conflict := "Failed to start transient service unit: Unit petra-suspend.service was already loaded or has a fragment file."
	for _, tc := range []struct {
		name, output, locked                      string
		nuFails, lockFails, sleepFails, wantError bool
		wantCalls                                 int
	}{
		{name: "normal invocation", wantCalls: 1},
		{name: "existing locked session", output: conflict, locked: "true\n", nuFails: true, wantCalls: 3},
		{name: "existing unlocked session", output: conflict, locked: "false\n", nuFails: true, wantError: true, wantCalls: 2},
		{name: "unavailable lock state", output: conflict, nuFails: true, lockFails: true, wantError: true, wantCalls: 2},
		{name: "invalid lock state", output: conflict, locked: "unknown request", nuFails: true, wantError: true, wantCalls: 2},
		{name: "sleep rejected", output: conflict, locked: "true", nuFails: true, sleepFails: true, wantError: true, wantCalls: 3},
		{name: "unrelated script failure", output: "Wayland display missing", nuFails: true, wantError: true, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			err := executeSuspend("/test/nu", "/test/config.nu", func(name string, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{name}, args...))
				var output string
				var failed bool
				switch len(calls) {
				case 1:
					output, failed = tc.output, tc.nuFails
				case 2:
					output, failed = tc.locked, tc.lockFails
				case 3:
					output, failed = "Permission denied", tc.sleepFails
				default:
					t.Fatal("unexpected extra command")
				}
				if failed {
					return []byte(output), errors.New("exit status 1")
				}
				return []byte(output), nil
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			want := [][]string{
				{"/test/nu", "--config", "/test/config.nu", "--commands", "suspend"},
				{"/run/current-system/sw/bin/hyprctl", "locked"},
				{"/run/current-system/sw/bin/systemctl", "--no-block", "suspend"},
			}
			if !reflect.DeepEqual(calls, want[:tc.wantCalls]) {
				t.Fatalf("commands = %#v", calls)
			}
		})
	}
}

func TestDisconnectOnlyOwnIncomingSSH(t *testing.T) {
	var ended []string
	properties := map[string]string{
		"1": "User=1000\nRemote=no\nService=login\n",
		"2": "User=1000\nRemote=yes\nService=sshd\n",
		"3": "User=1000\nRemote=yes\nService=sshd\n",
		"4": "User=1000\nRemote=yes\nService=remote-desktop\n",
		"5": "User=2000\nRemote=yes\nService=sshd\n", // Recheck ownership after listing.
	}
	count, err := disconnectSSH("1000", func(name string, args ...string) ([]byte, error) {
		if name != "/run/current-system/sw/bin/loginctl" {
			t.Fatalf("unexpected executable: %s", name)
		}
		switch args[0] {
		case "list-sessions":
			return []byte("1 1000 afung seat0\n2 1000 afung\n3 1000 afung\n4 1000 afung\n5 1000 afung\n6 2000 other\n"), nil
		case "show-session":
			info, ok := properties[args[1]]
			if !ok {
				t.Fatalf("inspected unrelated session %s", args[1])
			}
			return []byte(info), nil
		case "--no-ask-password":
			if args[1] != "terminate-session" {
				t.Fatal(args)
			}
			ended = append(ended, args[2])
			return nil, nil
		default:
			t.Fatalf("unexpected command: %v", args)
		}
		return nil, nil
	})
	if err != nil || count != 2 || !reflect.DeepEqual(ended, []string{"2", "3"}) {
		t.Fatalf("count=%d sessions=%v error=%v", count, ended, err)
	}
}

func TestDisconnectSSHFailures(t *testing.T) {
	for _, step := range []string{"list-sessions", "show-session", "--no-ask-password"} {
		t.Run(step, func(t *testing.T) {
			_, err := disconnectSSH("1000", func(_ string, args ...string) ([]byte, error) {
				if args[0] == step {
					return []byte("permission denied"), errors.New("failed")
				}
				if args[0] == "list-sessions" {
					return []byte("2 1000 afung\n"), nil
				}
				if args[0] == "show-session" {
					return []byte("User=1000\nRemote=yes\nService=sshd\n"), nil
				}
				t.Fatal("unexpected command after failure")
				return nil, nil
			})
			if err == nil {
				t.Fatal("failure was ignored")
			}
		})
	}
	_, err := disconnectSSH("1000", func(_ string, args ...string) ([]byte, error) {
		if args[0] != "list-sessions" {
			t.Fatal("used invalid session ID")
		}
		return []byte("--all 1000 afung\n"), nil
	})
	if err == nil {
		t.Fatal("accepted invalid session ID")
	}
	count, err := disconnectSSH("1000", func(_ string, args ...string) ([]byte, error) {
		if args[0] != "list-sessions" {
			t.Fatal("unexpected command with no sessions")
		}
		return nil, nil
	})
	if err != nil || count != 0 {
		t.Fatalf("empty sessions: %d %v", count, err)
	}
}

func TestSuspendAuthorizationAndDelivery(t *testing.T) {
	a := receiverApp(t)
	path := "/api/hosts/petra/suspend"
	cookie := loginCookie(t, a)
	if w := request(a, "POST", path, "", nil, true); w.Code != 401 {
		t.Fatalf("anonymous suspend: %d", w.Code)
	}
	if w := request(a, "POST", path, "", cookie, true); w.Code != 409 {
		t.Fatalf("unavailable suspend: %d", w.Code)
	}
	if w := ingest(a, "petra", a.telemetryTokens["petra"], `{"cpu_percent":1,"can_suspend":true}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := request(a, "POST", path, "", cookie, false); w.Code != 403 {
		t.Fatalf("missing CSRF header: %d", w.Code)
	}
	if w := request(a, "POST", "/api/hosts/unknown/suspend", "", cookie, true); w.Code != 404 {
		t.Fatalf("unknown host: %d", w.Code)
	}
	if w := request(a, "POST", path, "", cookie, true); w.Code != 202 {
		t.Fatalf("queue suspend: %d %s", w.Code, w.Body)
	}
	if w := request(a, "POST", path, "", cookie, true); w.Code != 429 {
		t.Fatalf("duplicate suspend: %d", w.Code)
	}
	if w := ingest(a, "petra", "wrong", `{"cpu_percent":1,"can_suspend":true}`); w.Code != 401 {
		t.Fatal("invalid credentials accepted")
	}
	for i := 0; i < 2; i++ {
		w := ingest(a, "petra", a.telemetryTokens["petra"], `{"cpu_percent":1,"can_suspend":true}`)
		var reply collectorReply
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		if (reply.Command == "suspend") != (i == 0) {
			t.Fatalf("delivery %d: %+v", i, reply)
		}
	}
	old := time.Now().Add(-time.Minute)
	reading := a.readings["petra"]
	reading.UpdatedAt = &old
	a.readings["petra"] = reading
	if w := request(a, "POST", path, "", cookie, true); w.Code != 409 {
		t.Fatalf("stale collector: %d", w.Code)
	}
}

func TestSuspendExpiryAndIsolation(t *testing.T) {
	a := receiverApp(t)
	now := time.Now()
	a.pendingSuspend = map[string]time.Time{"petra": now.Add(time.Second)}
	if a.takeSuspendLocked("babylon", telemetry{CanSuspend: true}, now) != "" {
		t.Fatal("command crossed hosts")
	}
	if a.takeSuspendLocked("petra", telemetry{CanSuspend: true}, now.Add(suspendLifetime)) != "" {
		t.Fatal("expired command delivered")
	}
	a.pendingSuspend["petra"] = now.Add(time.Second)
	if a.takeSuspendLocked("petra", telemetry{}, now) != "" {
		t.Fatal("command delivered to disabled collector")
	}
}

func TestCollectorRejectsDisabledAndArbitraryActions(t *testing.T) {
	for _, command := range []string{"suspend", "arbitrary shell command"} {
		client := &http.Client{Transport: localTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, collectorReply{Accepted: true, Command: command})
		})}}
		cpu := 1.0
		c := collectorConfig{Target: "http://100.64.0.1:8081", HostID: "petra", Token: strings.Repeat("a", 64)}
		if err := postSample(context.Background(), client, c, telemetry{CPUPercent: &cpu}); err == nil {
			t.Fatal("disabled action accepted")
		}
		c.AllowSuspend = true
		if command != "suspend" {
			if err := postSample(context.Background(), client, c, telemetry{CPUPercent: &cpu, CanSuspend: true}); err == nil {
				t.Fatal("arbitrary command accepted")
			}
		}
	}
}

func TestSuspendRunsConfiguredNushellExpression(t *testing.T) {
	// Use an isolated, harmless expression; never load the user's real suspend function.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.Mkdir(filepath.Join(dir, "nushell"), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "nushell/config.nu")
	if err := os.WriteFile(config, []byte(`def suspend [] { "called" | save --force $env.RMM_SUSPEND_TEST_FILE }`), 0600); err != nil {
		t.Fatal(err)
	}
	if !suspendAvailable() {
		t.Skip("Linux with Nushell required")
	}
	marker := filepath.Join(dir, "called")
	t.Setenv("RMM_SUSPEND_TEST_FILE", marker)
	if err := runNushellSuspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "called" {
		t.Fatalf("expression did not run: %q %v", data, err)
	}
}

func TestSuspendFailureIsReportedOnNextSample(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.Mkdir(filepath.Join(dir, "nushell"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nushell/config.nu"), []byte(`def suspend [] { error make {msg: "expected test failure"} }`), 0600); err != nil {
		t.Fatal(err)
	}
	if !suspendAvailable() {
		t.Skip("Linux with Nushell required")
	}
	a := receiverApp(t)
	a.pendingSuspend = map[string]time.Time{"petra": time.Now().Add(suspendLifetime)}
	client := &http.Client{Transport: localTransport{a.telemetryHandler()}}
	c := collectorConfig{Target: "http://100.64.0.1:8081", HostID: "petra", Token: a.telemetryTokens["petra"], AllowSuspend: true}
	cpu := 1.0
	reading := telemetry{CPUPercent: &cpu, CanSuspend: true}
	var reported string
	if err := deliverSampleWithAction(context.Background(), client, c, reading, &reported, runNushellSuspend); err == nil || reported == "" {
		t.Fatalf("expected reported failure: %q %v", reported, err)
	}
	reading.SuspendError = reported
	if err := deliverSample(context.Background(), client, c, reading, &reported); err != nil {
		t.Fatal(err)
	}
	if a.readings["petra"].SuspendError == "" {
		t.Fatal("failure was not delivered to dashboard")
	}
}

func TestSuspendCapabilityPublic(t *testing.T) {
	a := receiverApp(t)
	ingest(a, "petra", a.telemetryTokens["petra"], `{"cpu_percent":1,"can_suspend":true}`)
	w := request(a, "GET", "/api/hosts", "", nil, false)
	if !strings.Contains(w.Body.String(), `"can_suspend":true`) {
		t.Fatal(w.Body)
	}
	// Endpoint stays POST-only.
	r := httptest.NewRequest("GET", "/api/hosts/petra/suspend", nil)
	result := httptest.NewRecorder()
	a.handler().ServeHTTP(result, r)
	if result.Code == 202 {
		t.Fatal("GET suspended host")
	}
}
