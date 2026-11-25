package connections

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// counters count what the snapshots' checks were asked, by level and scope.
type counters struct {
	mu     sync.Mutex
	counts map[string]int
	// block holds the check of the project blockOn until it is closed.
	block   chan struct{}
	blockOn string
}

func (c *counters) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[key]++
}

func (c *counters) get(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[key]
}

func (c *counters) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum int
	for _, count := range c.counts {
		sum += count
	}
	return sum
}

// snapshotFixture is snapshots over made-up projects acme and globex whose
// checks only count themselves, with a clock the test moves.
type snapshotFixture struct {
	*testEnvironment
	snapshots *Snapshots
	counters  *counters
	folder    string
	clock     time.Time
	running   []state.TaskMeta
}

func newSnapshotFixture(t *testing.T) *snapshotFixture {
	t.Helper()
	e := newTestEnvironment(t)
	for _, project := range []string{"acme", "globex"} {
		e.manifest(t, project, `[{"name":"sample","method":"env","env":["SAMPLE_VALUE"]}]`)
		e.checkout(t, project)
	}
	e.ProjectsRoot = func() string { return filepath.Dir(e.checkout(t, "acme")) }
	f := &snapshotFixture{testEnvironment: e, counters: &counters{counts: map[string]int{}}, folder: t.TempDir(), clock: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	f.snapshots = f.open(t)
	return f
}

// open makes snapshots over the fixture's folder, as a restart does.
func (f *snapshotFixture) open(t *testing.T) *Snapshots {
	t.Helper()
	snapshots := NewSnapshots(f.Environment, f.folder)
	snapshots.Now = func() time.Time { return f.clock }
	snapshots.CheckMachine = func(context.Context) Level {
		f.counters.add("machine")
		return Level{Items: []Item{{ID: "cli:acme-cli", Name: "acme-cli", Status: StatusSet}}}
	}
	snapshots.CheckProject = func(ctx context.Context, name, path string) Level {
		f.counters.add("project:" + name)
		if f.counters.block != nil && name == f.counters.blockOn {
			select {
			case <-f.counters.block:
			case <-ctx.Done():
			}
		}
		return Level{Items: []Item{{ID: "service:sample", Name: "sample", Status: StatusSet, Detail: path}}}
	}
	snapshots.ReadGoblin = func(meta state.TaskMeta, _ string, _ int64, _ Goblin) (Goblin, int64) {
		f.counters.add("goblin:" + meta.ID)
		return Goblin{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Project: filepath.Base(meta.Project)}, 0
	}
	snapshots.Transcript = func(state.TaskMeta) string { return "" }
	t.Cleanup(snapshots.Close)
	return snapshots
}

// goblin is a running goblin of a project.
func (f *snapshotFixture) goblin(project, task string) state.TaskMeta {
	return state.TaskMeta{ID: task, Project: filepath.Join(filepath.Dir(f.DataDir), "projects", project), SpawnGen: "g1", Harness: "claude"}
}

// tick is one look of the watch and the checks it queued, finished.
func (f *snapshotFixture) tick() {
	f.snapshots.tick(slices.Clone(f.running))
	f.snapshots.work.Wait()
}

func (f *snapshotFixture) project(t *testing.T, name string) Project {
	t.Helper()
	for _, project := range f.snapshots.Read().Projects {
		if project.Name == name {
			return project
		}
	}
	t.Fatalf("project %q is not listed", name)
	return Project{}
}

// Reading the view calls no check and starts no process, however often a page
// reads it, including a restart that has only the files to read.
func TestReadingTheViewChecksNothingAndStartsNothing(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.running = []state.TaskMeta{f.goblin("acme", "acme-one")}
	f.tick()
	f.snapshots.Close()
	if f.counters.get("machine") != 1 {
		t.Fatalf("premise: machine checked %d times while making the snapshots", f.counters.get("machine"))
	}
	reopened := f.open(t)
	f.counters = &counters{counts: map[string]int{}}
	reopened.CheckMachine = func(context.Context) Level { t.Error("a read checked the machine"); return Level{} }
	reopened.CheckProject = func(context.Context, string, string) Level { t.Error("a read checked a project"); return Level{} }
	reopened.ReadGoblin = func(state.TaskMeta, string, int64, Goblin) (Goblin, int64) {
		t.Error("a read read a goblin")
		return Goblin{}, 0
	}
	f.runner.respond = nil

	// Act
	var view View
	for range 200 {
		view = reopened.Read()
	}

	// Assert
	if len(view.Projects) != 2 || len(view.Goblins) != 1 || len(view.Machine.Items) != 1 {
		t.Fatalf("view = %d projects, %d goblins, %d machine items, want what the files kept", len(view.Projects), len(view.Goblins), len(view.Machine.Items))
	}
	if f.runner.total() != 0 || f.counters.total() != 0 {
		t.Fatalf("reads started %d processes and %d checks", f.runner.total(), f.counters.total())
	}
}

// Each level is checked once: two goblins of one project cause one check of the
// project, the machine is checked once, and nothing is checked again until
// something changed or a check grew old.
func TestEachLevelIsCheckedOnceAndAgainOnlyWhenItChangedOrGrewOld(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.running = []state.TaskMeta{f.goblin("acme", "acme-one"), f.goblin("acme", "acme-two"), f.goblin("globex", "globex-one")}
	steps := []struct {
		name   string
		change func()
		want   map[string]int
	}{
		{"first look", func() {}, map[string]int{"machine": 1, "project:acme": 1, "project:globex": 1}},
		{"nothing changed", func() { f.clock = f.clock.Add(time.Minute) }, map[string]int{"machine": 1, "project:acme": 1, "project:globex": 1}},
		{"one project's .mcp.json changed", func() {
			writeFile(t, filepath.Join(f.goblin("acme", "").Project, ".mcp.json"), `{"mcpServers":{"acme-docs":{"command":"acme-docs"}}}`)
		}, map[string]int{"machine": 1, "project:acme": 2, "project:globex": 1}},
		{"one project's manifest changed", func() {
			f.manifest(t, "globex", `[{"name":"sample","method":"env","env":["SAMPLE_VALUE","SAMPLE_EXTRA"]}]`)
		}, map[string]int{"machine": 1, "project:acme": 2, "project:globex": 2}},
		{"projects grew old", func() { f.clock = f.clock.Add(projectLife) }, map[string]int{"machine": 1, "project:acme": 3, "project:globex": 3}},
		{"the machine grew old", func() { f.clock = f.clock.Add(machineLife) }, map[string]int{"machine": 2, "project:acme": 4, "project:globex": 4}},
	}

	for _, step := range steps {
		// Act
		step.change()
		f.tick()

		// Assert
		for key, want := range step.want {
			if got := f.counters.get(key); got != want {
				t.Fatalf("after %s: %s checked %d times, want %d", step.name, key, got, want)
			}
		}
	}
	for _, task := range []string{"acme-one", "acme-two", "globex-one"} {
		if got := f.counters.get("goblin:" + task); got != 2 {
			t.Errorf("goblin %s read %d times across six looks, want the first look and its own project's change only", task, got)
		}
	}
}

// A refresh of one project checks only it and dates only it: the machine, the
// other project and every goblin are left as they were.
func TestRefreshOfOneProjectChecksOnlyItAndUpdatesItsCheckedAt(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.running = []state.TaskMeta{f.goblin("acme", "acme-one")}
	f.tick()
	first := f.clock
	f.clock = f.clock.Add(10 * time.Minute)

	// Act
	err := f.snapshots.Refresh(RefreshProject, "globex")
	f.snapshots.work.Wait()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := f.project(t, "globex").CheckedAt; !got.Equal(f.clock) {
		t.Errorf("globex checked at %v, want %v", got, f.clock)
	}
	if got := f.project(t, "acme").CheckedAt; !got.Equal(first) {
		t.Errorf("acme checked at %v, want it left at %v", got, first)
	}
	for key, want := range map[string]int{"machine": 1, "project:acme": 1, "project:globex": 2, "goblin:acme-one": 1} {
		if got := f.counters.get(key); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
	if f.project(t, "globex").Checking {
		t.Error("globex is still said to be checking")
	}
}

func TestRefreshOfTheMachineChecksOnlyTheMachine(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.tick()
	f.clock = f.clock.Add(time.Minute)

	// Act
	err := f.snapshots.Refresh(RefreshMachine, "ignored")
	f.snapshots.work.Wait()

	// Assert
	if got := f.snapshots.Read().Machine.CheckedAt; err != nil || !got.Equal(f.clock) || f.counters.get("machine") != 2 || f.counters.get("project:acme") != 1 {
		t.Fatalf("machine checked at %v (%v) after %d machine and %d acme checks, want only the machine again", got, err, f.counters.get("machine"), f.counters.get("project:acme"))
	}
}

// A refresh marks its level checking at once, before the check ends, and two
// asks for one project that wait behind another check are one check.
func TestRefreshMarksTheLevelCheckingAndMergesAskForTheSameProject(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.tick()
	f.counters.block, f.counters.blockOn = make(chan struct{}), "globex"
	var release sync.Once
	unblock := func() { release.Do(func() { close(f.counters.block) }) }
	t.Cleanup(unblock)

	// Act
	held := f.snapshots.Refresh(RefreshProject, "globex")
	first := f.snapshots.Refresh(RefreshProject, "acme")
	second := f.snapshots.Refresh(RefreshProject, "acme")
	isChecking := f.project(t, "acme").Checking
	unblock()
	f.snapshots.work.Wait()

	// Assert
	if held != nil || first != nil || second != nil || !isChecking {
		t.Fatalf("refresh = %v, %v, %v and checking %v, want all accepted and the project shown checking", held, first, second, isChecking)
	}
	if got := f.counters.get("project:acme"); got != 2 {
		t.Fatalf("acme checked %d times, want the first look and one more for the two asks that waited", got)
	}
	if f.project(t, "acme").Checking {
		t.Error("acme is still checking after its check ended")
	}
}

func TestRefreshRefusesWhatIsNotListed(t *testing.T) {
	cases := []struct {
		name, level, scope string
		want               error
	}{
		{"unknown level", "pc", "", ErrUnknownLevel},
		{"empty level", "", "", ErrUnknownLevel},
		{"unlisted project", RefreshProject, "initech", ErrNotListed},
		{"project with a path in its name", RefreshProject, "..", ErrNotListed},
		{"unlisted goblin", RefreshGoblin, "no-such-task", ErrNotListed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSnapshotFixture(t)

			// Act
			err := f.snapshots.Refresh(test.level, test.scope)

			// Assert
			f.snapshots.work.Wait()
			if err != test.want || f.counters.total() != 0 {
				t.Fatalf("refresh = %v after %d checks, want %v and no check", err, f.counters.total(), test.want)
			}
		})
	}
}

// A goblin that no longer runs is let go with its file, and a project nothing
// names any longer goes with its file.
func TestAGoblinThatStoppedRunningAndAProjectNothingNamesAreLetGo(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.running = []state.TaskMeta{f.goblin("initech", "initech-one")}
	f.tick()
	goblinFile := filepath.Join(f.folder, "goblins", "initech-one.json")
	projectFile := filepath.Join(f.folder, "projects", "initech.json")
	for _, path := range []string{goblinFile, projectFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("premise: %v", err)
		}
	}

	// Act
	f.running = nil
	f.tick()

	// Assert
	view := f.snapshots.Read()
	if len(view.Goblins) != 0 || len(view.Projects) != 2 {
		t.Fatalf("view = %d goblins, %d projects, want the goblin and its manifest-less project gone", len(view.Goblins), len(view.Projects))
	}
	for _, path := range []string{goblinFile, projectFile} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists (%v)", path, err)
		}
	}
}

