package siqspeak

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotDistinguishesMissingStoppedRunningAndUnreadable(t *testing.T) {
	for _, test := range []struct {
		name        string
		isInstalled bool
		isRunning   bool
		probeError  bool
		want        string
	}{
		{name: "missing", want: "missing"},
		{name: "stopped", isInstalled: true, want: "stopped"},
		{name: "running", isInstalled: true, isRunning: true, want: "running"},
		{name: "unreadable state", isInstalled: true, probeError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if test.isInstalled {
				if err := os.WriteFile(filepath.Join(directory, "dictate.py"), []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0

			got, err := snapshot(directory, func() (bool, error) {
				calls++
				if test.probeError {
					return false, errors.New("private process detail")
				}
				return test.isRunning, nil
			})

			if test.probeError {
				if err == nil || err.Error() != "SIQspeak running status could not be read" {
					t.Fatal("probe failure was hidden or exposed details")
				}
				return
			}
			if err != nil || got.State != test.want || got.Entries == nil {
				t.Fatalf("snapshot = %+v, %v", got, err)
			}
			if !test.isInstalled && calls != 0 {
				t.Fatal("missing installation should not probe the system")
			}
		})
	}
}

func TestStoppedAppKeepsHistoryAndReportsReadFailure(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "SIQspeak.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "transcriptions.jsonl")
	if err := os.WriteFile(filename, []byte("{\"text\":\"saved words\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	probe := func() (bool, error) { return false, nil }

	got, err := snapshot(directory, probe)

	if err != nil || got.State != "stopped" || len(got.Entries) != 1 || got.Entries[0].Text != "saved words" {
		t.Fatalf("snapshot = %+v, %v", got, err)
	}
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filename, 0700); err != nil {
		t.Fatal(err)
	}
	got, err = snapshot(directory, probe)
	if err != nil || got.State != "stopped" || got.Problem != "SIQspeak history could not be read" {
		t.Fatalf("read problem not preserved: %+v, %v", got, err)
	}
}

func TestLocateUsesExplicitDirectoryOrOneInstalledProjectsFolder(t *testing.T) {
	for _, test := range []struct {
		name      string
		folders   []string
		override  string
		want      string
		wantError bool
	}{
		{name: "no projects root"},
		{name: "explicit", override: "custom", want: "custom"},
		{name: "source", folders: []string{"SIQspeak-main"}, want: "SIQspeak-main"},
		{name: "case", folders: []string{"siqspeak"}, want: "siqspeak"},
		{name: "unrelated", folders: []string{"SIQspeak-backup"}},
		{name: "ambiguous", folders: []string{"SIQspeak", "SIQspeak-main"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, folder := range test.folders {
				directory := filepath.Join(root, folder)
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "dictate.py"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			override := ""
			if test.override != "" {
				override = filepath.Join(root, test.override)
			}
			got, err := Locate(root, override)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError {
				return
			}
			want := ""
			if test.want != "" {
				want = filepath.Join(root, test.want)
			}
			if got != want {
				t.Fatalf("directory = %q, want %q", got, want)
			}
		})
	}
	if got, err := Locate("", ""); err != nil || got != "" {
		t.Fatalf("empty root = %q, %v", got, err)
	}
	if _, err := Locate(filepath.Join(t.TempDir(), "missing"), ""); err == nil {
		t.Fatal("unreadable projects root was hidden")
	}
}

func TestLocateRejectsRelativeOverrideAndIgnoresAnUninstalledFolder(t *testing.T) {
	if _, err := Locate("", "relative/path"); err == nil {
		t.Fatal("relative installation folder accepted")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "SIQspeak", "dictate.py"), 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := Locate(root, "")
	if err != nil || directory != "" {
		t.Fatalf("non-file installation marker = %q, %v", directory, err)
	}
	got, err := Read("")
	if err != nil || got.State != "missing" || len(got.Entries) != 0 {
		t.Fatalf("missing installation = %+v, %v", got, err)
	}
}
