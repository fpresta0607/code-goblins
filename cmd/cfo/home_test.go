package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/layout"
)

// migrationHome is a scratch home whose data predates the layout: one
// finished task, one live task, and the backlog.
func migrationHome(t *testing.T) home.Home {
	t.Helper()
	h := testHome(t)
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	for path, content := range map[string]string{
		filepath.Join(h.Data, "backlog.md"):           "# Backlog\n\n## Queued\n\n## Done\n",
		filepath.Join(h.Data, "done1", "brief.md"):    "# Brief done1\n",
		filepath.Join(h.Data, "done1", "report.md"):   "what done1 found\n",
		filepath.Join(h.State, "done1.status"):        "done: PR https://github.com/o/r/pull/1\n",
		filepath.Join(h.Data, "live1", "brief.md"):    "# Brief live1\n",
		filepath.Join(h.State, "live1.meta"):          "id=live1\n",
		filepath.Join(h.Data, "overlord.md"):          "a directive\n",
		filepath.Join(h.Data, ".git", "objects", "x"): "a git object\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The briefs were written before their tasks ran.
	briefed := time.Now().Add(-time.Hour)
	for _, id := range []string{"done1", "live1"} {
		if err := os.Chtimes(filepath.Join(h.Data, id, "brief.md"), briefed, briefed); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestHomeMigrateDryRunListsEveryFileAndChangesNothing(t *testing.T) {
	h := migrationHome(t)
	before, err := layout.HashTree(h.Data)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := run([]string{"home", "migrate"}, &stdout, &stderr)

	if exit != 0 {
		t.Fatalf("exit = %d, stderr %s", exit, stderr.String())
	}
	after, err := layout.HashTree(h.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the dry run changed the data folder: %d files before, %d after", len(before), len(after))
	}
	for rel, sum := range before {
		if after[rel] != sum {
			t.Errorf("the dry run changed data/%s", rel)
		}
	}
	out := stdout.String()
	for _, want := range []string{
		"nothing was changed",
		"move   data/done1/brief.md -> data/archive/finished/done1/brief.md  " + before["done1/brief.md"],
		"move   data/done1/report.md -> data/archive/finished/done1/report.md  " + before["done1/report.md"],
		"change data/backlog.md  " + before["backlog.md"],
		"create data/.layout",
		"stays  data/live1 (under way)",
		"before: 6 files",
		"dropped: 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, out)
		}
	}
	var digests []string
	for _, line := range strings.Split(out, "\n") {
		if _, digest, ok := strings.Cut(line, "SHA-256, "); ok {
			digests = append(digests, strings.TrimSpace(digest[strings.Index(digest, ":")+1:]))
		}
	}
	if len(digests) != 2 || digests[0] != digests[1] {
		t.Errorf("digests before and after = %v, want two equal digests", digests)
	}
}

func TestHomeMigrateApplyBacksUpAndLaysOutTheData(t *testing.T) {
	h := migrationHome(t)
	before, err := layout.HashTree(h.Data)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := run([]string{"home", "migrate", "--apply"}, &stdout, &stderr)

	if exit != 0 {
		t.Fatalf("exit = %d, stderr %s\n%s", exit, stderr.String(), stdout.String())
	}
	backups, err := filepath.Glob(filepath.Join(h.State, "backups", "home-migrate-*", "data"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v; want one", backups, err)
	}
	backedUp, err := layout.HashTree(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(backedUp) != len(before) {
		t.Errorf("the backup holds %d files, the data folder held %d", len(backedUp), len(before))
	}
	for rel, sum := range before {
		if backedUp[rel] != sum {
			t.Errorf("the backup's data/%s is not the original", rel)
		}
	}
	for _, path := range []string{layout.Marker, "archive/finished/done1/report.md", "live1/brief.md"} {
		if _, err := os.Stat(filepath.Join(h.Data, filepath.FromSlash(path))); err != nil {
			t.Errorf("data/%s missing after the migration: %v", path, err)
		}
	}
	if !strings.Contains(stdout.String(), "migrated: every file moved or written matches the plan") {
		t.Errorf("the migration did not report its check:\n%s", stdout.String())
	}
}

func TestHomeMigrateRefusesAMemoryFolderThatIsNotThere(t *testing.T) {
	migrationHome(t)

	var stdout, stderr bytes.Buffer
	exit := run([]string{"home", "migrate", "--memory-from", filepath.Join(t.TempDir(), "absent")}, &stdout, &stderr)

	if exit != 1 || !strings.Contains(stderr.String(), "is not a folder") {
		t.Errorf("exit = %d, stderr %q; want a refusal naming the folder", exit, stderr.String())
	}
}
