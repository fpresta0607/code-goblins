package layout

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// legacyHome is a home whose data predates the layout, shaped like the one
// the fleet has run on since August: a backlog with hand-parked rows, task
// folders in every state, the Overlord's files, and a data folder that is a
// git repository of its own.
func legacyHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	day := 24 * time.Hour
	write(t, filepath.Join(h.Data, Backlog), "# Backlog\r\n\r\n## Queued\r\n- **q1** - Resume f2\r\n  detail: start from data/f2/handoff.md\r\n\r\n## Parked\r\n- parked **p1** - Set aside by hand\r\n")
	write(t, filepath.Join(h.Data, "overlord.md"), "a directive, word for word\n")
	write(t, filepath.Join(h.Data, "memory-archive.md"), "retired memory\n")
	write(t, filepath.Join(h.Data, "routing.json"), "{}\n")
	write(t, filepath.Join(h.Data, ".git", "objects", "ab", "cdef"), "a git object\n")
	write(t, filepath.Join(h.Data, "ops", "weekly-report", "run.ps1"), "the Overlord's own script\n")
	write(t, filepath.Join(h.Data, "projects", "acme", "auth.json"), "{}\n")
	write(t, filepath.Join(h.Data, "archive", "parked", "p0", "brief.md"), "parked by hand before the layout\n")
	taskFolder(t, h, "f1", 5*day)
	write(t, filepath.Join(h.State, "f1.status"), "done: PR https://github.com/o/r/pull/1\n")
	taskFolder(t, h, "f2", 5*day)
	write(t, filepath.Join(h.Data, "f2", "handoff.md"), "where f2 stopped\n")
	write(t, filepath.Join(h.State, "f2.status"), "done: PR https://github.com/o/r/pull/2\n")
	taskFolder(t, h, "g1", 5*day)
	write(t, filepath.Join(h.State, "g1.meta"), "id=g1\n")
	taskFolder(t, h, "s1", 5*day)
	taskFolder(t, h, "b1", time.Hour)
	return h
}

// harnessMemory is a harness's memory folder, as Claude Code keeps it.
func harnessMemory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "memory")
	write(t, filepath.Join(dir, "MEMORY.md"), "# Memory index\n\n- [Gate needs a scratch home](gate-needs-a-scratch-home.md) - unset CFO_HOME\n- [Overlord pronouns](overlord-pronouns.md) - he/him\n")
	write(t, filepath.Join(dir, "gate-needs-a-scratch-home.md"), "---\nname: gate-needs-a-scratch-home\n---\nfact one\n")
	write(t, filepath.Join(dir, "overlord-pronouns.md"), "---\nname: overlord-pronouns\n---\nfact two\n")
	return dir
}

