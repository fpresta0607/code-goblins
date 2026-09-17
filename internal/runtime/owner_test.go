package runtime

import (
	"strings"
	"testing"
)

// The fixture below is this machine's real shape on 2026-09-17, which is the
// shape every ownership mistake the brief names actually happened in: one
// project whose demo stack runs from a worktree its task no longer holds, one
// live goblin that restarted a single service of that same stack from its own
// worktree, and two stacks whose worktrees are gone.
const (
	peakRoot     = `C:\dev\peakCraftsman`
	peakDemoTree = `C:\dev\peakCraftsman\.worktrees\gb-peak-demo-local`
	peakLiveTree = `C:\dev\peakCraftsman\.worktrees\gb-peak-inbox-connect`
	peakDeadTree = `C:\dev\peakCraftsman\.worktrees\gb-peak-compute-to-supabase`
	sermonRoot   = `C:\dev\code-goblins\projects\siqsermon`
	sermonGone   = `C:\dev\code-goblins\projects\siqsermon\.worktrees\gb-siqsermon-draft-flow`
	demoStack    = "peakcraftsman-demo"
	configToml   = `C:\dev\peakCraftsman\supabase\config.toml`
)

func machineFixture() Inventory {
	return Inventory{
		Tasks: []Task{
			{ID: "peak-inbox-connect", Project: peakRoot, Worktree: peakLiveTree},
		},
		Retired: map[string]bool{
			"peak-demo-local":          true,
			"peak-compute-to-supabase": true,
			"siqsermon-draft-flow":     true,
		},
		Checkouts: []Checkout{
			{Name: "peakCraftsman", Path: peakRoot},
			{Name: "siqsermon", Path: sermonRoot},
		},
		Projects: []Project{{
			Name:   "peakCraftsman",
			Path:   peakRoot,
			Stacks: []StackName{{Name: demoStack, Source: configToml}},
		}},
		// The demo worktree and the retired compute worktree are still on
		// disk; the draft-flow one has been returned.
		Present: map[string]bool{
			normalize(peakDemoTree):     true,
			normalize(peakDeadTree):     true,
			normalize(sermonGone):       false,
			normalize(`C:\dev\scratch`): true,
		},
	}
}

func TestAttributionOfSeparatesTheDemoStackFromAGoblinsBenchStack(t *testing.T) {
	attribution := NewAttribution(machineFixture())

	cases := []struct {
		name     string
		workDir  string
		stack    string
		want     OwnerKind
		task     string
		project  string
		reap     bool
		evidence string
	}{
		{
			// The single service a live goblin restarted from its own
			// worktree. It carries the demo stack's name, so only the
			// working directory tells it apart - and getting this wrong
			// means killing a working goblin.
			name:     "live goblin inside the demo stack's name",
			workDir:  peakLiveTree,
			stack:    demoStack,
			want:     OwnerGoblin,
			task:     "peak-inbox-connect",
			project:  "peakCraftsman",
			evidence: "live worktree of task peak-inbox-connect",
		},
		{
			// The demo stack itself. Its worktree's task was retired days
			// ago, so every directory-only rule calls it abandoned; the
			// project's committed config.toml is what says otherwise.
			name:     "demo stack whose worktree task is retired",
			workDir:  peakDemoTree,
			stack:    demoStack,
			want:     OwnerProject,
			project:  "peakCraftsman",
			evidence: configToml,
		},
		{
			name:     "stack brought up from a project's main checkout",
			workDir:  peakRoot,
			stack:    "",
			want:     OwnerProject,
			project:  "peakCraftsman",
			evidence: "main checkout of peakCraftsman",
		},
		{
			// A retired task's worktree that is still on disk, running a
			// stack no project declares.
			name:     "leftover in a retired worktree still on disk",
			workDir:  peakDeadTree,
			stack:    "peakCraftsman",
			want:     OwnerUnowned,
			task:     "peak-compute-to-supabase",
			project:  "peakCraftsman",
			reap:     true,
			evidence: "has been retired",
		},
		{
			name:     "leftover whose worktree has been returned",
			workDir:  sermonGone,
			stack:    "gb-siqsermon-draft-flow",
			want:     OwnerUnowned,
			task:     "siqsermon-draft-flow",
			reap:     true,
			evidence: "has been retired",
		},
		{
			// A dev server is started in the directory it serves, which is
			// usually not the root of the worktree. Matching the worktree path
			// alone reported a working goblin's server as nobody's.
			name:     "live goblin's server in a subdirectory of its worktree",
			workDir:  peakLiveTree + `\frontend`,
			stack:    "",
			want:     OwnerGoblin,
			task:     "peak-inbox-connect",
			project:  "peakCraftsman",
			evidence: "live worktree of task peak-inbox-connect",
		},
		{
			name:     "leftover in a subdirectory of a retired worktree",
			workDir:  peakDeadTree + `\frontend`,
			stack:    "",
			want:     OwnerUnowned,
			task:     "peak-compute-to-supabase",
			project:  "peakCraftsman",
			reap:     true,
			evidence: "has been retired",
		},
		{
			name:     "stack brought up in a subdirectory of a main checkout",
			workDir:  peakRoot + `\apps\web`,
			stack:    "",
			want:     OwnerProject,
			project:  "peakCraftsman",
			evidence: "main checkout of peakCraftsman",
		},
		{
			name:     "directory nothing in the fleet claims",
			workDir:  `C:\dev\scratch`,
			stack:    "scratch",
			want:     OwnerOverlord,
			evidence: "no project manifest and no task record claims",
		},
		{
			// A volume outlives every container that touched it, so its
			// name prefix is the only owner mark left.
			name:     "volume prefix naming a retired task",
			workDir:  "",
			stack:    "gb-siqsermon-draft-flow",
			want:     OwnerUnowned,
			task:     "siqsermon-draft-flow",
			reap:     true,
			evidence: "its name carries task siqsermon-draft-flow",
		},
		{
			name:     "volume prefix naming nothing the fleet knows",
			workDir:  "",
			stack:    "pd-vi",
			want:     OwnerUnowned,
			evidence: `carries the stack "pd-vi"`,
		},
		{
			name:     "no directory and no stack name at all",
			workDir:  "",
			stack:    "",
			want:     OwnerUnowned,
			evidence: "neither a working directory nor a stack name",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			owner := attribution.Of(testCase.workDir, testCase.stack)
			if owner.Kind != testCase.want {
				t.Errorf("kind = %q, want %q (evidence %q)", owner.Kind, testCase.want, owner.Evidence)
			}
			if owner.TaskID != testCase.task {
				t.Errorf("task = %q, want %q", owner.TaskID, testCase.task)
			}
			if testCase.project != "" && owner.Project != testCase.project {
				t.Errorf("project = %q, want %q", owner.Project, testCase.project)
			}
			if owner.Reap != testCase.reap {
				t.Errorf("reap = %v, want %v", owner.Reap, testCase.reap)
			}
			if !strings.Contains(owner.Evidence, testCase.evidence) {
				t.Errorf("evidence = %q, want it to contain %q", owner.Evidence, testCase.evidence)
			}
		})
	}
}

