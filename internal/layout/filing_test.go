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

var filingNow = time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)

// filingHome is a laid-out home whose backlog is the given text.
func filingHome(t *testing.T, backlog string) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(filepath.Join(h.State, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(h.Data); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(h.Data, Backlog), backlog)
	return h
}

// taskFolder writes data/<id> with a brief last changed age ago, plus a
// report, so a test can prove a move carried the whole folder.
func taskFolder(t *testing.T, h home.Home, id string, age time.Duration) {
	t.Helper()
	brief := filepath.Join(h.Data, id, "brief.md")
	write(t, brief, "# Brief "+id+"\n")
	write(t, filepath.Join(h.Data, id, "evidence", "report.md"), "what "+id+" found\n")
	when := filingNow.Add(-age)
	if err := os.Chtimes(brief, when, when); err != nil {
		t.Fatal(err)
	}
}

const emptyBacklog = "# Backlog\n\n## Queued\n\n## Parked\n\n## Done\n"

// finishedAt writes a task record last changed age before filingNow.
func finishedAt(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	write(t, path, content)
	when := filingNow.Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestPlanFilesEachTaskFolderByWhatTheFleetRecords(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name    string
		backlog string
		arrange func(t *testing.T, h home.Home)
		want    []string // "archive <id>" or "park <id>"
	}{
		{"a live task stays", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", 10*day)
			write(t, filepath.Join(h.State, "g1.meta"), "id=g1\n")
			write(t, filepath.Join(h.State, "g1.status"), "working: on it\n")
		}, nil},
		{"a finished task is archived", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, []string{"archive g1"}},
		{"a task known only to the state archive is finished", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "archive", "g1.20260920T101010Z", "note"), "")
		}, []string{"archive g1"}},
		{"a brief written again after its task finished is a new brief", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			finishedAt(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n", 5*day)
		}, nil},
		{"a brief older than its task's last record is finished", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", 5*day)
			finishedAt(t, filepath.Join(h.State, "archive", "g1.20260920T101010Z"), "", day)
		}, []string{"archive g1"}},
		{"a brief written again after its archived run is a new brief", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			finishedAt(t, filepath.Join(h.State, "archive", "g1.status.20260920T101010Z"), "done\n", 5*day)
		}, nil},
		{"an archive entry for a longer dotted id is not this task's record", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "cg", 2*day)
			finishedAt(t, filepath.Join(h.State, "archive", "cg.v2.20260925T120000Z"), "", day)
		}, nil},
		{"a status archive entry for a longer dotted id is not this task's record", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "cg", 2*day)
			finishedAt(t, filepath.Join(h.State, "archive", "cg.v2.status.20260925T120000Z"), "", day)
		}, nil},
		{"the task's own archive entry records it", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "cg", 2*day)
			finishedAt(t, filepath.Join(h.State, "archive", "CG.20260925T120000Z"), "", day)
		}, []string{"archive cg"}},
		{"the task's own status archive entry records it", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "cg", 2*day)
			finishedAt(t, filepath.Join(h.State, "archive", "cg.status.20260925T120000Z"), "", day)
		}, []string{"archive cg"}},
		{"an archive entry without a stamp is not a record", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "cg", 2*day)
			finishedAt(t, filepath.Join(h.State, "archive", "cg.notes"), "", day)
		}, nil},
		{"a fresh brief waits to be dispatched", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", StaleBriefAge-time.Minute)
		}, nil},
		{"a stale brief is parked", emptyBacklog, func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", StaleBriefAge)
		}, []string{"park g1"}},
		{"a stale brief with a queued row is still queued", "## Queued\n- [ ] g1 - Waits for a slot\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", 10*day)
		}, nil},
		{"a brief whose row was parked follows it", "## Queued\n\n## Parked\n- parked **g1** - Set aside by the Overlord\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
		}, []string{"park g1"}},
		{"a finished task an open row points at stays", "## Queued\n- **g2** - Resume g1\n  detail: start from data/g1/handoff.md\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, nil},
		{"a parked row naming a folder with backslashes keeps it", "## Parked\n- parked **p1** - see data\\g1\\report.md\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, nil},
		{"a longer id in an open row is not a mention", "## Queued\n- **g10** - see data/g10/brief.md\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, []string{"archive g1"}},
		{"a queued row naming a metadata path is not a mention", "## Queued\n- **q9** - read metadata/g1/fields.md\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, []string{"archive g1"}},
		{"a queued row ending a sentence on a path keeps a stale brief queued", "## Queued\n- **q9** - start from data/g1.\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", 10*day)
		}, nil},
		{"a done row naming the task by id does not hold its folder", "## Done\n- [x] g1 - Shipped https://github.com/o/r/pull/1\n", func(t *testing.T, h home.Home) {
			taskFolder(t, h, "g1", time.Hour)
			write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
		}, []string{"archive g1"}},
		{"folders that are not tasks stay", emptyBacklog, func(t *testing.T, h home.Home) {
			write(t, filepath.Join(h.Data, "ops", "weekly-report", "run.ps1"), "")
			write(t, filepath.Join(h.Data, "memory", "brief.md"), "a fact about briefs\n")
			write(t, filepath.Join(h.Data, "projects", "brief.md"), "")
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := filingHome(t, c.backlog)
			c.arrange(t, h)

			moves, err := Plan(h, filepath.Join(h.Root, "harness-memory"), filingNow)

			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, move := range moves {
				verb := "archive"
				if move.Parked {
					verb = "park"
				}
				got = append(got, verb+" "+move.ID)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("plan = %v, want %v", got, c.want)
			}
		})
	}
}

