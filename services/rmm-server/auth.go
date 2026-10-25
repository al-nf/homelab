package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sessionLifetime = 90 * 24 * time.Hour

type savedSessions struct {
	CredentialTag string             `json:"credential_tag"`
	Sessions      map[string]session `json:"sessions"`
}

func sessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func credentialTag(password string) string {
	mac := hmac.New(sha256.New, []byte(password))
	_, _ = mac.Write([]byte("homelab persistent sessions v1"))
	return hex.EncodeToString(mac.Sum(nil))
}
func cloneSessions(sessions map[string]session) map[string]session {
	copy := make(map[string]session, len(sessions))
	for key, value := range sessions {
		copy[key] = value
	}
	return copy
}
func authPaths() (string, string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	password := os.Getenv("HOMELAB_PASSWORD_FILE")
	if password == "" {
		password = filepath.Join(config, "homelab", "password")
	}
	state := os.Getenv("HOMELAB_STATE_DIR")
	if state == "" {
		state = os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", "", err
			}
			state = filepath.Join(home, ".local", "state")
		}
		state = filepath.Join(state, "homelab")
	}
	return password, filepath.Join(state, "sessions.json"), nil
}

// Replace rather than truncate so a crash cannot leave a partially written store.
func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".homelab-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func setPasswordFrom(reader io.Reader, path string) error {
	data, err := bufio.NewReader(io.LimitReader(reader, 4097)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	password := strings.TrimRight(data, "\r\n")
	if len(password) < 16 || len(password) > 1024 {
		return errors.New("password must have between 16 and 1024 bytes")
	}
	return writePrivateFile(path, []byte(password))
}
func (a *app) loadSessions() error {
	a.sessions = map[string]session{}
	if a.sessionFile == "" {
		return nil
	}
	data, err := os.ReadFile(a.sessionFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved savedSessions
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("invalid session store: %w", err)
	}
	// Changing or removing the configured password invalidates all saved sessions.
	if a.password == "" || !hmac.Equal([]byte(saved.CredentialTag), []byte(credentialTag(a.password))) {
		return nil
	}
	now := time.Now()
	for key, s := range saved.Sessions {
		if now.Before(s.Expires) {
			a.sessions[key] = s
		}
	}
	return nil
}

// Call with a.mu held. Only token hashes are written to disk.
func (a *app) saveSessions() error {
	if a.sessionFile == "" {
		return nil
	}
	data, err := json.Marshal(savedSessions{CredentialTag: credentialTag(a.password), Sessions: a.sessions})
	if err != nil {
		return err
	}
	return writePrivateFile(a.sessionFile, data)
}
