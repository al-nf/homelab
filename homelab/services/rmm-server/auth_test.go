package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistentLoginAndLogout(t *testing.T) {
	a := testApp(t)
	a.sessionFile = filepath.Join(t.TempDir(), "state", "sessions.json")
	cookie := loginCookie(t, a)
	if cookie.MaxAge != 90*24*60*60 || time.Until(cookie.Expires) < 89*24*time.Hour {
		t.Fatal("cookie should persist for 90 days")
	}
	data, err := os.ReadFile(a.sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), cookie.Value) || strings.Contains(string(data), a.password) {
		t.Fatal("session file contains raw credentials")
	}
	info, err := os.Stat(a.sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("session file must be private")
	}
	restarted := testApp(t)
	restarted.sessionFile = a.sessionFile
	if err := restarted.loadSessions(); err != nil {
		t.Fatal(err)
	}
	w := request(restarted, "GET", "/api/session", "", cookie, false)
	if !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatal("session lost after restart")
	}
	w = request(restarted, "POST", "/api/logout", "", cookie, true)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	if len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout must delete cookie")
	}
	again := testApp(t)
	again.sessionFile = a.sessionFile
	if err := again.loadSessions(); err != nil {
		t.Fatal(err)
	}
	w = request(again, "GET", "/api/session", "", cookie, false)
	if !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatal("logout did not survive restart")
	}
}
func TestPasswordChangeAndExpiredSavedSessions(t *testing.T) {
	a := testApp(t)
	a.sessionFile = filepath.Join(t.TempDir(), "sessions.json")
	cookie := loginCookie(t, a)
	for _, password := range []string{"different-password-value", ""} {
		restarted := testApp(t)
		restarted.sessionFile = a.sessionFile
		restarted.password = password
		if err := restarted.loadSessions(); err != nil {
			t.Fatal(err)
		}
		w := request(restarted, "POST", "/wake", "", cookie, true)
		if w.Code != 401 {
			t.Fatal("changed password did not revoke session")
		}
	}
	a.sessions[sessionKey(cookie.Value)] = session{Expires: time.Now().Add(-time.Hour)}
	if err := a.saveSessions(); err != nil {
		t.Fatal(err)
	}
	if err := a.loadSessions(); err != nil {
		t.Fatal(err)
	}
	if len(a.sessions) != 0 {
		t.Fatal("expired session restored")
	}
}
func TestSessionWriteFailure(t *testing.T) {
	a := testApp(t)
	// An existing directory cannot be replaced by a regular file.
	a.sessionFile = t.TempDir()
	w := request(a, "POST", "/api/login", `{"password":"a-long-test-password"}`, nil, true)
	if w.Code != 500 || len(a.sessions) != 0 || len(w.Result().Cookies()) != 0 {
		t.Fatal("failed login persistence granted a session")
	}
	a.sessionFile = ""
	cookie := loginCookie(t, a)
	a.sessionFile = t.TempDir()
	w = request(a, "POST", "/api/logout", "", cookie, true)
	if w.Code != 500 || len(a.sessions) != 1 {
		t.Fatal("failed logout persistence was reported as success")
	}
}
func TestPasswordSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "password")
	if err := setPasswordFrom(strings.NewReader("short"), path); err == nil {
		t.Fatal("short password accepted")
	}
	if err := setPasswordFrom(strings.NewReader("a-long-test-password\n"), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a-long-test-password" {
		t.Fatal("password changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("password file must be private")
	}
}
func TestCorruptSessionFile(t *testing.T) {
	a := testApp(t)
	a.sessionFile = filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(a.sessionFile, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.loadSessions(); err == nil {
		t.Fatal("corrupt store silently ignored")
	}
}
func TestPrivateSessionState(t *testing.T) {
	a := testApp(t)
	a.sessionFile = filepath.Join(t.TempDir(), "sessions.json")
	_ = loginCookie(t, a)
	data, err := os.ReadFile(a.sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	var store savedSessions
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	for key := range store.Sessions {
		if len(key) != 64 {
			t.Fatal("expected SHA256 token digest")
		}
	}
	w := request(a, "GET", "/api/session", "", &http.Cookie{Name: "homelab_session", Value: "forged"}, false)
	if !strings.Contains(w.Body.String(), `"authenticated":false`) {
		t.Fatal("forged cookie accepted")
	}
}

// Stdin may be a terminal or a long-lived pipe: a newline must finish setup
// without waiting for the reader to close.
func TestPasswordSetupFinishesAtNewline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	path := filepath.Join(t.TempDir(), "password")
	finished := make(chan error, 1)
	go func() { finished <- setPasswordFrom(reader, path) }()
	if _, err := writer.Write([]byte("a-long-test-password\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("password setup waited for EOF after receiving a newline")
	}
}