// A path written with forward slashes, a trailing separator or a different
// case is the same directory. Docker labels, task metadata and the process
// parameter block each spell them differently on this machine, so an
// attribution that matched literally would report a live goblin as a leftover.
func TestAttributionOfMatchesPathsWrittenAnyWay(t *testing.T) {
	attribution := NewAttribution(machineFixture())
	for _, spelling := range []string{
		peakLiveTree,
		strings.ToLower(peakLiveTree),
		strings.ReplaceAll(peakLiveTree, `\`, "/"),
		peakLiveTree + `\`,
	} {
		if owner := attribution.Of(spelling, ""); owner.Kind != OwnerGoblin {
			t.Errorf("Of(%q) = %q, want goblin", spelling, owner.Kind)
		}
	}
}

// A directory that was never checked proves nothing. Only a directory checked
// and found missing is evidence of a leftover, and conflating the two would
// report every unexamined path as abandoned.
func TestAttributionOfDoesNotCallAnUncheckedDirectoryAbandoned(t *testing.T) {
	inv := machineFixture()
	inv.Present = map[string]bool{}
	owner := NewAttribution(inv).Of(`C:\dev\somewhere-unchecked`, "")
	if owner.Kind != OwnerUnowned || owner.Reap {
		t.Fatalf("owner = %+v, want unowned without a reap pointer", owner)
	}
	if !strings.Contains(owner.Evidence, "was not checked on disk") {
		t.Errorf("evidence = %q, want it to say the directory was never checked", owner.Evidence)
	}
}

// A stack name that merely looks like a task id is not one. Inventing a task
// for it would put a `cfo reap` pointer on something reap has never heard of.
func TestAttributionOfIgnoresAStackNameWithNoTaskRecord(t *testing.T) {
	owner := NewAttribution(machineFixture()).Of("", "gb-never-existed")
	if owner.TaskID != "" || owner.Reap {
		t.Errorf("owner = %+v, want no task and no reap pointer", owner)
	}
}

func TestWorktreeTaskIDOnlyAcceptsFleetWorktrees(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{peakLiveTree, "peak-inbox-connect"},
		{peakLiveTree + `\`, "peak-inbox-connect"},
		{peakLiveTree + `\frontend\src`, "peak-inbox-connect"},
		{`C:/dev/peakCraftsman/.worktrees/gb-peak-inbox-connect`, "peak-inbox-connect"},
		{peakRoot, ""},
		{`C:\dev\peakCraftsman\.worktrees\notagoblin`, ""},
		{`C:\dev\peakCraftsman\.worktrees\notagoblin\frontend`, ""},
		{`C:\dev\elsewhere\gb-peak-inbox-connect`, ""},
	}
	for _, testCase := range cases {
		got, ok := worktreeTaskID(testCase.path)
		if !ok {
			got = ""
		}
		if got != testCase.want {
			t.Errorf("worktreeTaskID(%q) = %q, want %q", testCase.path, got, testCase.want)
		}
	}
}
