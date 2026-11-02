package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func installCollectorAutostart(config string) error {
	binary, config, err := collectorInstallPaths(config)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	job := domain + "/" + collectorAgent
	if err := exec.Command("/bin/launchctl", "print", domain).Run(); err != nil {
		return fmt.Errorf("log into babylon's macOS desktop before enabling its LaunchAgent: %w", err)
	}
	if err := exec.Command("/bin/launchctl", "print", job).Run(); err == nil {
		if err := serviceCommand("/bin/launchctl", "bootout", job); err != nil {
			return err
		}
	}
	plist := filepath.Join(home, "Library/LaunchAgents", collectorAgent+".plist")
	logPath := filepath.Join(filepath.Dir(binary), "collector.log")
	if err := writePrivateFile(plist, []byte(collectorPlist(binary, config, logPath))); err != nil {
		return err
	}
	if err := serviceCommand("/bin/launchctl", "enable", job); err != nil {
		return err
	}
	if err := serviceCommand("/bin/launchctl", "bootstrap", domain, plist); err != nil {
		return err
	}
	log.Printf("Collector enabled at macOS login with automatic restart. Logs: %s", logPath)
	return nil
}