// A finished folder that anything still read names by path stays where that
// path finds it: a live goblin's brief, the Overlord's directives, a memory
// fact or an open question pointing at data/f1/report.md must not dangle.
func TestPlanKeepsAFinishedFolderNamedInLiveText(t *testing.T) {
	mention := "see data/f1/report.md for what f1 found\n"
	cases := []struct {
		name    string
		backlog string
		arrange func(t *testing.T, h home.Home, harnessMemory string)
		stays   bool
	}{
		{"a done row", emptyBacklog + "- [x] d1 - Shipped, " + mention, func(t *testing.T, h home.Home, _ string) {}, true},
		{"a section filing does not read", emptyBacklog + "\n## Fleet defects\n" + mention, func(t *testing.T, h home.Home, _ string) {}, true},
		{"the directives", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.Data, "overlord.md"), `a ruling that cites data\f1\report.md`+"\n")
		}, true},
		{"the home's memory", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.Data, "memory", "a-fact.md"), mention)
		}, true},
		{"the harness's memory folder", emptyBacklog, func(t *testing.T, h home.Home, harnessMemory string) {
			write(t, filepath.Join(harnessMemory, "a-fact.md"), mention)
		}, true},
		{"a live task's brief", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			taskFolder(t, h, "g2", time.Hour)
			write(t, filepath.Join(h.Data, "g2", "brief.md"), "start from "+mention)
			write(t, filepath.Join(h.State, "g2.meta"), "brief="+filepath.Join(h.Data, "g2", "brief.md")+"\n")
		}, true},
		{"a brief not yet dispatched", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.Data, "b1", "brief.md"), "start from "+mention)
		}, true},
		{"an open question", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.State, ".supervisor.json"), `{"questions":[{"text":"Ship it? `+strings.TrimSpace(mention)+`","status":"pending"}]}`)
		}, true},
		{"an open review", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.State, ".supervisor.json"), `{"reviews":[{"title":"Read data/f1/report.md","state":"open"}]}`)
		}, true},
		{"an open review's Lavish page", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.State, ".supervisor.json"), `{"reviews":[{"title":"Pick one","state":"open","lavish_page":"C:\\home\\data\\f1\\deliverables\\options.html"}]}`)
		}, true},
		{"a run card waiting to run", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.State, ".supervisor.json"), `{"runs":[{"title":"Tidy","command":"Get-Content data\\f1\\report.md","state":"ready"}]}`)
		}, true},
		{"an answered question", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.State, ".supervisor.json"), `{"questions":[{"text":"`+strings.TrimSpace(mention)+`","status":"succeeded"}],"reviews":[{"title":"`+strings.TrimSpace(mention)+`","state":"cleared"}],"runs":[{"title":"t","command":"`+strings.TrimSpace(mention)+`","state":"finished"}]}`)
		}, false},
		{"another finished task's brief", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			finishedAt(t, filepath.Join(h.Data, "f2", "brief.md"), "start from "+mention, 5*24*time.Hour)
			finishedAt(t, filepath.Join(h.State, "f2.status"), "done\n", 0)
		}, false},
		{"a longer word ending in data", emptyBacklog, func(t *testing.T, h home.Home, _ string) {
			write(t, filepath.Join(h.Data, "overlord.md"), "the metadata/f1/report.md field\n")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := filingHome(t, c.backlog)
			harnessMemory := filepath.Join(h.Root, "harness-memory")
			taskFolder(t, h, "f1", 5*24*time.Hour)
			finishedAt(t, filepath.Join(h.State, "f1.status"), "done: PR https://github.com/o/r/pull/1\n", 0)
			c.arrange(t, h, harnessMemory)

			moves, err := Plan(h, harnessMemory, filingNow)

			if err != nil {
				t.Fatal(err)
			}
			archived := slices.ContainsFunc(moves, func(m Move) bool { return m.ID == "f1" })
			if archived == c.stays {
				t.Errorf("f1 archived = %v, want it to stay = %v", archived, c.stays)
			}
		})
	}
}

