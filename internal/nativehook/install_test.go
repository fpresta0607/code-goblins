package nativehook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPreservesOtherHooksAndIsIdempotent(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			filename := "hooks.json"
			if name == "claude" {
				filename = "settings.json"
			}
			path := filepath.Join(dir, filename)
			original := []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo unrelated"}]}]}}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			cfg := InstallConfig{Harness: name, ConfigDir: dir, Executable: filepath.Join(dir, "cfo.exe"), Home: dir, State: filepath.Join(dir, "state")}
			if _, err := Install(cfg); err != nil {
				t.Fatal(err)
			}
			first, _ := os.ReadFile(path)
			if _, err := Install(cfg); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(path)
			if string(first) != string(second) {
				t.Fatal("second install changed settings")
			}
			var doc struct {
				Theme string `json:"theme"`
				Hooks map[string][]struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal(second, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Theme != "dark" || doc.Hooks["Stop"][0].Hooks[0].Command != "echo unrelated" || len(doc.Hooks["Stop"]) != 2 || len(doc.Hooks["SessionEnd"]) != 1 || len(doc.Hooks["SubagentStart"]) != 1 {
				t.Fatalf("settings lost meaning: %s", second)
			}
			if (name == "codex") != (len(doc.Hooks["Interrupt"]) == 1) {
				t.Fatal("Interrupt must be installed only for the verified Codex contract")
			}
			backup, _ := os.ReadFile(path + ".cfo-native.backup")
			if string(backup) != string(original) {
				t.Fatal("original backup not preserved")
			}
		})
	}
}

func TestUnsupportedCapabilitiesAndBrokenSettingsAreRefused(t *testing.T) {
	for _, name := range []string{"codex", "claude", "pi", "unknown"} {
		if err := CheckCapability(name, "0.1.0", ""); err == nil {
			t.Fatalf("accepted unsupported %s", name)
		}
	}
	if err := CheckCapability("codex", "codex-cli 0.154.0", "--dangerously-bypass-hook-trust"); err != nil {
		t.Fatal(err)
	}
	if err := CheckCapability("pi", "0.85.1", ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Install(InstallConfig{Harness: "codex", ConfigDir: dir, Executable: filepath.Join(dir, "cfo.exe"), Home: dir, State: filepath.Join(dir, "state")})
	if err == nil {
		t.Fatal("accepted malformed hooks")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{"hooks":42}` {
		t.Fatal("overwrote broken settings")
	}
}
