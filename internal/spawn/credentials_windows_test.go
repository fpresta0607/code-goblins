package spawn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The stand-in project declares three services, the last of them given by
// default, and its scope holds a value for each of their names and one more
// that no service declares. Every value is a stand-in and every name is one no
// real terminal sets.
const (
	standInDatabase = "STANDIN_DATABASE_URL"
	standInPayments = "STANDIN_PAYMENTS_KEY"
	standInSource   = "STANDIN_SOURCE_TOKEN"
	standInStray    = "STANDIN_STRAY_KEY"
)

var standInValues = map[string]string{
	standInDatabase: "stand-in-database",
	standInPayments: "stand-in-payments",
	standInSource:   "stand-in-source",
	standInStray:    "stand-in-stray",
}

const standInManifest = `{"project": "primary", "services": [
  {"name": "database", "method": "env", "env": ["STANDIN_DATABASE_URL"]},
  {"name": "payments", "method": "env", "env": ["STANDIN_PAYMENTS_KEY"]},
  {"name": "source", "method": "env", "env": ["STANDIN_SOURCE_TOKEN"], "default": true}
]}`

// giveStandInCredentials stores the stand-in project's values in a store of
// the test's own and gives the fixture the real preflight, so what a terminal
// carries is decided by the code under test.
func giveStandInCredentials(t *testing.T, f *fixture) auth.Store {
	t.Helper()
	for name := range standInValues {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	t.Setenv(auth.StoreDirEnv, filepath.Join(t.TempDir(), "credentials"))
	manifestPath := auth.ManifestPath(f.dataDir, f.project)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(standInManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range standInValues {
		if err := store.Set(auth.Scoped(f.project, name), value); err != nil {
			t.Fatal(err)
		}
	}
	f.service.Auth = auth.SpawnPreflight{DataDir: f.dataDir, Runner: f.runner}
	return store
}

// briefAsking writes the fixture's brief with an Authentication section that
// holds line, such as "credentials: database", or nothing but the older
// form's prose when line is empty.
func briefAsking(t *testing.T, f *fixture, line string) {
	t.Helper()
	writeFile(t, f.brief, "Delivery contract: mode=no-mistakes\n\n## Task\n\nDo the work.\n\n## Authentication\n\nServices this task needs are declared in data\\projects\\primary\\auth.json.\n"+line+"\n\n## Delivery\n\nkind: ship\nmode: no-mistakes\n")
}

// carriedBy is the stand-in names the harness of one launch started with,
// sorted. A failure prints names and never a value.
func carriedBy(launch codexEvent) []string {
	var names []string
	for name := range standInValues {
		if value := launch.Env[name]; value != nil && *value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// holdsNoStandInValue fails when text holds any value of the stand-in store.
func holdsNoStandInValue(t *testing.T, what, text string) {
	t.Helper()
	for _, value := range standInValues {
		if strings.Contains(text, value) {
			t.Errorf("%s holds a credential value", what)
		}
	}
}

// taskRecords is everything a task's start wrote about it outside its
// terminal: its record, its status log and the instruction it was handed.
func taskRecords(t *testing.T, f *fixture, id string) string {
	t.Helper()
	var all strings.Builder
	for _, path := range []string{state.TaskMetaPath(f.stateDir, id), filepath.Join(f.stateDir, id+".status"), filepath.Join(f.stateDir, "tasktmp", id, "instruction.md")} {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		all.Write(data)
	}
	return all.String()
}

func TestASpawnGivesATaskOnlyTheServiceItsBriefNames(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	giveStandInCredentials(t, f.fixture)
	briefAsking(t, f.fixture, "credentials: database")

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := carriedBy(named(f.events(t), "env")[0]); !slices.Equal(got, []string{standInDatabase}) {
		t.Errorf("the terminal carries %v, want only %s", got, standInDatabase)
	}
	if !result.Meta.HasCredentials || !slices.Equal(result.Meta.Credentials, []string{"database"}) {
		t.Errorf("the record names %v, want database alone", result.Meta.Credentials)
	}
	for _, want := range []string{"payments", "source", "cfo auth grant task-7 <service>", standInStray} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("output = %q, want %q on the line that says what was withheld", result.Output, want)
		}
	}
	instruction := delivered(t, submittedLines(t, f, 1)[0])
	for _, want := range []string{"database", "blocked report", "never ask for a value"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q: the goblin is told what it carries and how to ask for more", instruction, want)
		}
	}
	holdsNoStandInValue(t, "the spawn's output", result.Output)
	holdsNoStandInValue(t, "the task's record, status log or instruction", taskRecords(t, f.fixture, "task-7"))
}

func TestASpawnGivesATaskThatNamesNoneNoCredential(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	giveStandInCredentials(t, f.fixture)
	briefAsking(t, f.fixture, "credentials: none")

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := carriedBy(named(f.events(t), "env")[0]); len(got) != 0 {
		t.Errorf("the terminal carries %v, want none, the default service's included", got)
	}
	if !result.Meta.HasCredentials || len(result.Meta.Credentials) != 0 {
		t.Errorf("the record names %v (recorded %v), want it to say none", result.Meta.Credentials, result.Meta.HasCredentials)
	}
}

// A brief in the form every brief had before a brief named its services does
// not break, and does not keep the whole set by saying nothing: its task
// carries the services the manifest marks default, and the spawn says which
// were withheld and how to grant one.
func TestASpawnOfABriefInTheOlderFormGivesOnlyTheDefaultServices(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	giveStandInCredentials(t, f.fixture)
	briefAsking(t, f.fixture, "")

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := carriedBy(named(f.events(t), "env")[0]); !slices.Equal(got, []string{standInSource}) {
		t.Errorf("the terminal carries %v, want only %s, which the default service declares", got, standInSource)
	}
	if !slices.Equal(result.Meta.Credentials, []string{"source"}) {
		t.Errorf("the record names %v, want the default service", result.Meta.Credentials)
	}
	withheld := regexp.MustCompile(`(?m)^auth: withheld 2 of primary's services \(database, payments\), grant one with .cfo auth grant task-7 <service>.`)
	if !withheld.MatchString(result.Output) {
		t.Errorf("output = %q, want one line naming the two withheld services and the command that grants one", result.Output)
	}
	holdsNoStandInValue(t, "the spawn's output", result.Output)
}

func TestASpawnRefusesABriefNamingAServiceTheManifestDoesNotDeclare(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	giveStandInCredentials(t, f.fixture)
	briefAsking(t, f.fixture, "credentials: database, billing")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "billing") || !strings.Contains(err.Error(), "database, payments, source") {
		t.Fatalf("spawn = %v, want it refused naming billing and the services the manifest declares", err)
	}
	if _, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !errors.Is(readErr, os.ErrNotExist) {
		t.Errorf("the refused start left its task record: %v", readErr)
	}
	if _, readErr := host.ReadRecord(f.stateDir, f.request.ID); !errors.Is(readErr, os.ErrNotExist) {
		t.Errorf("the refused start launched a terminal: %v", readErr)
	}
}