func TestFileMovesWholeFoldersAndRecordsWhere(t *testing.T) {
	h := filingHome(t, "# Backlog\r\n\r\n## Queued\r\n- [ ] q1 - Queued work\r\n\r\n## Parked\r\n- parked **g3** - Set aside by hand\r\n\r\n## Done\r\n")
	taskFolder(t, h, "g1", time.Hour)
	write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
	taskFolder(t, h, "g2", StaleBriefAge+time.Hour)
	taskFolder(t, h, "g3", time.Hour)
	before := map[string]map[string]string{}
	for _, id := range []string{"g1", "g2", "g3"} {
		before[id] = tree(t, filepath.Join(h.Data, id))
	}

	moved, err := File(h, filingNow)

	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 3 {
		t.Fatalf("filed %d folders, want 3: %+v", len(moved), moved)
	}
	for id, dir := range map[string]string{"g1": "archive/finished/g1", "g2": "archive/parked/g2", "g3": "archive/parked/g3"} {
		if _, err := os.Stat(filepath.Join(h.Data, id)); !os.IsNotExist(err) {
			t.Errorf("data/%s is still in place: %v", id, err)
		}
		if after := tree(t, filepath.Join(h.Data, filepath.FromSlash(dir))); !maps.Equal(before[id], after) {
			t.Errorf("data/%s does not hold exactly what data/%s held:\nbefore %v\nafter  %v", dir, id, before[id], after)
		}
	}

	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		t.Fatal(err)
	}
	var parked []string
	for _, row := range backlog.Parked {
		parked = append(parked, row.ID)
	}
	if !slices.Equal(parked, []string{"g3", "g2"}) {
		t.Errorf("parked rows = %v, want g3 as it was and one new row for g2", parked)
	}
	if len(backlog.Queued) != 1 || backlog.Queued[0].ID != "q1" {
		t.Errorf("queued rows = %+v, want q1 alone", backlog.Queued)
	}
	raw, err := os.ReadFile(filepath.Join(h.Data, Backlog))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != strings.Count(string(raw), "\r\n") {
		t.Errorf("the parked row broke the backlog's CRLF line endings:\n%q", raw)
	}
	if !strings.Contains(string(raw), "data/archive/parked/g2/brief.md") {
		t.Errorf("g2's parked row does not say where its brief went:\n%s", raw)
	}

	log, err := os.ReadFile(filepath.Join(h.Data, filepath.FromSlash(FilingLog)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"data/g1 -> data/archive/finished/g1", "data/g2 -> data/archive/parked/g2", "data/g3 -> data/archive/parked/g3"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("the filing log does not record %q:\n%s", want, log)
		}
	}
}

func TestFileAddsTheParkedSectionWhereItIsMissing(t *testing.T) {
	h := filingHome(t, "# Backlog\n\n## Queued\n- [ ] q1 - Queued work\n\n## Done\n- [x] d1 - Shipped\n")
	taskFolder(t, h, "g1", StaleBriefAge)

	if _, err := File(h, filingNow); err != nil {
		t.Fatal(err)
	}

	if got, want := headings(t, filepath.Join(h.Data, Backlog)), []string{"Queued", "Parked", "Done"}; !slices.Equal(got, want) {
		t.Errorf("backlog sections = %v, want %v", got, want)
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlog.Parked) != 1 || backlog.Parked[0].ID != "g1" || len(backlog.Done) != 1 || len(backlog.Queued) != 1 {
		t.Errorf("backlog = %+v, want g1 parked and the other rows where they were", backlog)
	}
}

// A home without the marker predates the layout: filing it would be the
// migration its owner has not agreed to.
func TestFileLeavesAHomeFromBeforeTheLayoutAlone(t *testing.T) {
	h := filingHome(t, emptyBacklog)
	if err := os.Remove(filepath.Join(h.Data, Marker)); err != nil {
		t.Fatal(err)
	}
	taskFolder(t, h, "g1", time.Hour)
	write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
	taskFolder(t, h, "g2", StaleBriefAge)
	before := tree(t, h.Data)

	moved, err := File(h, filingNow)

	if err != nil || len(moved) != 0 {
		t.Fatalf("File = %+v, %v; want nothing filed", moved, err)
	}
	if after := tree(t, h.Data); !maps.Equal(before, after) {
		t.Errorf("File changed a home from before the layout:\nbefore %v\nafter  %v", before, after)
	}
}

