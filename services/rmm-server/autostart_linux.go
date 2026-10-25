package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func installCollectorAutostart(config string) error {
	source, config, err := collectorInstallPaths(config)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local/share")
	}
	binary := filepath.Join(dataDir, "homelab/collector/rmm-collector")
	if source != binary {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		// Publish a complete executable atomically so upgrades can replace the
		// binary while the previous collector is still running.
		if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
			return err
		}
		file, err := os.CreateTemp(filepath.Dir(binary), ".collector-*")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		if _, err = file.Write(data); err == nil {
			err = file.Chmod(0700)
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if err := os.Rename(file.Name(), binary); err != nil {
			return err
		}
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	unit := filepath.Join(configDir, "systemd/user", collectorService)
	if err := writePrivateFile(unit, []byte(collectorUnit(binary, config))); err != nil {
		return err
	}
	if err := serviceCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := serviceCommand("systemctl", "--user", "enable", collectorService); err != nil {
		return err
	}
	if err := serviceCommand("systemctl", "--user", "restart", collectorService); err != nil {
		return err
	}
	if err := serviceCommand("systemctl", "--user", "is-active", "--quiet", collectorService); err != nil {
		return err
	}
	linger, err := exec.Command("loginctl", "show-user", fmt.Sprint(os.Getuid()), "-p", "Linger", "--value").Output()
	if err != nil || strings.TrimSpace(string(linger)) != "yes" {
		if err := serviceCommand("loginctl", "enable-linger"); err != nil {
			return fmt.Errorf("collector is running and enabled at login; boot autostart needs lingering. Run `sudo loginctl enable-linger %s`, then retry: %w", os.Getenv("USER"), err)
		}
	}
	log.Print("Collector enabled at boot with automatic restart. Logs: journalctl --user -u rmm-collector.service -f")
	return nil
}