func TestPlanMigrationChangesNothingAndProvesEveryFileKept(t *testing.T) {
	h := legacyHome(t)
	memory := harnessMemory(t)
	before, source := tree(t, h.Data), tree(t, memory)

	m, err := PlanMigration(h, memory, filingNow)

	if err != nil {
		t.Fatal(err)
	}
	if after := tree(t, h.Data); !maps.Equal(before, after) {
		t.Errorf("planning changed the data folder:\nbefore %v\nafter  %v", before, after)
	}
	if after := tree(t, memory); !maps.Equal(source, after) {
		t.Errorf("planning changed the harness memory folder")
	}
	var moved, kept []string
	for _, move := range m.Moves {
		moved = append(moved, dataRel(h, move.From)+" -> "+dataRel(h, move.To))
	}
	for _, k := range m.Kept {
		kept = append(kept, k.ID)
	}
	if want := []string{"f1 -> archive/finished/f1", "s1 -> archive/parked/s1"}; !slices.Equal(moved, want) {
		t.Errorf("moves = %v, want %v", moved, want)
	}
	if want := []string{"b1", "f2", "g1"}; !slices.Equal(kept, want) {
		t.Errorf("kept = %v, want %v", kept, want)
	}
	var written []string
	for _, w := range m.Writes {
		written = append(written, w.Path)
	}
	slices.Sort(written)
	if want := []string{Marker, FilingLog, Backlog, "memory/MEMORY.md", "memory/gate-needs-a-scratch-home.md", "memory/overlord-pronouns.md"}; !slices.Equal(written, want) {
		t.Errorf("writes = %v, want %v", written, want)
	}
	if len(m.Before) != len(before)-countDirs(before) {
		t.Errorf("Before holds %d files, the folder %d", len(m.Before), len(before)-countDirs(before))
	}
	creates := 0
	for _, w := range m.Writes {
		if w.Before == "" {
			creates++
		}
	}
	if len(m.After) != len(m.Before)+creates {
		t.Errorf("After holds %d files, want the %d there were plus %d created", len(m.After), len(m.Before), creates)
	}
	proof := m.Proof()
	if len(proof.Dropped) != 0 || proof.BeforeDigest != proof.AfterDigest {
		t.Errorf("proof = %+v, want nothing dropped and equal digests", proof)
	}
	if proof.Moved != 4 || proof.Changed != 1 || proof.Created != 5 || proof.Unchanged != proof.Before-5 || proof.After != proof.Before+5 {
		t.Errorf("proof counts = %+v, want 4 moved, the backlog changed, 5 created", proof)
	}
}

// A digest over a folder that lost a file is not the digest over the plan.
func TestProofDigestsDifferWhenAFileIsLost(t *testing.T) {
	h := legacyHome(t)
	m, err := PlanMigration(h, "", filingNow)
	if err != nil {
		t.Fatal(err)
	}

	delete(m.After, "archive/finished/f1/brief.md")

	if proof := m.Proof(); proof.BeforeDigest == proof.AfterDigest || len(proof.Dropped) != 1 {
		t.Errorf("proof = %+v, want different digests and f1/brief.md dropped", proof)
	}
}