// A task's later terminals carry what its spawn gave it: the record decides,
// not the brief as it reads by then, nor the manifest's defaults.
func TestASwitchAndAResumeKeepTheServicesTheTaskWasSpawnedWith(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "turns")
	giveStandInCredentials(t, f.fixture)
	briefAsking(t, f.fixture, "credentials: database")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	awaitComposer(t, f)
	briefAsking(t, f.fixture, "credentials: payments")

	// Act
	switched, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	second, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(f.stateDir, second, nativeCloseWait); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", ResumeSession: "owned-task-7-session"}); err != nil {
		t.Fatalf("resume in place: %v", err)
	}

	// Assert
	launches := named(f.events(t), "env")
	if len(launches) != 3 {
		t.Fatalf("the harness started %d times, want the spawn, the switch and the resume", len(launches))
	}
	for index, launch := range launches {
		if got := carriedBy(launch); !slices.Equal(got, []string{standInDatabase}) {
			t.Errorf("launch %d carries %v, want only %s every time", index, got, standInDatabase)
		}
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil || !after.HasCredentials || !slices.Equal(after.Credentials, []string{"database"}) {
		t.Errorf("after both the record names %v, %v, want database alone", after.Credentials, err)
	}
	holdsNoStandInValue(t, "the switch's output", switched.Output)
}

