//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A snapshot that reads nothing compares equal to any other, so each reader
// is held to finding what every Windows machine has.
func TestEachReaderFindsWhatEveryMachineHas(t *testing.T) {
	// Arrange
	snapshot := newSnapshot(time.Now())

	// Act
	listRegistry(&snapshot)
	listTasks(&snapshot)
	listProcesses(&snapshot)
	listVolumes(&snapshot)
	listCredentials(&snapshot)
	listVirtualDisks(&snapshot)

	// Assert
	for kind, fewest := range least {
		if kind == kindFile {
			continue
		}
		if got := snapshot.count(kind); got < fewest {
			t.Errorf("read %d of kind %s, want at least %d", got, kind, fewest)
		}
	}
	if _, ok := snapshot.Things[kindRegistryValue][`HKCU\Environment\Path`]; !ok {
		t.Errorf("the user's PATH is not among the registry values read")
	}
	if _, ok := snapshot.Things[kindVolume][os.Getenv("SystemDrive")+`\`]; !ok {
		t.Errorf("the system drive is not among the drives read: %v", snapshot.Things[kindVolume])
	}
	for _, unread := range snapshot.Unread {
		t.Logf("unread: %s", unread)
	}
}

func TestListFilesRecordsFoldersLinksAndTheContentItIsAskedFor(t *testing.T) {
	// Arrange
	root := t.TempDir()
	watched := filepath.Join(root, "watched")
	other := filepath.Join(root, "other")
	skipped := filepath.Join(root, "skipped")
	for _, folder := range []string{watched, other, skipped, filepath.Join(root, "target")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(watched, "settings.json"), filepath.Join(other, "big.bin"), filepath.Join(skipped, "cache.bin"), filepath.Join(root, "target", "inside.txt")} {
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, filepath.Join(root, "target")).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, output)
	}
	snapshot := newSnapshot(time.Now())

	// Act
	listFiles(&snapshot, root, []string{watched}, []string{skipped})

	// Assert
	files := snapshot.Things[kindFile]
	if got := files[watched]; got != "folder" {
		t.Errorf("a folder is recorded as %q, want folder", got)
	}
	if got := files[filepath.Join(watched, "settings.json")]; !strings.HasPrefix(got, "sha256 ") {
		t.Errorf("a file under a content folder is recorded as %q, want its hash", got)
	}
	if got, ok := files[filepath.Join(other, "big.bin")]; !ok || got != "" {
		t.Errorf("a file elsewhere is recorded as %q (%v), want it there with no hash", got, ok)
	}
	if got := files[link]; !strings.HasPrefix(got, "link to ") || !strings.HasSuffix(got, "target") {
		t.Errorf("a junction is recorded as %q, want its target", got)
	}
	if _, ok := files[filepath.Join(link, "inside.txt")]; ok {
		t.Errorf("the walk followed a junction")
	}
	if _, ok := files[filepath.Join(skipped, "cache.bin")]; ok {
		t.Errorf("the walk went into a skipped folder")
	}
}
