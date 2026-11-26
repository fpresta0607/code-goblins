package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/goblinname"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// helperOfferBrief is a helper's brief written as the helper offer tells a
// goblin to: its task and an Authentication section that carries nothing.
const helperOfferBrief = "## Task\nWrite the migration for the accounts table.\n## Authentication\ncredentials: none\nmcp: none\n"

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// parentRepository is a project with one commit and a parent goblin's
// worktree beside it on branch feat/x, one commit ahead of the project, and
// returns both.
func parentRepository(t *testing.T) (project, parentWorktree string) {
	t.Helper()
	root := makeDir(t, t.TempDir())
	project = filepath.Join(root, "app")
	gitIn(t, root, "init", "-q", "--initial-branch=main", project)
	gitIn(t, project, "config", "user.email", "t@t")
	gitIn(t, project, "config", "user.name", "t")
	writeFile(t, filepath.Join(project, "README.md"), "seed\n")
	gitIn(t, project, "add", ".")
	gitIn(t, project, "commit", "-q", "-m", "seed")
	parentWorktree = filepath.Join(root, "worktrees", "app", "g1")
	gitIn(t, project, "worktree", "add", "-q", "-b", "feat/x", parentWorktree)
	writeFile(t, filepath.Join(parentWorktree, "parent.txt"), "the parent's work\n")
	gitIn(t, parentWorktree, "add", ".")
	gitIn(t, parentWorktree, "commit", "-q", "-m", "parent work")
	return project, parentWorktree
}

func writeParent(t *testing.T, stateDir string, meta state.TaskMeta) {
	t.Helper()
	meta.Window, meta.Harness, meta.Kind, meta.Backend = "native", "claude", "ship", "native"
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
}

func TestHelperStartCutsTheHelpersBranchFromItsParentsBranchAndLastCommit(t *testing.T) {
	// Arrange
	project, parentWorktree := parentRepository(t)
	stateDir := t.TempDir()
	writeParent(t, stateDir, state.TaskMeta{ID: "g1", Project: project, Worktree: parentWorktree})
	service := Service{StateDir: stateDir, Commands: execx.OSRunner{}}

	// Act
	base, err := service.helperStart(context.Background(), Request{ID: "g1-h1", Parent: "g1"}, project)

	// Assert
	if err != nil {
		t.Fatalf("helperStart: %v", err)
	}
	if base.branch != "feat/x-h1" || base.parentBranch != "feat/x" {
		t.Errorf("branch = %q off %q, want feat/x-h1 off feat/x", base.branch, base.parentBranch)
	}
	if want := gitIn(t, parentWorktree, "rev-parse", "HEAD"); base.head != want {
		t.Errorf("head = %q, want the parent's last commit %q", base.head, want)
	}
}

func TestHelperStartRefusesWhatCannotHaveAHelper(t *testing.T) {
	project, parentWorktree := parentRepository(t)
	detached := filepath.Join(filepath.Dir(parentWorktree), "g2")
	gitIn(t, project, "worktree", "add", "-q", "--detach", detached)
	other := makeDir(t, t.TempDir())
	cases := []struct {
		name    string
		request Request
		project string
		want    string
	}{
		{"a helper of a helper", Request{ID: "g1-h1-h1", Parent: "g1-h1"}, project, "a helper cannot start helpers"},
		{"a second helper", Request{ID: "g1-h2", Parent: "g1"}, project, "already has a helper, g1-h1"},
		{"a parent on no branch", Request{ID: "g2-h1", Parent: "g2"}, project, "on no branch"},
		{"another project", Request{ID: "g3-h1", Parent: "g3"}, other, "its parent's project"},
		{"a name that is not the parent's", Request{ID: "x-h1", Parent: "g3"}, project, "g3-<name>"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			writeParent(t, stateDir, state.TaskMeta{ID: "g1", Project: project, Worktree: parentWorktree})
			writeParent(t, stateDir, state.TaskMeta{ID: "g1-h1", Project: project, Worktree: parentWorktree + "-h1", Parent: "g1"})
			writeParent(t, stateDir, state.TaskMeta{ID: "g2", Project: project, Worktree: detached})
			writeParent(t, stateDir, state.TaskMeta{ID: "g3", Project: project, Worktree: parentWorktree})
			service := Service{StateDir: stateDir, Commands: execx.OSRunner{}}

			// Act
			_, err := service.helperStart(context.Background(), test.request, test.project)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("helperStart = %v, want a refusal naming %q", err, test.want)
			}
		})
	}
}

func TestSpawnRefusesAHelperOutsideLocalOnlyBeforeAnyMutation(t *testing.T) {
	for _, mode := range []string{"direct-PR", "no-mistakes", ""} {
		t.Run("mode "+mode, func(t *testing.T) {
			// Arrange
			f := newFixture(t)
			f.request.Parent, f.request.ID, f.request.Mode = "g1", "g1-h1", mode

			// Act
			_, err := f.service.Spawn(context.Background(), f.request)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "local-only") {
				t.Errorf("Spawn = %v, want a helper refused outside local-only", err)
			}
			if slices.Contains(f.events, "worktree-acquire") {
				t.Errorf("events = %v, want nothing acquired", f.events)
			}
		})
	}
}

