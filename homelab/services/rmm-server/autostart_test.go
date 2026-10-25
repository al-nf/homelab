package main

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestCollectorUnitUsesExplicitPathsAndRestart(t *testing.T) {
	unit := collectorUnit(`/home/a b/100%/$collector`, `/home/a b/config "private".json`)
	for _, fragment := range []string{
		`ExecStart="/home/a b/100%%/$$collector" -mode collector -collector-config "/home/a b/config \"private\".json"`,
		"Restart=always", "RestartSec=5", "StartLimitIntervalSec=0", "WantedBy=default.target",
	} {
		if !strings.Contains(unit, fragment) {
			t.Fatalf("unit is missing %q:\n%s", fragment, unit)
		}
	}
	if strings.Contains(unit, "tmux") || strings.Contains(unit, "nix develop") {
		t.Fatal("boot startup depends on an interactive environment")
	}
}

func TestCollectorLaunchAgentXMLAndArguments(t *testing.T) {
	binary := `/Users/a & b/rmm-collector/rmm-collector`
	config := `/Users/a & b/rmm-collector/config "private".json`
	plist := collectorPlist(binary, config, "/Users/a & b/rmm-collector/collector.log")
	decoder := xml.NewDecoder(strings.NewReader(plist))
	var stringsFound []string
	var keys []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid launchd plist XML: %v", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "string" && start.Name.Local != "key" {
			continue
		}
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			t.Fatal(err)
		}
		if start.Name.Local == "key" {
			keys = append(keys, value)
		} else {
			stringsFound = append(stringsFound, value)
		}
	}
	for _, expected := range []string{binary, config, "-mode", "collector", "-collector-config", collectorAgent} {
		found := false
		for _, actual := range stringsFound {
			found = found || actual == expected
		}
		if !found {
			t.Fatalf("missing or incorrectly escaped launchd argument %q", expected)
		}
	}
	for _, expected := range []string{"RunAtLoad", "KeepAlive", "ThrottleInterval", "StandardOutPath", "StandardErrorPath"} {
		if !strings.Contains(strings.Join(keys, ","), expected) {
			t.Fatalf("missing launchd setting %s", expected)
		}
	}
}