// A task an older build spawned has a record that names no services, and its
// terminal carries everything its project's scope held. Its next terminal is
// given what a brief that names no service is given, the manifest's default
// services, and its record names them from then on. Its brief is not read: it
// was written before a brief could name a service, and its goblin can write
// to it, so a credentials line in it by now is no word of the CFO's.
func TestAResumeOfATaskAnOlderBuildSpawnedCarriesTheDefaultServices(t *testing.T) {
	cases := []struct {
		name  string
		brief string
	}{
		{"its brief is in the older form", ""},
		{"its brief has since been given a credentials line", "credentials: payments"},
		{"its brief is gone", "gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			f := newSwitchFixture(t, harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}})
			giveStandInCredentials(t, f.fixture)
			briefAsking(t, f.fixture, tc.brief)
			if tc.brief == "gone" {
				if err := os.Remove(f.brief); err != nil {
					t.Fatal(err)
				}
			}
			if f.meta.HasCredentials {
				t.Fatal("the premise is a record that names no services, as an older build wrote it")
			}
			if err := state.WriteLifecycle(f.stateDir, state.Lifecycle{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "resume-1", Action: "resume", Phase: "resuming"}); err != nil {
				t.Fatal(err)
			}

			// Act
			result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, IsResume: true, ResumeSession: "saved-session-42"})

			// Assert
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			launches := named(f.events(t), "env")
			if !result.Resumed || len(launches) != 1 {
				t.Fatalf("result = %+v with %d launches, want the task resumed once", result, len(launches))
			}
			if got := carriedBy(launches[0]); !slices.Equal(got, []string{standInSource}) {
				t.Errorf("the resumed terminal carries %v, want only %s", got, standInSource)
			}
			after, err := state.ReadTaskMeta(f.stateDir, f.meta.ID)
			if err != nil || !after.HasCredentials || !slices.Equal(after.Credentials, []string{"source"}) {
				t.Errorf("after the resume the record names %v (recorded %v), %v, want the default service", after.Credentials, after.HasCredentials, err)
			}
			for _, want := range []string{"started by a build that gave it every credential stored for primary", "carry only source", "withheld 2 of primary's services (database, payments)", "cfo auth grant task-7 <service>"} {
				if !strings.Contains(result.Output, want) {
					t.Errorf("output = %q, want %q: the resume says what became of the task and how to grant a service", result.Output, want)
				}
			}
			log, err := state.TailStatus(f.stateDir, f.meta.ID, 10)
			if err != nil || !slices.ContainsFunc(log, func(line string) bool {
				return strings.Contains(line, "credentials: task-7 was started by a build") && strings.Contains(line, "carry only source")
			}) {
				t.Errorf("status log = %q, %v, want a record of the narrowing, since an automatic resume prints to nobody", log, err)
			}
			holdsNoStandInValue(t, "the resume's output", result.Output)
			holdsNoStandInValue(t, "the task's record, status log or instruction", taskRecords(t, f.fixture, f.meta.ID))
		})
	}
}

// A terminal starts from the variables Windows gives the user's processes,
// so a name set there would reach a task that was never given its service.
// Such a name is left out of the terminal, and the user's other settings and
// the task's own services are not.
func TestAWithheldNameInTheUsersOwnEnvironmentDoesNotReachTheTerminal(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	giveStandInCredentials(t, f.fixture)
	briefAsking(t, f.fixture, "credentials: database")
	f.userEnv = append(f.userEnv, standInPayments+"=a value set for the user", standInStray+"=a value set for the user", "USERS_OWN_SETTING=kept")

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	launch := named(f.events(t), "env")[0]
	if got := carriedBy(launch); !slices.Equal(got, []string{standInDatabase}) {
		t.Errorf("the terminal carries %v, want only %s", got, standInDatabase)
	}
	if kept := launch.Env["USERS_OWN_SETTING"]; kept == nil || *kept != "kept" {
		t.Error("a setting of the user's that is no credential of the project was left out of the terminal")
	}
}

