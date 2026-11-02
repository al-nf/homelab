package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

func registerTelemetryHost(path, id string) error {
	if id == "" || !validHostname(id) || strings.HasPrefix(id, "__") {
		return errors.New("host ID must be a nonempty hostname and cannot start with __")
	}
	existing := path
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		existing = ""
	} else if err != nil {
		return err
	}
	hosts, err := loadHosts(existing)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		if h.ID == id {
			return nil
		}
	}
	hosts = append(hosts, host{ID: id, Name: strings.ToLower(id)})
	data, err := json.MarshalIndent(hosts, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}
