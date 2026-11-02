package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const collectorService = "rmm-collector.service"
const collectorAgent = "local.homelab.collector"

func collectorInstallPaths(config string) (binary, configPath string, err error) {
	if _, err = loadCollectorConfig(config); err != nil {
		return "", "", fmt.Errorf("collector configuration: %w", err)
	}
	configPath, err = filepath.Abs(config)
	if err != nil {
		return "", "", err
	}
	binary, err = os.Executable()
	return binary, configPath, err
}

func serviceCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func systemdArgument(value string) string {
	// Unit specifiers and ExecStart environment expansion are separate from
	// ordinary quoting; escape both, then quote the complete argument.
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "$", "$$", "\n", "\\n", "\r", "\\r").Replace(value)
	return "\"" + value + "\""
}

func collectorUnit(binary, config string) string {
	return fmt.Sprintf(`[Unit]
Description=Homelab telemetry collector
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=%s -mode collector -collector-config %s
Restart=always
RestartSec=5
UMask=0077

[Install]
WantedBy=default.target
`, systemdArgument(binary), systemdArgument(config))
}

func xmlText(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func collectorPlist(binary, config, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>
<string>%s</string><string>-mode</string><string>collector</string>
<string>-collector-config</string><string>%s</string>
</array>
<key>WorkingDirectory</key><string>%s</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>10</integer>
<key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, collectorAgent, xmlText(binary), xmlText(config), xmlText(filepath.Dir(binary)), xmlText(logPath), xmlText(logPath))
}
