package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparedFixture(t *testing.T) (string, PreparedInventory) {
	t.Helper()
	root := t.TempDir()
	inventory := PreparedInventory{Identity: "headless-shell-1243-fixture"}
	for _, name := range []string{"runtime/v8_context_snapshot.bin", "runtime/vulkan-1.dll", "runtime/vk_swiftshader.dll"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data := []byte("synthetic fixture: " + name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		inventory.Files = append(inventory.Files, PreparedFile{Path: name, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])})
	}
	writePreparedEvidence(t, root, `{"identity":"headless-shell-1243-fixture","state":"complete"}`)
	return root, inventory
}

func writePreparedEvidence(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, PreparedEvidenceFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedGenerationRequiresCompleteEvidence(t *testing.T) {
	for _, test := range []struct {
		name, evidence, want string
	}{
		{"complete", `{"identity":"headless-shell-1243-fixture","state":"complete"}`, ""},
		{"incomplete", `{"identity":"headless-shell-1243-fixture","state":"incomplete"}`, "state"},
		{"interrupted", `{"identity":"headless-shell-1243-fixture","state":"interrupted"}`, "state"},
		{"unknown state", `{"identity":"headless-shell-1243-fixture","state":"ready"}`, "state"},
		{"missing state", `{"identity":"headless-shell-1243-fixture"}`, "state"},
		{"missing identity", `{"state":"complete"}`, "identity"},
		{"wrong identity", `{"identity":"other","state":"complete"}`, "identity"},
		{"truncated", `{"identity":`, "evidence"},
		{"null", `null`, "evidence"},
		{"extra document", `{"identity":"headless-shell-1243-fixture","state":"complete"}{}`, "evidence"},
		{"unknown field", `{"identity":"headless-shell-1243-fixture","state":"complete","command":"never-run"}`, "evidence"},
		{"conflicting states", `{"identity":"headless-shell-1243-fixture","state":"interrupted","state":"complete"}`, "evidence"},
		{"conflicting identities", `{"identity":"other","identity":"headless-shell-1243-fixture","state":"complete"}`, "evidence"},
		{"oversized", strings.Repeat(" ", 4097), "evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			writePreparedEvidence(t, root, test.evidence)

			err := ValidatePreparedGeneration(root, inventory)

			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPreparedGenerationRejectsInvalidRoots(t *testing.T) {
	for _, name := range []string{"empty", "missing", "file"} {
		t.Run(name, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			switch name {
			case "empty":
				root = ""
			case "missing":
				root = filepath.Join(root, "missing")
			case "file":
				root = filepath.Join(root, PreparedEvidenceFile)
			}

			err := ValidatePreparedGeneration(root, inventory)

			if err == nil {
				t.Fatal("invalid root passed")
			}
		})
	}
}

func TestPreparedGenerationRejectsSameKeyDamagedFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, *PreparedInventory)
		want   string
	}{
		{"missing snapshot", func(t *testing.T, root string, inventory *PreparedInventory) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(inventory.Files[0].Path))); err != nil {
				t.Fatal(err)
			}
		}, "v8_context_snapshot.bin"},
		{"missing vulkan", func(t *testing.T, root string, inventory *PreparedInventory) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(inventory.Files[1].Path))); err != nil {
				t.Fatal(err)
			}
		}, "vulkan-1.dll"},
		{"same size corruption", func(t *testing.T, root string, inventory *PreparedInventory) {
			file := inventory.Files[0]
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file.Path)), []byte(strings.Repeat("x", int(file.Size))), 0600); err != nil {
				t.Fatal(err)
			}
		}, "SHA-256"},
		{"wrong size", func(t *testing.T, root string, inventory *PreparedInventory) { inventory.Files[0].Size++ }, "size"},
		{"wrong hash", func(t *testing.T, root string, inventory *PreparedInventory) {
			inventory.Files[0].SHA256 = strings.Repeat("0", 64)
		}, "SHA-256"},
		{"directory instead of file", func(t *testing.T, root string, inventory *PreparedInventory) { inventory.Files[0].Path = "runtime" }, "regular file"},
		{"missing evidence", func(t *testing.T, root string, inventory *PreparedInventory) {
			if err := os.Remove(filepath.Join(root, PreparedEvidenceFile)); err != nil {
				t.Fatal(err)
			}
		}, "evidence"},
		{"evidence directory", func(t *testing.T, root string, inventory *PreparedInventory) {
			path := filepath.Join(root, PreparedEvidenceFile)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}, "evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			test.mutate(t, root, &inventory)

			err := ValidatePreparedGeneration(root, inventory)

			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPreparedGenerationRejectsInvalidRequirements(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*PreparedInventory)
	}{
		{"empty identity", func(inventory *PreparedInventory) { inventory.Identity = "" }},
		{"blank identity", func(inventory *PreparedInventory) { inventory.Identity = " " }},
		{"empty inventory", func(inventory *PreparedInventory) { inventory.Files = nil }},
		{"negative size", func(inventory *PreparedInventory) { inventory.Files[0].Size = -1 }},
		{"empty hash", func(inventory *PreparedInventory) { inventory.Files[0].SHA256 = "" }},
		{"invalid hash", func(inventory *PreparedInventory) { inventory.Files[0].SHA256 = strings.Repeat("g", 64) }},
		{"duplicate path", func(inventory *PreparedInventory) { inventory.Files = append(inventory.Files, inventory.Files[0]) }},
		{"case alias", func(inventory *PreparedInventory) {
			other := inventory.Files[0]
			other.Path = strings.ToUpper(other.Path)
			inventory.Files = append(inventory.Files, other)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			test.mutate(&inventory)

			err := ValidatePreparedGeneration(root, inventory)

			if err == nil {
				t.Fatal("invalid requirements passed")
			}
		})
	}
}

func TestPreparedGenerationRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{"", ".", "..", "../outside", "runtime/../../outside", "/absolute", `C:\outside`, `C:outside`, `\\server\share\file`, `runtime\v8_context_snapshot.bin`, "runtime/./v8_context_snapshot.bin", "runtime//v8_context_snapshot.bin", "runtime/file:stream", "runtime/file.", "runtime/file ", "runtime/\x00file"} {
		t.Run(path, func(t *testing.T) {
			root, inventory := preparedFixture(t)
			inventory.Files[0].Path = path

			err := ValidatePreparedGeneration(root, inventory)

			if err == nil || !strings.Contains(err.Error(), "path") {
				t.Fatalf("unsafe path passed: %v", err)
			}
		})
	}
}

func TestPreparedGenerationAcceptsEmptyRequiredFile(t *testing.T) {
	root, inventory := preparedFixture(t)
	digest := sha256.Sum256(nil)
	inventory.Files[0].Size = 0
	inventory.Files[0].SHA256 = hex.EncodeToString(digest[:])
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(inventory.Files[0].Path)), nil, 0600); err != nil {
		t.Fatal(err)
	}

	err := ValidatePreparedGeneration(root, inventory)

	if err != nil {
		t.Fatal(err)
	}
}
