package codegoblins

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// tailnetName is a MagicDNS name: a machine, then its tailnet, under ts.net.
// A real one names a person's machine on their own private network, and Code
// Goblins is open source, so the repository holds only names whose tailnet is
// example, as an example site is example.com.
var tailnetName = regexp.MustCompile(`(?i)\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.ts\.net\b`)

// realTailnetNames are the MagicDNS names in text whose tailnet is not
// example, each with its line.
func realTailnetNames(text []byte) []string {
	var found []string
	for number, line := range bytes.Split(text, []byte("\n")) {
		for _, name := range tailnetName.FindAll(line, -1) {
			if lower := strings.ToLower(string(name)); lower != "example.ts.net" && !strings.HasSuffix(lower, ".example.ts.net") {
				found = append(found, fmt.Sprintf("%d: %s", number+1, name))
			}
		}
	}
	return found
}

func TestRealTailnetNamesAreFoundAndExamplesAreNot(t *testing.T) {
	// Arrange: the names are joined here so this file holds none itself.
	machine := "pc." + "tail1234abcd" + ".ts.net"
	renamed := "desk." + "yak-bebop" + ".ts.net"
	text := strings.Join([]string{
		"open https://" + machine + ":4311/session/1",
		"http://" + renamed + ":4387/",
		"https://pc.example.ts.net:4311 and home.example.ts.net.evil, not https://*.example.ts.net",
		`a host ends in ".ts.net", as *.ts.net names do`,
	}, "\n")

	// Act
	found := realTailnetNames([]byte(text))

	// Assert
	if want := []string{"1: " + machine, "2: " + renamed}; !slices.Equal(found, want) {
		t.Fatalf("found %q, want %q", found, want)
	}
}

func TestNoTrackedFileNamesARealTailnet(t *testing.T) {
	// Arrange
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(files) < 100 {
		t.Fatalf("git ls-files listed %d files, too few to be this repository", len(files))
	}

	// Act
	findings, err := tailnetFindings(files)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(findings) > 0 {
		t.Errorf("these name a real tailnet; use a name under example.ts.net, such as pc.example.ts.net:\n%s", strings.Join(findings, "\n"))
	}
}

func TestATrackedFileDeletedButNotStagedIsSkipped(t *testing.T) {
	// Arrange: the name is joined here so this file holds none itself.
	dir := t.TempDir()
	written := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(written, []byte("open https://"+"pc."+"tail1234abcd"+".ts.net"+":4311/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deleted := filepath.Join(dir, "deleted.md")

	// Act
	findings, err := tailnetFindings([]string{deleted, written})

	// Assert
	if err != nil {
		t.Fatalf("tailnetFindings: %v", err)
	}
	if len(findings) != 1 || !strings.HasPrefix(findings[0], written+":") {
		t.Fatalf("findings %q, want one naming %s", findings, written)
	}
}

// tailnetFindings are the real tailnet names in files, each with its file
// and line. A file deleted but not yet staged names nothing, and a binary
// file is not read as text.
func tailnetFindings(files []string) ([]string, error) {
	var findings []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			continue
		}
		for _, found := range realTailnetNames(data) {
			findings = append(findings, file+":"+found)
		}
	}
	return findings, nil
}
