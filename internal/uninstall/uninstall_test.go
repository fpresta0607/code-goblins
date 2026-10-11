package uninstall

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeMachine is a machine of things in memory: each finder finds what is
// still in there, and each removal takes a thing out.
type fakeMachine struct {
	there   map[string]bool
	order   []string
	scripts []string
	// failing are things whose removal fails, and stuck things a removal
	// leaves there all the same.
	failing, stuck map[string]bool
	still          []string
	adminErr       error
}

func (f *fakeMachine) item(action Action, group Group, where string) Item {
	return Item{Action: action, Group: group, What: "the thing " + where, Where: where, do: func(context.Context) error {
		f.order = append(f.order, where)
		if f.failing[where] {
			return errors.New("access is denied; try again")
		}
		if !f.stuck[where] {
			delete(f.there, where)
		}
		return nil
	}}
}

func (f *fakeMachine) finder(group Group, things ...Item) finder {
	return finder{group: group, find: func(context.Context) ([]Item, error) {
		var found []Item
		for _, thing := range things {
			if f.there[thing.Where] {
				found = append(found, thing)
			}
		}
		return found, nil
	}}
}

func (f *fakeMachine) plan(finders ...finder) Plan {
	p := Plan{Home: `C:\home`, Kept: `C:\kept`, finders: finders, aside: new(string), admin: func(_ context.Context, script string) ([]string, error) {
		f.scripts = append(f.scripts, script)
		return f.still, f.adminErr
	}}
	for _, finder := range finders {
		items, _ := finder.find(context.Background())
		p.Items = append(p.Items, items...)
	}
	return p
}

func newFake(things ...string) *fakeMachine {
	f := &fakeMachine{there: map[string]bool{}, failing: map[string]bool{}, stuck: map[string]bool{}}
	for _, thing := range things {
		f.there[thing] = true
	}
	return f
}

func names(items []Item) []string {
	var out []string
	for _, item := range items {
		out = append(out, item.Where)
	}
	return out
}

func leftNames(left []Left) []string {
	var out []string
	for _, l := range left {
		out = append(out, l.Where)
	}
	return out
}

func TestRunDoesEachGroupInOrderAndReadsItBack(t *testing.T) {
	// Arrange
	f := newFake("board", "work", "credential", "home")
	plan := f.plan(
		f.finder(GroupRunning, f.item(Stop, GroupRunning, "board")),
		f.finder(GroupWork, f.item(Keep, GroupWork, "work")),
		f.finder(GroupCredentials, f.item(Remove, GroupCredentials, "credential")),
		f.finder(GroupHome, f.item(Remove, GroupHome, "home")),
	)
	var started []Group

	// Act
	outcome := plan.Run(context.Background(), func(group Group, _ []Item, _ []Left, finished bool) {
		if !finished {
			started = append(started, group)
		}
	})

	// Assert
	if want := []string{"board", "work", "credential", "home"}; !slices.Equal(f.order, want) {
		t.Errorf("did %v, want %v", f.order, want)
	}
	if want := []Group{GroupRunning, GroupWork, GroupCredentials, GroupHome}; !slices.Equal(started, want) {
		t.Errorf("groups started %v, want %v", started, want)
	}
	if len(outcome.Left) != 0 || outcome.Stopped || len(outcome.Done) != 4 {
		t.Errorf("outcome = %+v, want all four done", outcome)
	}
}

func TestRunNamesWhatItCouldNotRemoveAndWhatIsStillThere(t *testing.T) {
	// Arrange
	f := newFake("failing", "stuck", "fine")
	f.failing["failing"], f.stuck["stuck"] = true, true
	items := []Item{f.item(Remove, GroupFolders, "failing"), f.item(Remove, GroupFolders, "stuck"), f.item(Remove, GroupFolders, "fine")}
	plan := f.plan(f.finder(GroupFolders, items...))

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if got := leftNames(outcome.Left); !slices.Equal(got, []string{"failing", "stuck"}) {
		t.Fatalf("left %v, want the failing and the stuck thing", got)
	}
	for _, l := range outcome.Left {
		sentence := l.Sentence()
		if strings.ContainsAny(sentence, ";\u2014") || !strings.HasPrefix(sentence, "Left the thing ") || !strings.HasSuffix(sentence, "run goblins uninstall again to finish.") {
			t.Errorf("Sentence() = %q, want one sentence naming it and how to finish, with no semicolon or dash", sentence)
		}
	}
	if !strings.Contains(outcome.Left[1].Sentence(), "it is still there") {
		t.Errorf("a removal that left the thing there says %q, want it named as still there", outcome.Left[1].Sentence())
	}
	if names(outcome.Done)[0] != "fine" {
		t.Errorf("done %v, want the removed thing", names(outcome.Done))
	}
}