// A goblin writes its helper's brief, so that brief cannot give the helper a
// service: a helper carries what its parent carries.
func TestAHelperCarriesWhatItsParentCarriesWhateverItsBriefSays(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	project, parentWorktree := parentRepository(t)
	f.project = project
	giveStandInCredentials(t, f.fixture)
	writeParent(t, f.stateDir, state.TaskMeta{ID: "task", Project: project, Worktree: parentWorktree, Credentials: []string{"database"}, HasCredentials: true})
	f.service.Worktrees.Git = nil
	f.service.Worktrees.Commands = execx.OSRunner{}
	f.service.Commands = execx.OSRunner{}
	f.request.Project, f.request.Parent, f.request.Mode = project, "task", "local-only"
	writeFile(t, f.brief, "Do the helper's part.\n\n## Authentication\n\ncredentials: payments\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := f.service.Spawn(ctx, f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got := carriedBy(named(f.events(t), "env")[0]); !slices.Equal(got, []string{standInDatabase}) {
		t.Errorf("the helper's terminal carries %v, want only %s, as its parent's does", got, standInDatabase)
	}
	if !result.Meta.HasCredentials || !slices.Equal(result.Meta.Credentials, []string{"database"}) {
		t.Errorf("the helper's record names %v, want its parent's database", result.Meta.Credentials)
	}
}

