package cleanup

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupScrubsMCPBearerAndEnvironmentBeforeArchival(t *testing.T) {
	f := newCleanupFixture(t)
	dir := filepath.Join(f.stateDir, "tasktmp", "g1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	const marker = "SYNTHETIC_CREDENTIAL_ARCHIVE_REGRESSION"
	for name, content := range map[string]string{
		"auth.ps1":      "$env:TEST_TOKEN='" + marker + "'",
		"mcp.json":      `{"mcpServers":{"http":{"url":"https://example.test/mcp","headers":{"Authorization":"Bearer ` + marker + `"}},"stdio":{"command":"synthetic","env":{"TOKEN":"` + marker + `"}}}}`,
		"pipeline.json": `{"synthetic_nonsecret_policy":true}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.Cleanup(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(f.stateDir, ArchiveDirName)
	policies := 0
	if err := filepath.WalkDir(archive, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), marker) {
			t.Error("synthetic credential survived in archive")
		}
		if entry.Name() == "mcp.json" || entry.Name() == "auth.ps1" {
			t.Error("generated credential input was archived")
		}
		if entry.Name() == "pipeline.json" {
			policies++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if policies != 1 {
		t.Fatalf("nonsecret policy copies=%d, want 1", policies)
	}
}