func TestSpawnRefusesASecondHelperUnderTheSpawnLockBeforeAnyMutation(t *testing.T) {
	// Arrange
	f := newFixture(t)
	writeParent(t, f.stateDir, state.TaskMeta{ID: "g1", Project: f.project, Worktree: f.worktree})
	writeParent(t, f.stateDir, state.TaskMeta{ID: "g1-h1", Project: f.project, Worktree: f.worktree + "-h1", Parent: "g1"})
	f.request.Parent, f.request.ID, f.request.Mode = "g1", "g1-h2", "local-only"

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "already has a helper") {
		t.Errorf("Spawn = %v, want the second helper refused", err)
	}
	if slices.Contains(f.events, "worktree-acquire") {
		t.Errorf("events = %v, want nothing acquired", f.events)
	}
	if _, statErr := os.Stat(state.TaskMetaPath(f.stateDir, "g1-h2")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the refused helper's record: stat = %v, want none", statErr)
	}
}

// A goblin's helpers are named after it with their numbers, as its baby
// goblins are, Grub Jr. for g1-h1 and Grub VII for g1-h7, and keep one title
// for their role round after round, though another goblin holds it. Each
// took a name and a title of its own before, so one builder read as a new
// goblin every round (#2959). A helper whose id has no number, a top-level
// goblin started after them and a helper of a goblin with no name yet still
// take a name from the pool, and only their pairs join the recent ones.
func TestSpawnNamesAHelperAfterItsParentWithItsNumberAndKeepsOneTitleForItsRole(t *testing.T) {
	// Arrange: goblin g1, called Grub, at work on a branch; each start's
	// build is refused, so nothing starts and its record is retired.
	f := newFixture(t)
	project, parentWorktree := parentRepository(t)
	gitIn(t, project, "remote", "add", "origin", project)
	writeParent(t, f.stateDir, state.TaskMeta{ID: "g1", Project: project, Worktree: parentWorktree, GoblinName: "Grub", GoblinTitle: "Review Runner"})
	writeParent(t, f.stateDir, state.TaskMeta{ID: "o1", Project: project, Worktree: parentWorktree, GoblinName: "Olaf", GoblinTitle: "Builder Bender"})
	unnamedWorktree := filepath.Join(filepath.Dir(parentWorktree), "g3")
	gitIn(t, project, "worktree", "add", "-q", "-b", "feat/y", unnamedWorktree)
	writeParent(t, f.stateDir, state.TaskMeta{ID: "g3", Project: project, Worktree: unnamedWorktree})
	f.service.Commands = execx.OSRunner{}
	f.service.Worktrees.Git, f.service.Worktrees.Commands = nil, execx.OSRunner{}
	f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: errors.New("nothing starts in this test")}
	helperBrief := filepath.Join(t.TempDir(), "helper.md")
	writeFile(t, helperBrief, helperOfferBrief)
	start := func(id, parent, title string) state.TaskMeta {
		t.Helper()
		request := f.request
		request.ID, request.Parent, request.Project, request.Title = id, parent, project, title
		if parent != "" {
			request.Mode, request.BriefPath = "local-only", helperBrief
		}
		result, err := f.service.Spawn(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), "nothing starts in this test") {
			t.Fatalf("Spawn %s = %v, want the build refusal", id, err)
		}
		return result.Meta
	}

	// Act: its first helper builds round 1, its seventh round 7 and a helper
	// whose id has no number reviews them, then a goblin of its own builds
	// another page, and goblin g3, with no name yet, starts a helper.
	first := start("g1-h1", "g1", "Round 1 builder")
	seventh := start("g1-h7", "g1", "Round 7 builder")
	unnumbered := start("g1-review", "g1", "Review the rounds")
	topLevel := start("g2", "", "Round 8 builder")
	unnamedParents := start("g3-h1", "g3", "Round 9 builder")

	// Assert
	if first.GoblinName != "Grub Jr." || seventh.GoblinName != "Grub VII" {
		t.Errorf("helpers named %q and %q, want Grub Jr. and Grub VII", first.GoblinName, seventh.GoblinName)
	}
	if first.GoblinTitle != "Builder Bender" || seventh.GoblinTitle != first.GoblinTitle {
		t.Errorf("helpers titled %q and %q, want Builder Bender for both", first.GoblinTitle, seventh.GoblinTitle)
	}
	var pooledPairs []goblinname.Pair
	for _, pooled := range []state.TaskMeta{unnumbered, topLevel, unnamedParents} {
		if name := pooled.GoblinName; name == "" || strings.Contains(name, " ") {
			t.Errorf("%s named %q, want a first name of the pool's", pooled.ID, name)
		}
		pooledPairs = append(pooledPairs, goblinname.Pair{Name: pooled.GoblinName, Title: pooled.GoblinTitle})
	}
	if topLevel.GoblinTitle == "Builder Bender" {
		t.Errorf("a top-level goblin titled %q, want one clear of the title a live goblin holds", topLevel.GoblinTitle)
	}
	data, err := os.ReadFile(filepath.Join(f.stateDir, ".goblin-names.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recent []goblinname.Pair
	if err := json.Unmarshal(data, &recent); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recent, pooledPairs) {
		t.Errorf("recent pairs = %v, want those of the goblins named from the pool, %v, and no numbered helper's", recent, pooledPairs)
	}
}