// A snapshot file that cannot be read is left out, and its level is checked
// as one never made.
func TestAnUnreadableSnapshotFileIsCheckedAsOneNeverMade(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.tick()
	f.snapshots.Close()
	if err := os.WriteFile(filepath.Join(f.folder, "projects", "acme.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.counters = &counters{counts: map[string]int{}}
	f.snapshots = f.open(t)

	// Act
	f.tick()

	// Assert
	if f.counters.get("project:acme") != 1 || f.counters.get("project:globex") != 0 || f.counters.get("machine") != 0 {
		t.Fatalf("checks = %v, want only acme checked again", f.counters.counts)
	}
}

// What a restart reads back is what was kept, shown as not checking.
func TestSnapshotsReadBackAfterARestart(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	f.running = []state.TaskMeta{f.goblin("acme", "acme-one")}
	f.tick()
	want := f.snapshots.Read()
	f.snapshots.Close()

	// Act
	got := f.open(t).Read()

	// Assert
	if asJSON(t, got) != asJSON(t, want) {
		t.Fatalf("after a restart:\n%s\nwant:\n%s", asJSON(t, got), asJSON(t, want))
	}
}

// A view of nothing is empty lists, never null.
func TestAnEmptyViewHasEmptyListsNotNull(t *testing.T) {
	// Arrange
	e := newTestEnvironment(t)
	snapshots := NewSnapshots(e.Environment, t.TempDir())
	t.Cleanup(snapshots.Close)

	// Act
	text := asJSON(t, snapshots.Read())

	// Assert
	if text != `{"machine":{"items":[],"checked_at":"0001-01-01T00:00:00Z","checking":false},"projects":[],"goblins":[]}` {
		t.Fatalf("view = %s", text)
	}
}

// A goblin is read again when its task record changes, as a grant changes it,
// and not otherwise.
func TestAGoblinIsReadAgainWhenItsTaskRecordChanges(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	meta := f.goblin("acme", "acme-one")
	f.running = []state.TaskMeta{meta}
	f.tick()
	f.tick()

	// Act
	meta.Credentials = []string{"sample"}
	f.running = []state.TaskMeta{meta}
	f.tick()

	// Assert
	if got := f.counters.get("goblin:acme-one"); got != 2 {
		t.Fatalf("goblin read %d times, want the first look and the change only", got)
	}
}

// A goblin whose session records nothing, a Codex goblin's, is read again when
// its project's manifest or .mcp.json changes, as what it carries and is not
// given comes from them.
func TestAGoblinIsReadAgainWhenItsProjectsManifestOrMCPConfigChanges(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	meta := f.goblin("acme", "acme-one")
	meta.Harness = "codex"
	f.running = []state.TaskMeta{meta}
	f.tick()
	f.tick()

	// Act
	f.manifest(t, "acme", `[{"name":"sample","method":"env","env":["SAMPLE_VALUE"]},{"name":"extra","method":"env","env":["EXTRA_VALUE"]}]`)
	f.tick()
	afterManifest := f.counters.get("goblin:acme-one")
	writeFile(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"acme-docs":{"command":"acme-docs"}}}`)
	f.tick()
	f.tick()

	// Assert
	if afterManifest != 2 {
		t.Errorf("goblin read %d times after the manifest changed, want the first look and the change", afterManifest)
	}
	if got := f.counters.get("goblin:acme-one"); got != 3 {
		t.Errorf("goblin read %d times, want the first look and each change only", got)
	}
}

// Watch checks what is due at once and ends with its context.
func TestWatchChecksAtOnceAndEndsWithItsContext(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	// Act
	go func() {
		f.snapshots.Watch(ctx, time.Hour, func() []state.TaskMeta { return []state.TaskMeta{f.goblin("acme", "acme-one")} })
		close(done)
	}()
	deadline := time.Now().Add(time.Minute)
	for f.counters.get("machine") == 0 || f.counters.get("project:acme") == 0 || f.counters.get("goblin:acme-one") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("watch checked %v in a minute", f.counters.counts)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	// Assert
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("watch did not end with its context")
	}
}

// A goblin whose checkout's folder differs only in case from its project's
// manifest folder is listed under that one project, which is checked once,
// as Windows reads both names as one folder.
func TestAGoblinWhoseCheckoutDiffersInCaseIsListedUnderItsManifestProject(t *testing.T) {
	// Arrange
	f := newSnapshotFixture(t)
	meta := f.goblin("acme", "acme-one")
	meta.Project = filepath.Join(filepath.Dir(meta.Project), "Acme")
	f.running = []state.TaskMeta{meta}

	// Act
	f.tick()

	// Assert
	var names []string
	for _, project := range f.snapshots.Read().Projects {
		names = append(names, project.Name)
	}
	if !slices.Equal(names, []string{"acme", "globex"}) {
		t.Fatalf("projects = %v, want [acme globex]: the checkout Acme is the manifest acme", names)
	}
	if got := f.counters.get("project:acme") + f.counters.get("project:Acme"); got != 1 {
		t.Errorf("acme checked %d times, want once", got)
	}
}