func TestApplyMigrationBacksUpThenLeavesExactlyThePlan(t *testing.T) {
	h := legacyHome(t)
	m, err := PlanMigration(h, harnessMemory(t), filingNow)
	if err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(h.State, "backups", "migrate")

	result, err := m.Apply(backupDir)

	if err != nil {
		t.Fatal(err)
	}
	backedUp, err := HashTree(filepath.Join(backupDir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(backedUp, m.Before) {
		t.Errorf("the backup is not the data folder as it was")
	}
	actual, err := HashTree(h.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(actual, m.After) || len(result.Unexpected) != 0 {
		t.Errorf("the data folder is not what the plan predicted; unexpected %v", result.Unexpected)
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		t.Fatal(err)
	}
	var parked []string
	for _, row := range backlog.Parked {
		parked = append(parked, row.ID)
	}
	if !slices.Equal(parked, []string{"p1", "s1"}) || len(backlog.Queued) == 0 || backlog.Queued[0].ID != "q1" {
		t.Errorf("backlog = queued %+v, parked %v; want q1 queued and p1, s1 parked", backlog.Queued, parked)
	}
	if _, err := os.Stat(filepath.Join(h.Data, "f2", "handoff.md")); err != nil {
		t.Errorf("f2, which an open row points at, moved: %v", err)
	}

	again, err := File(h, filingNow.Add(time.Hour))
	if err != nil || len(again) != 0 {
		t.Errorf("filing the migrated home = %+v, %v; want nothing left to file", again, err)
	}
}

// Something that changes the backlog between the plan and the apply would
// have its change overwritten, so the migration refuses instead.
func TestApplyMigrationRefusesWhatChangedSinceThePlan(t *testing.T) {
	cases := map[string]func(t *testing.T, h home.Home){
		"the backlog": func(t *testing.T, h home.Home) {
			write(t, filepath.Join(h.Data, Backlog), "# Backlog\n\n## Queued\n- [ ] q9 - written a moment ago\n")
		},
		"a folder it moves": func(t *testing.T, h home.Home) {
			write(t, filepath.Join(h.Data, "f1", "late.md"), "written a moment ago\n")
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			h := legacyHome(t)
			m, err := PlanMigration(h, "", filingNow)
			if err != nil {
				t.Fatal(err)
			}
			change(t, h)
			before := tree(t, h.Data)

			_, err = m.Apply(filepath.Join(h.State, "backups", "migrate"))

			if err == nil || !strings.Contains(err.Error(), "changed since the plan") {
				t.Fatalf("Apply = %v, want a refusal naming the change", err)
			}
			if after := tree(t, h.Data); !maps.Equal(before, after) {
				t.Errorf("a refused migration changed the data folder")
			}
		})
	}
}

func TestPlanMigrationImportsMemoryWithoutOverwritingTheHomes(t *testing.T) {
	h := legacyHome(t)
	write(t, filepath.Join(h.Data, "memory", "MEMORY.md"), "# Memory index\r\n\r\n- [Overlord pronouns](overlord-pronouns.md) - he/him, as the home has it\r\n")
	write(t, filepath.Join(h.Data, "memory", "overlord-pronouns.md"), "the home's own version\n")

	m, err := PlanMigration(h, harnessMemory(t), filingNow)

	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	for _, w := range m.Writes {
		contents[w.Path] = string(w.Content)
	}
	if want := "# Memory index\r\n\r\n- [Overlord pronouns](overlord-pronouns.md) - he/him, as the home has it\r\n" + importStamp(m) + "\r\n- [Gate needs a scratch home](gate-needs-a-scratch-home.md) - unset CFO_HOME\r\n"; contents["memory/MEMORY.md"] != want {
		t.Errorf("index = %q, want the home's lines and one new line for the fact it lacked", contents["memory/MEMORY.md"])
	}
	if _, ok := contents["memory/overlord-pronouns.md"]; ok {
		t.Error("the import overwrote the home's own overlord-pronouns.md")
	}
	if len(m.NotImported) != 1 || !strings.HasPrefix(m.NotImported[0], "overlord-pronouns.md") {
		t.Errorf("not imported = %v, want the differing overlord-pronouns.md reported", m.NotImported)
	}
}

func importStamp(m Migration) string {
	return "Imported once from " + m.MemoryFrom + " at " + filingNow.Format(time.RFC3339) + "; that folder was only read and is left as it was."
}

// The import is a one-time copy that says where and when it came from: the
// index is stamped with its source and time, and every fact is the source's
// file byte for byte.
func TestPlanMigrationStampsTheImportAndCopiesFactsExactly(t *testing.T) {
	h := legacyHome(t)
	memory := harnessMemory(t)

	m, err := PlanMigration(h, memory, filingNow)

	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	for _, w := range m.Writes {
		contents[w.Path] = string(w.Content)
	}
	if want := "# Memory index\n\n" + importStamp(m) + "\n\n- [Gate needs a scratch home](gate-needs-a-scratch-home.md) - unset CFO_HOME\n- [Overlord pronouns](overlord-pronouns.md) - he/him\n"; contents["memory/MEMORY.md"] != want {
		t.Errorf("index = %q, want the source's index stamped with where and when it came from", contents["memory/MEMORY.md"])
	}
	for _, fact := range []string{"gate-needs-a-scratch-home.md", "overlord-pronouns.md"} {
		source, err := os.ReadFile(filepath.Join(memory, fact))
		if err != nil {
			t.Fatal(err)
		}
		if contents["memory/"+fact] != string(source) {
			t.Errorf("memory/%s is not the source's file byte for byte", fact)
		}
	}
}

func countDirs(paths map[string]string) int {
	dirs := 0
	for _, content := range paths {
		if content == "<dir>" {
			dirs++
		}
	}
	return dirs
}