// Work that could not be kept is never removed with what holds it: the run
// stops there, before any group that removes.
func TestRunStopsBeforeAnyRemovalWhenWorkCouldNotBeKept(t *testing.T) {
	// Arrange
	f := newFake("board", "work", "worktree", "home")
	f.failing["work"] = true
	plan := f.plan(
		f.finder(GroupRunning, f.item(Stop, GroupRunning, "board")),
		f.finder(GroupWork, f.item(Keep, GroupWork, "work")),
		f.finder(GroupCheckouts, f.item(Remove, GroupCheckouts, "worktree")),
		f.finder(GroupHome, f.item(Remove, GroupHome, "home")),
	)

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if !outcome.Stopped || !f.there["worktree"] || !f.there["home"] {
		t.Errorf("stopped %v, worktree there %v, home there %v; want the run stopped with both kept", outcome.Stopped, f.there["worktree"], f.there["home"])
	}
}

// What only an administrator can remove goes in one script, so Windows asks
// once, and the script's own read back names what is still there.
func TestRunAsksForTheAdministratorOnceAndReadsBackWhatItSaid(t *testing.T) {
	// Arrange
	f := newFake()
	f.still = []string{`\Code Goblins Dev Drive`}
	admin := []Item{
		{Action: Remove, Group: GroupDevDrive, Admin: true, What: "the Dev Drive E:", Where: `C:\DevDrives\CodeGoblins.vhdx`, Script: "remove the drive"},
		{Action: Remove, Group: GroupDevDrive, Admin: true, What: "its startup task", Where: `\Code Goblins Dev Drive`, Script: "remove the task"},
	}
	plan := f.plan(finder{group: GroupDevDrive, find: func(context.Context) ([]Item, error) { return admin, nil }})

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if len(f.scripts) != 1 || !strings.Contains(f.scripts[0], "remove the drive") || !strings.Contains(f.scripts[0], "remove the task") {
		t.Fatalf("scripts = %q, want one holding both parts", f.scripts)
	}
	if got := leftNames(outcome.Left); !slices.Equal(got, []string{`\Code Goblins Dev Drive`}) {
		t.Errorf("left %v, want only what the script found still there", got)
	}
	if got := names(outcome.Done); !slices.Equal(got, []string{`C:\DevDrives\CodeGoblins.vhdx`}) {
		t.Errorf("done %v, want the drive", got)
	}
}

func TestRunLeavesEverythingTheAdministratorWasAskedForWhenThePromptIsDeclined(t *testing.T) {
	// Arrange
	f := newFake()
	f.adminErr = errors.New("Windows' administrator prompt was declined")
	admin := []Item{{Action: Remove, Group: GroupDevDrive, Admin: true, What: "the Dev Drive E:", Where: "vhd", Script: "x"}}
	plan := f.plan(finder{group: GroupDevDrive, find: func(context.Context) ([]Item, error) { return admin, nil }})

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if len(outcome.Left) != 1 || !strings.Contains(outcome.Left[0].Sentence(), "administrator prompt was declined") {
		t.Errorf("left %+v, want the drive left because the prompt was declined", outcome.Left)
	}
}

// While anything is left, what a second run needs stays: the program on
// PATH, the entry in Installed apps and the home's records.
func TestRunKeepsTheHomeWhileAnythingBeforeItIsLeft(t *testing.T) {
	// Arrange
	f := newFake("credential", "home")
	f.failing["credential"] = true
	plan := f.plan(
		f.finder(GroupCredentials, f.item(Remove, GroupCredentials, "credential")),
		f.finder(GroupHome, f.item(Remove, GroupHome, "home")),
	)

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if !f.there["home"] || slices.Contains(f.order, "home") {
		t.Errorf("the home was removed although a credential was left")
	}
	if got := leftNames(outcome.Left); !slices.Equal(got, []string{"credential", "home"}) {
		t.Errorf("left %v, want the credential, and the home named as staying", got)
	}
}

func TestLeaveIsNamedAndNeverDone(t *testing.T) {
	// Arrange
	f := newFake("claude")
	tool := f.item(Leave, GroupTools, "claude")
	plan := f.plan(f.finder(GroupTools, tool))

	// Act
	outcome := plan.Run(context.Background(), func(Group, []Item, []Left, bool) {})

	// Assert
	if len(f.order) != 0 || len(outcome.Done) != 0 {
		t.Errorf("a tool to leave was acted on: %v", f.order)
	}
	if plan.Acts() {
		t.Errorf("a plan that only leaves a tool says it acts")
	}
}
