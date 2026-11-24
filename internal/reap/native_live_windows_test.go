package reap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A host that ends removes its own record, so a record listed but gone by the
// time it is read belongs to a host that has just ended: nothing is unknown,
// and the dead goblin's dev server is reported rather than held.
func TestAHostRecordGoneSinceTheListingIsAnEndedHost(t *testing.T) {
	h, worktree := nativeHome(t)
	recordFile := filepath.Join(h.State, "hosts", "board.json")
	if err := os.Remove(recordFile); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", recordFile, filepath.Join(h.State, "ended")).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	processes := stubProcesses{
		process(410, 400, "claude.exe", `claude --dangerously-skip-permissions`, fixtureLatest),
		process(420, 410, "node.exe", `node `+worktree+`\node_modules\vite\bin\vite.js`, fixtureLatest.Add(time.Minute)),
	}

	inv, notes, err := Collector{Home: h, Session: "default", Processes: processes}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	findings := Classify(inv)

	if slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, "state/hosts/board.json") }) {
		t.Errorf("notes %q name a record whose host has ended", notes)
	}
	reported := false
	for _, finding := range classOf(findings, StaleServer) {
		if finding.TaskID == "board" && heldOnHostRecord(finding) {
			t.Errorf("the dev server of a goblin whose host has ended is held on its host record: %v", lines(findings))
		}
		reported = reported || finding.TaskID == "board"
	}
	if !reported {
		t.Errorf("the dev server of a goblin whose host has ended was not reported: %v", lines(findings))
	}
}