// A task id can finish twice: a second archive must not land on the first.
func TestFileKeepsAnEarlierArchiveOfTheSameTask(t *testing.T) {
	h := filingHome(t, emptyBacklog)
	write(t, filepath.Join(h.Data, "archive", "finished", "g1", "brief.md"), "the first run\n")
	taskFolder(t, h, "g1", time.Hour)
	write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/2\n")

	moved, err := File(h, filingNow)

	if err != nil || len(moved) != 1 {
		t.Fatalf("File = %+v, %v; want g1 archived", moved, err)
	}
	if got := tree(t, filepath.Join(h.Data, "archive", "finished", "g1"))["brief.md"]; got != "the first run\n" {
		t.Errorf("the earlier archive of g1 was overwritten: %q", got)
	}
	if want := filepath.Join(h.Data, "archive", "finished", "g1."+filingNow.Format("20060102T150405Z")); moved[0].To != want {
		t.Errorf("second archive went to %s, want %s", moved[0].To, want)
	}
}

// A pass that cannot move a folder leaves it where it was, says so in the
// filing log once rather than every pass, and files it once it can.
func TestFileRecordsAFailedPassOnceAndRecovers(t *testing.T) {
	h := filingHome(t, emptyBacklog)
	taskFolder(t, h, "g1", time.Hour)
	write(t, filepath.Join(h.State, "g1.status"), "done: PR https://github.com/o/r/pull/1\n")
	finished := filepath.Join(h.Data, "archive", "finished")
	if err := os.Remove(finished); err != nil {
		t.Fatal(err)
	}
	write(t, finished, "a file where the archive folder should be")

	for pass := 0; pass < 2; pass++ {
		if _, err := File(h, filingNow.Add(time.Duration(pass)*time.Hour)); err == nil {
			t.Fatalf("pass %d filed g1 into a file", pass)
		}
	}
	if _, err := os.Stat(filepath.Join(h.Data, "g1", "brief.md")); err != nil {
		t.Fatalf("a failed pass moved g1: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(h.Data, filepath.FromSlash(FilingLog)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), "could not file") != 1 {
		t.Errorf("the filing log records the same failure %d times, want once:\n%s", strings.Count(string(log), "could not file"), log)
	}

	if err := os.Remove(finished); err != nil {
		t.Fatal(err)
	}
	if moved, err := File(h, filingNow.Add(2*time.Hour)); err != nil || len(moved) != 1 {
		t.Fatalf("File after the obstacle went = %+v, %v; want g1 archived", moved, err)
	}
}

// A folder a process holds open must not keep every later folder in place:
// the pass files what it can and records the failure once.
func TestFileGoesOnPastAMoveThatFails(t *testing.T) {
	h := filingHome(t, emptyBacklog)
	taskFolder(t, h, "a1", StaleBriefAge)
	taskFolder(t, h, "b2", time.Hour)
	write(t, filepath.Join(h.State, "b2.status"), "done: PR https://github.com/o/r/pull/1\n")
	parked := filepath.Join(h.Data, "archive", "parked")
	if err := os.Remove(parked); err != nil {
		t.Fatal(err)
	}
	write(t, parked, "a file where the parked folder should be")

	moved, err := File(h, filingNow)

	if err == nil {
		t.Fatal("File parked a1 into a file")
	}
	if len(moved) != 1 || moved[0].ID != "b2" {
		t.Fatalf("filed %+v, want b2 alone", moved)
	}
	if _, err := os.Stat(filepath.Join(h.Data, "a1", "brief.md")); err != nil {
		t.Errorf("the failed move took a1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.Data, "archive", "finished", "b2", "brief.md")); err != nil {
		t.Errorf("b2 was not archived: %v", err)
	}
	if _, err := File(h, filingNow.Add(time.Hour)); err == nil {
		t.Fatal("the second pass parked a1 into a file")
	}
	log, err := os.ReadFile(filepath.Join(h.Data, filepath.FromSlash(FilingLog)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "data/b2 -> data/archive/finished/b2") {
		t.Errorf("the filing log does not record b2's move:\n%s", log)
	}
	if strings.Count(string(log), "could not file") != 1 {
		t.Errorf("the filing log records the same failure %d times, want once:\n%s", strings.Count(string(log), "could not file"), log)
	}
}
