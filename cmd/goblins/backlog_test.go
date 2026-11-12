package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBacklogDonePreservesLegacyAndCanonicalCompletionEvidence(t *testing.T) {
	for _, row := range []string{"- **delivered** - Ship it (repo: project)", "- [ ] delivered - Ship it (repo: project)"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(row+newline, func(t *testing.T) {
				// Arrange
				h := primaryHomeFixture(t)
				if err := os.MkdirAll(h.Data, 0o700); err != nil {
					t.Fatal(err)
				}
				body := "  Original evidence.\n\n  ## Continuation\n  - [ ] nested-note - Keep verbatim\n\tArchived source stays.\n"
				prefix := "# Backlog\n\n## Queued\n- [ ] next - Keep next\n"
				suffix := "\n## Parked\n- **parked** - Keep parked\n\n## Done\n- [x] earlier - Keep earlier\n"
				original := strings.ReplaceAll(prefix+row+"\n"+body+suffix, "\n", newline)
				path := filepath.Join(h.Data, "backlog.md")
				if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
				outcome := state.Outcome{ID: "delivered", Generation: "old", Phase: "done", Evidence: "reported pull request", PR: "https://github.com/example/project/pull/1", At: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)}
				if err := state.WriteOutcome(h.State, outcome); err != nil {
					t.Fatal(err)
				}
				archive := filepath.Join(h.State, state.ArchiveDirName, "delivered.20261004T000000Z")
				if err := os.MkdirAll(archive, 0o700); err != nil {
					t.Fatal(err)
				}
				evidencePath := filepath.Join(archive, "handoff.md")
				if err := os.WriteFile(evidencePath, []byte("Preserved archived source\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
				var stdout, stderr bytes.Buffer

				// Act
				exit := runWithRuntime([]string{"backlog", "done", "delivered"}, &stdout, &stderr, runtime)

				// Assert
				if exit != 0 {
					t.Fatalf("closeout exit=%d stderr=%s", exit, &stderr)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				closed := "- [x] delivered - Ship it (repo: project) " + outcome.PR + " (done 2026-10-04)\n" + body
				want := strings.ReplaceAll(prefix+"\n## Parked\n- **parked** - Keep parked\n\n## Done\n"+closed+"- [x] earlier - Keep earlier\n", "\n", newline)
				if string(got) != want {
					t.Fatalf("closeout changed unrelated source or continuation bytes:\ngot %q\nwant %q", got, want)
				}
				if evidence, err := os.ReadFile(evidencePath); err != nil || string(evidence) != "Preserved archived source\n" {
					t.Fatal("closeout changed archived source")
				}
				if preserved, err := state.ReadOutcome(h.State, "delivered"); err != nil || preserved != outcome {
					t.Fatalf("closeout changed delivery evidence: %+v %v", preserved, err)
				}
				stdout.Reset()
				if exit := runWithRuntime([]string{"backlog", "done", "delivered"}, &stdout, &stderr, runtime); exit != 0 {
					t.Fatalf("idempotent closeout exit=%d stderr=%s", exit, &stderr)
				}
				if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, got) {
					t.Fatal("repeated closeout changed the backlog")
				}
			})
		}
	}
}

func TestBacklogDoneRefusesUnprovenOrAmbiguousCompletion(t *testing.T) {
	for _, test := range []struct {
		name, phase, source, meta string
	}{
		{"missing outcome", "", "## Queued\n- **delivered** - Ship it\n", ""},
		{"stopped outcome", "stopped", "## Queued\n- [ ] delivered - Ship it\n", ""},
		{"active task", "done", "## Queued\n- **delivered** - Ship it\n", "id=delivered\n"},
		{"duplicate rows", "done", "## Queued\n- **delivered** - Ship it\n- [ ] delivered - Again\n", ""},
		{"parked task", "done", "## Parked\n- **delivered** - Keep parked\n", ""},
		{"unreadable outcome", "corrupt", "## Queued\n- [ ] delivered - Ship it\n", ""},
		{"missing delivery time", "done", "## Queued\n- **delivered** - Ship it\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			if err := os.MkdirAll(h.Data, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(h.Data, "backlog.md")
			if err := os.WriteFile(path, []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.phase == "corrupt" {
				if err := os.MkdirAll(filepath.Join(h.State, "outcomes"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.State, "outcomes", "delivered.json"), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if test.phase != "" {
				if err := state.WriteOutcome(h.State, state.Outcome{ID: "delivered", Phase: test.phase, Evidence: "reported pull request"}); err != nil {
					t.Fatal(err)
				}
			}
			if test.meta != "" {
				if err := os.WriteFile(state.TaskMetaPath(h.State, "delivered"), []byte(test.meta), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"backlog", "done", "delivered"}, &stdout, &stderr, commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }})

			// Assert
			if exit != 1 || stderr.Len() == 0 {
				t.Errorf("unsafe closeout exit=%d stderr=%s", exit, &stderr)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != test.source {
				t.Fatal("refused closeout changed backlog source")
			}
		})
	}
}

func TestBacklogDoneRefusesConcurrentQueueOrSpawnWrites(t *testing.T) {
	for _, name := range []string{".queued-delivered.lock", ".spawn.lock", ".backlog.lock"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			if err := os.MkdirAll(h.Data, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(h.Data, "backlog.md")
			source := "## Queued\n- **delivered** - Ship it\n"
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
				t.Fatal(err)
			}
			defer lock.ReleaseExclusiveNamed(h.State, name)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"backlog", "done", "delivered"}, &stdout, &stderr, commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }})

			// Assert
			if exit != 1 || !strings.Contains(stderr.String(), "held") {
				t.Errorf("concurrent closeout exit=%d stderr=%s", exit, &stderr)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != source {
				t.Fatal("concurrent closeout changed backlog source")
			}
		})
	}
}

func TestBacklogDoneCreatesTheDoneSectionWithoutDroppingSource(t *testing.T) {
	// Arrange
	h := primaryHomeFixture(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Data, "backlog.md")
	if err := os.WriteFile(path, []byte("## Queued\n- **delivered** - Ship it\n  Evidence without final newline"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "delivered", Phase: "done", Evidence: "requested report.md delivered", At: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"backlog", "done", "delivered"}, &stdout, &stderr, commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }})

	// Assert
	if exit != 0 {
		t.Fatalf("closeout exit=%d stderr=%s", exit, &stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "## Queued\n\n## Done\n- [x] delivered - Ship it (done 2026-10-04)\n  Evidence without final newline" {
		t.Fatalf("closeout loses source or line endings: %q %v", got, err)
	}
}