// scriptNames is the variables a credential script sets, sorted.
func scriptNames(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, match := range regexp.MustCompile(`(?m)^\$env:([A-Za-z_][A-Za-z0-9_]*)\s*=`).FindAllStringSubmatch(string(data), -1) {
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

// recordedTask writes the record of a live task of the fixture's project that
// names services, and returns it.
func recordedTask(t *testing.T, f *fixture, id string, isRecorded bool, services ...string) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{
		ID:             id,
		Window:         "native",
		Worktree:       makeDir(t, filepath.Join(filepath.Dir(f.stateDir), "worktrees", id)),
		Project:        f.project,
		Harness:        string(harness.Codex),
		Kind:           "ship",
		Mode:           "no-mistakes",
		TaskTmp:        makeDir(t, filepath.Join(f.stateDir, "tasktmp", id)),
		Brief:          f.brief,
		Backend:        "native",
		HerdrPaneID:    id,
		Credentials:    services,
		HasCredentials: isRecorded,
	}
	if err := state.WriteTaskMeta(f.stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// A refresh rewrites every live task's credential script after the store
// changes. It used to write every name stored in the project's scope into
// each one.
func TestARefreshWritesEachTaskOnlyTheServicesItCarries(t *testing.T) {
	// Arrange
	f := newFixture(t)
	store := giveStandInCredentials(t, f)
	briefAsking(t, f, "")
	one := recordedTask(t, f, "names-one", true, "database")
	none := recordedTask(t, f, "names-none", true)
	older := recordedTask(t, f, "older-build", false)
	refresher := AuthRefresher{StateDir: f.stateDir, DataDir: f.dataDir, Store: store, Panes: stubPanes{live: map[string]bool{"names-one": true, "names-none": true, "older-build": true}}}

	// Act
	result, err := refresher.RefreshProject(context.Background(), f.project)

	// Assert
	if err != nil || len(result.Refreshed) != 3 {
		t.Fatalf("refreshed %d tasks, %v, want all three", len(result.Refreshed), err)
	}
	for _, tc := range []struct {
		meta state.TaskMeta
		want []string
	}{
		{one, []string{standInDatabase}},
		{none, nil},
		{older, []string{standInSource}},
	} {
		if got := scriptNames(t, filepath.Join(tc.meta.TaskTmp, state.AuthScriptName)); !slices.Equal(got, tc.want) {
			t.Errorf("%s's script sets %v, want %v", tc.meta.ID, got, tc.want)
		}
	}
}

func TestAGrantAddsOneServiceAndNothingElse(t *testing.T) {
	// Arrange
	f := newFixture(t)
	store := giveStandInCredentials(t, f)
	briefAsking(t, f, "")
	meta := recordedTask(t, f, "names-one", true, "database")
	bystander := recordedTask(t, f, "bystander", true, "database")
	refresher := AuthRefresher{StateDir: f.stateDir, DataDir: f.dataDir, Store: store, Panes: stubPanes{live: map[string]bool{"names-one": true}}}

	// Act
	script, carried, err := refresher.Grant(context.Background(), meta.ID, []string{"payments"})

	// Assert
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, meta.ID)
	if err != nil || !slices.Equal(after.Credentials, []string{"database", "payments"}) {
		t.Errorf("after the grant the record names %v, %v, want database and payments", after.Credentials, err)
	}
	if got := scriptNames(t, script.Path); !slices.Equal(got, []string{standInDatabase, standInPayments}) {
		t.Errorf("the task's script sets %v, want the two names its services declare", got)
	}
	if !slices.Equal(carried, []string{"database", "payments"}) || !script.Live {
		t.Errorf("the grant reports %v, live %v, want the task's services after it and its terminal told", carried, script.Live)
	}
	log, err := state.TailStatus(f.stateDir, meta.ID, 10)
	if err != nil || !slices.ContainsFunc(log, func(line string) bool {
		return strings.Contains(line, "credentials: the CFO granted payments, so the task carries database, payments")
	}) {
		t.Errorf("status log = %q, %v, want the grant recorded by service name", log, err)
	}
	holdsNoStandInValue(t, "the task's record or status log", taskRecords(t, f, meta.ID))
	untouched, err := state.ReadTaskMeta(f.stateDir, bystander.ID)
	if err != nil || !slices.Equal(untouched.Credentials, []string{"database"}) {
		t.Errorf("another task of the project now names %v, %v, want it left as it was", untouched.Credentials, err)
	}
	if _, err := os.Stat(filepath.Join(bystander.TaskTmp, state.AuthScriptName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("another task's script was written by the grant: %v", err)
	}
}

func TestAGrantToATaskAnOlderBuildSpawnedAddsToTheDefaultServices(t *testing.T) {
	// Arrange
	f := newFixture(t)
	store := giveStandInCredentials(t, f)
	briefAsking(t, f, "")
	meta := recordedTask(t, f, "older-build", false)
	refresher := AuthRefresher{StateDir: f.stateDir, DataDir: f.dataDir, Store: store, Panes: stubPanes{live: map[string]bool{}}}

	// Act
	script, _, err := refresher.Grant(context.Background(), meta.ID, []string{"database"})

	// Assert
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, meta.ID)
	if err != nil || !after.HasCredentials || !slices.Equal(after.Credentials, []string{"database", "source"}) {
		t.Errorf("after the grant the record names %v, %v, want the default service and the one granted", after.Credentials, err)
	}
	if script.Live {
		t.Error("the grant reports a terminal told, and none runs")
	}
}

func TestAGrantRefusesAServiceTheManifestDoesNotDeclare(t *testing.T) {
	// Arrange
	f := newFixture(t)
	store := giveStandInCredentials(t, f)
	meta := recordedTask(t, f, "names-one", true, "database")
	refresher := AuthRefresher{StateDir: f.stateDir, DataDir: f.dataDir, Store: store}

	// Act
	_, _, err := refresher.Grant(context.Background(), meta.ID, []string{"billing"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "billing") || !strings.Contains(err.Error(), "database, payments, source") {
		t.Fatalf("grant = %v, want it refused naming billing and the services the manifest declares", err)
	}
	after, readErr := state.ReadTaskMeta(f.stateDir, meta.ID)
	if readErr != nil || !slices.Equal(after.Credentials, []string{"database"}) {
		t.Errorf("after the refusal the record names %v, %v, want it unchanged", after.Credentials, readErr)
	}
}

// A credential script is what a refresh wrote for a task's earlier terminal.
// One written before the task's services were narrowed holds everything its
// project had stored, and a resume used to leave it in the task's folder,
// where the goblin could load from it what its new terminal no longer
// carries. A relaunch removes it and says so.
func TestARelaunchRemovesTheCredentialScriptWrittenForTheLastTerminal(t *testing.T) {
	// Arrange
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit"})
	giveStandInCredentials(t, f.fixture)
	script := filepath.Join(f.meta.TaskTmp, state.AuthScriptName)
	writeFile(t, script, "$env:STANDIN_PAYMENTS_KEY = 'stand-in-payments'\n$env:STANDIN_STRAY_KEY = 'stand-in-stray'\n")

	// Act
	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9"})

	// Assert
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if left, err := os.ReadFile(script); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the script written before the relaunch is still there, setting %v (%v)", scriptNames(t, script), err)
		holdsNoStandInValue(t, "the script left in the task's folder", string(left))
	}
	if got := carriedBy(named(f.events(t), "env")[0]); !slices.Equal(got, []string{standInSource}) {
		t.Errorf("the new terminal carries %v, want only %s", got, standInSource)
	}
	if !strings.Contains(result.Output, "auth: removed the credential script written for task-7's last terminal") {
		t.Errorf("output = %q, want the relaunch to say it removed the script", result.Output)
	}
	holdsNoStandInValue(t, "the relaunch's output", result.Output)
}
