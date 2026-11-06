package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type collectorReply struct {
	Accepted bool   `json:"accepted"`
	Command  string `json:"command,omitempty"`
}

const suspendLifetime = 15 * time.Second

func (a *app) suspend(w http.ResponseWriter, r *http.Request) {
	if !a.authenticated(r) {
		fail(w, 401, "Log in to suspend a machine.")
		return
	}
	id := r.PathValue("id")
	for _, h := range a.hosts {
		if h.ID != id {
			continue
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		reading := a.readings[id]
		if !reading.CanSuspend || reading.UpdatedAt == nil || time.Since(*reading.UpdatedAt) > telemetryStaleAfter {
			fail(w, 409, "Suspend needs a connected collector with suspend enabled.")
			return
		}
		if time.Since(a.lastSuspend[id]) < 30*time.Second {
			fail(w, 429, "Please wait before requesting suspend again.")
			return
		}
		if a.lastSuspend == nil {
			a.lastSuspend = map[string]time.Time{}
		}
		if a.pendingSuspend == nil {
			a.pendingSuspend = map[string]time.Time{}
		}
		now := time.Now()
		a.lastSuspend[id] = now
		a.pendingSuspend[id] = now.Add(suspendLifetime)
		log.Printf("suspend queued: %s", id)
		writeJSON(w, http.StatusAccepted, map[string]string{"message": "Suspend requested. The collector will run it on its next report."})
		return
	}
	fail(w, 404, "Machine not found.")
}

// Called only after authenticating and accepting this host's telemetry. Commands
// are delivered once and expire quickly so reconnecting cannot trigger old sleep requests.
func (a *app) takeSuspendLocked(id string, reading telemetry, now time.Time) string {
	expires, ok := a.pendingSuspend[id]
	delete(a.pendingSuspend, id)
	if ok && reading.CanSuspend && now.Before(expires) {
		return "suspend"
	}
	return ""
}

func suspendTools() (nu, config, searchPath string) {
	home, _ := os.UserHomeDir()
	dir, _ := os.UserConfigDir()
	config = filepath.Join(dir, "nushell", "config.nu")
	// User services on NixOS do not necessarily inherit the interactive PATH.
	paths := []string{filepath.Join(home, ".nix-profile/bin")}
	if user := os.Getenv("USER"); user != "" {
		paths = append(paths, filepath.Join("/etc/profiles/per-user", user, "bin"))
	}
	paths = append(paths, "/run/current-system/sw/bin", "/usr/local/bin", "/usr/bin", "/bin")
	searchPath = strings.Join(paths, string(os.PathListSeparator)) + string(os.PathListSeparator) + os.Getenv("PATH")
	for _, dir := range filepath.SplitList(searchPath) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, "nu")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			nu = candidate
			break
		}
	}
	return
}

func suspendAvailable() bool {
	nu, config, _ := suspendTools()
	_, err := os.Stat(config)
	return runtime.GOOS == "linux" && nu != "" && err == nil
}

func runSuspend(ctx context.Context) error {
	if !suspendAvailable() {
		return errors.New("Nushell suspend is unavailable")
	}
	cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
	count, err := disconnectSSH(strconv.Itoa(os.Getuid()), func(name string, args ...string) ([]byte, error) {
		return exec.CommandContext(cleanup, name, args...).CombinedOutput()
	})
	cancel()
	if err != nil {
		return fmt.Errorf("close SSH sessions: %w", err)
	}
	log.Printf("Closed %d incoming SSH session(s) before suspend", count)
	return runNushellSuspend(ctx)
}

func runNushellSuspend(ctx context.Context) error {
	nu, config, searchPath := suspendTools()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	env := append(os.Environ(), "PATH="+searchPath)
	// The lock-before-sleep script needs the active desktop's Wayland display,
	// which may have started after the boot-time collector. Read the user manager
	// environment afresh instead of hardcoding wayland-0 or restarting the collector.
	manager := exec.CommandContext(ctx, "/run/current-system/sw/bin/systemctl", "--user", "show-environment")
	if _, err := os.Stat(manager.Path); err != nil {
		manager = exec.CommandContext(ctx, "systemctl", "--user", "show-environment")
	}
	if data, err := manager.Output(); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			key, _, ok := strings.Cut(line, "=")
			if ok && (key == "WAYLAND_DISPLAY" || key == "DISPLAY" || key == "HYPRLAND_INSTANCE_SIGNATURE" || key == "XDG_CURRENT_DESKTOP") {
				env = append(env, line)
			}
		}
	}
	return executeSuspend(nu, config, func(name string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, name, args...)
		command.Env = env
		return command.CombinedOutput()
	})
}

// End only this user's incoming OpenSSH login sessions. The desktop, user
// services, and sshd listener belong to other scopes and must remain running.
func disconnectSSH(uid string, run func(string, ...string) ([]byte, error)) (int, error) {
	const loginctl = "/run/current-system/sw/bin/loginctl"
	data, err := run(loginctl, "list-sessions", "--no-legend", "--no-pager")
	if err != nil {
		return 0, fmt.Errorf("list sessions: %w: %s", err, strings.TrimSpace(string(data)))
	}
	var targets []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[0]
		if !validSessionID(id) {
			return 0, errors.New("invalid login session ID")
		}
		// The list contains UID as its second column. Avoid inspecting unrelated users.
		if len(fields) < 2 || fields[1] != uid {
			continue
		}
		info, err := run(loginctl, "show-session", id, "--property=User", "--property=Remote", "--property=Service", "--no-pager")
		if err != nil {
			return 0, fmt.Errorf("inspect session %s: %w", id, err)
		}
		properties := map[string]string{}
		for _, property := range strings.Split(string(info), "\n") {
			key, value, ok := strings.Cut(property, "=")
			if ok {
				properties[key] = value
			}
		}
		if properties["User"] == uid && properties["Remote"] == "yes" && properties["Service"] == "sshd" {
			targets = append(targets, id)
		}
	}
	for i, id := range targets {
		if data, err := run(loginctl, "--no-ask-password", "terminate-session", id); err != nil {
			return i, fmt.Errorf("terminate SSH session %s: %w: %s", id, err, strings.TrimSpace(string(data)))
		}
	}
	return len(targets), nil
}

func validSessionID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// The desktop script keeps petra-suspend.service alive while its screen locker
// runs. A second invocation cannot create that same transient unit. Leave the
// locker intact and ask systemd to sleep only if the compositor confirms that
// the session is already locked. Never infer a lock from the unit name alone.
func executeSuspend(nu, config string, run func(string, ...string) ([]byte, error)) error {
	output, err := run(nu, "--config", config, "--commands", "suspend")
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if strings.Contains(message, "Unit petra-suspend.service was already loaded") {
		locked, lockErr := run("/run/current-system/sw/bin/hyprctl", "locked")
		if lockErr != nil || strings.TrimSpace(string(locked)) != "true" {
			return fmt.Errorf("suspend service already exists, but the desktop lock could not be confirmed: %w", err)
		}
		output, err = run("/run/current-system/sw/bin/systemctl", "--no-block", "suspend")
		if err == nil {
			return nil
		}
		message = strings.TrimSpace(string(output))
	}
	return fmt.Errorf("%w: %s", err, message)
}