func TestNotifyInstructionTellsAHelperToReportToItsParentAndNeverPush(t *testing.T) {
	helper := notifyInstruction(state.TaskMeta{ID: "g1-h1", Kind: "ship", Parent: "g1"})
	goblin := notifyInstruction(state.TaskMeta{ID: "g1", Kind: "ship"})

	for _, want := range []string{"helper goblin of g1", "notify g1-h1 --done", "never push", "g1 merges your branch", "cannot start helpers"} {
		if !strings.Contains(helper, want) {
			t.Errorf("helper instruction lacks %q:\n%s", want, helper)
		}
	}
	for _, unwanted := range []string{"--pr <url>", "helper start"} {
		if strings.Contains(helper, unwanted) {
			t.Errorf("helper instruction offers %q:\n%s", unwanted, helper)
		}
	}
	for _, want := range []string{"helper start g1 --brief", "helper merge g1", "--waiting-on <helper-id>", "send <helper-id>", "--pr <url>"} {
		if !strings.Contains(goblin, want) {
			t.Errorf("goblin instruction lacks %q:\n%s", want, goblin)
		}
	}
	if scout := notifyInstruction(state.TaskMeta{ID: "s1", Kind: "scout"}); strings.Contains(scout, "helper start") {
		t.Errorf("a scout is offered helpers:\n%s", scout)
	}
}

func TestHelperRefusalRecommendsExplicitAuthenticationChoices(t *testing.T) {
	for _, test := range []struct {
		name, key, subject string
		carried, named     []string
		isRefused          bool
	}{
		{"wider credentials", "credentials", "service", []string{"source"}, []string{"source", "database"}, true},
		{"wider mcp", "mcp", "MCP server", []string{"docs"}, []string{"docs", "database"}, true},
		{"no parent credentials", "credentials", "service", nil, []string{"database"}, true},
		{"no parent mcp", "mcp", "MCP server", nil, []string{"database"}, true},
		{"explicit subset", "credentials", "service", []string{"source", "database"}, []string{"source"}, false},
		{"explicit none", "mcp", "MCP server", []string{"docs"}, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			allowed := namesOr(test.carried, "none")

			// Act
			refusal := helperRefusal("parent", test.key, test.subject, test.carried, test.named)

			// Assert
			if !test.isRefused {
				if refusal != "" {
					t.Fatalf("explicit allowed choice refused: %s", refusal)
				}
				return
			}
			for _, want := range []string{"never widens it", "its " + test.key + " line names some of " + allowed, test.key + ": none", "Both credentials and mcp lines must be explicit"} {
				if !strings.Contains(refusal, want) {
					t.Errorf("refusal = %q, want %q", refusal, want)
				}
			}
			if strings.Contains(refusal, "leaves out") || strings.Contains(refusal, "inherit") {
				t.Errorf("refusal recommends implicit inheritance: %s", refusal)
			}
		})
	}
}

// A helper carries only what its own brief names, which narrows its parent's
// grants: a line its brief leaves out gives it nothing, never its parent's
// services or servers, and never the manifest's defaults.
func TestHelperNeedCarriesOnlyWhatItsBriefNames(t *testing.T) {
	parent := auth.Need{Services: []string{"database", "payments"}, MCPServers: []string{"docs"}}
	manifest := auth.Manifest{Services: []auth.Service{{Name: "database", Default: true}, {Name: "payments"}}}
	for _, test := range []struct {
		name                      string
		brief                     auth.Need
		wantServices, wantServers []string
	}{
		{name: "lines left out", brief: auth.Need{IsUnstated: true, IsMCPUnstated: true}},
		{name: "none", brief: auth.Need{}},
		{name: "a subset", brief: auth.Need{Services: []string{"payments"}, MCPServers: []string{"docs"}}, wantServices: []string{"payments"}, wantServers: []string{"docs"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			need, err := helperNeed("parent", parent, test.brief)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if carried := manifest.Grant(need).Services; !slices.Equal(carried, test.wantServices) || !slices.Equal(need.MCPServers, test.wantServers) {
				t.Fatalf("helper carries services %v and servers %v, want %v and %v", carried, need.MCPServers, test.wantServices, test.wantServers)
			}
		})
	}
}
