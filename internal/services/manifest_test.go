package services

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dataDir, project, body string) {
	t.Helper()
	path := ManifestPath(dataDir, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifestReadsTheProjectsDeclaration(t *testing.T) {
	// Arrange
	dataDir := t.TempDir()
	writeManifest(t, dataDir, "PrecisionDocs-AI", `{
  "project": "PrecisionDocs-AI",
  "compose": "docker-compose.dev.yml",
  "env_file": ".env.docker.local",
  "services": ["redis", "backend", "worker-light"],
  "check": ["powershell", "-NoProfile", "-File", "guard.ps1"],
  "memory_estimate_gb": 3.5
}`)

	// Act
	manifest, err := LoadManifest(dataDir, `C:\dev\PrecisionDocs-AI`)

	// Assert
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Compose != "docker-compose.dev.yml" || manifest.EnvFile != ".env.docker.local" {
		t.Errorf("compose = %q env = %q", manifest.Compose, manifest.EnvFile)
	}
	if strings.Join(manifest.Services, ",") != "redis,backend,worker-light" {
		t.Errorf("services = %v", manifest.Services)
	}
	if manifest.MemoryEstimateGB != 3.5 || len(manifest.Check) != 4 {
		t.Errorf("estimate = %v check = %v", manifest.MemoryEstimateGB, manifest.Check)
	}
	if manifest.Path != ManifestPath(dataDir, "PrecisionDocs-AI") {
		t.Errorf("path = %q", manifest.Path)
	}
}

func TestLoadManifestRefusesADeclarationItCannotStartFrom(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"no project", `{"compose":"c.yml","services":["a"],"memory_estimate_gb":1}`, "project is required"},
		{"no compose", `{"project":"p","services":["a"],"memory_estimate_gb":1}`, "compose"},
		{"compose outside the checkout", `{"project":"p","compose":"../c.yml","services":["a"],"memory_estimate_gb":1}`, "inside the checkout"},
		{"absolute compose", `{"project":"p","compose":"C:\\c.yml","services":["a"],"memory_estimate_gb":1}`, "inside the checkout"},
		{"env file outside the checkout", `{"project":"p","compose":"c.yml","env_file":"..\\.env","services":["a"],"memory_estimate_gb":1}`, "env_file"},
		{"no services", `{"project":"p","compose":"c.yml","services":[],"memory_estimate_gb":1}`, "names no compose service"},
		{"a flag as a service", `{"project":"p","compose":"c.yml","services":["--profile"],"memory_estimate_gb":1}`, "not a compose service name"},
		{"an empty check", `{"project":"p","compose":"c.yml","services":["a"],"check":[""],"memory_estimate_gb":1}`, "check names no program"},
		{"no estimate", `{"project":"p","compose":"c.yml","services":["a"]}`, "memory_estimate_gb"},
		{"an unknown field", `{"project":"p","compose":"c.yml","services":["a"],"memory_estimate_gb":1,"profile":"x"}`, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			dataDir := t.TempDir()
			writeManifest(t, dataDir, "p", tc.body)

			// Act
			_, err := LoadManifest(dataDir, "p")

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

func TestLoadManifestReportsAProjectThatDeclaresNone(t *testing.T) {
	_, err := LoadManifest(t.TempDir(), "nothing-declared")

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want a missing file", err)
	}
}
