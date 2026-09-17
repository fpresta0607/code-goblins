package runtime

import (
	"strings"
	"testing"
)

// reportFixture is the machine fixture with the containers, volumes and
// listeners that actually ran on it, so the report is exercised over the same
// evidence the attribution tests use.
func reportFixture() Inventory {
	inv := machineFixture()
	inv.SystemRoot = `C:\WINDOWS`
	inv.Containers = []Container{
		{
			Name: "supabase_edge_runtime_peakcraftsman-demo", Image: "edge-runtime:v1.74.3",
			State: "running", Stack: demoStack, WorkDir: peakLiveTree,
		},
		{
			Name: "supabase_db_peakcraftsman-demo", Image: "postgres:17.6",
			State: "running", Stack: demoStack, WorkDir: peakDemoTree,
			Ports: []string{"54422->5432/tcp"}, Volumes: []string{"supabase_db_peakcraftsman-demo"},
			MemoryLimitBytes: 2 << 30,
		},
		{
			// The log shipper nobody owned: up, failing, and producing
			// nothing anything reads.
			Name: "supabase_vector_peakcraftsman-demo", Image: "vector:0.53.0",
			State: "restarting", RestartCount: 1037, Stack: demoStack, WorkDir: peakDemoTree,
		},
		{
			Name: "gb-siqsermon-draft-flow-qdrant-1", Image: "qdrant:latest",
			State: "exited", Stack: "gb-siqsermon-draft-flow", WorkDir: sermonGone,
		},
	}
	inv.Volumes = []Volume{
		{Name: "supabase_db_peakcraftsman-demo", SizeBytes: 900 << 20},
		{Name: "gb-siqsermon-draft-flow_qdrant_data", SizeBytes: 451 << 20},
		{Name: "pd-vi_dev-qdrant-data", SizeBytes: 611 << 20},
	}
	inv.Listeners = []Listener{
		{Port: 3200, PID: 21524, Process: "node.exe", WorkDir: peakRoot},
		{Port: 3201, PID: 38896, Process: "node.exe", WorkDir: peakLiveTree},
		{Port: 3300, PID: 36032, Process: "node.exe", WorkDir: peakDeadTree},
		{Port: 54422, PID: 16248, Process: "com.docker.backend.exe", WorkDir: `C:\Users\x\Docker`},
		{Port: 5432, PID: 4268, Process: "wslrelay.exe", WorkDir: `C:\WINDOWS\system32`},
		{Port: 49664, PID: 1520, Process: "lsass.exe"},
	}
	inv.Machine = Machine{MemoryTotal: 32 << 30, MemoryAvailable: 8 << 30, WSL: 11 << 30}
	return inv
}

// The three ownership classes the brief demands be proven, checked on the
// report rather than on the attribution alone: the stacks must actually come
// out grouped this way.
func TestBuildGroupsStacksByOwner(t *testing.T) {
	report := Build(`C:\dev\code-goblins`, reportFixture())

	want := map[string]string{
		"goblin peak-inbox-connect":    "supabase_edge_runtime_peakcraftsman-demo",
		"project peakCraftsman":        "supabase_db_peakcraftsman-demo",
		"unowned siqsermon-draft-flow": "gb-siqsermon-draft-flow-qdrant-1",
	}
	got := map[string]string{}
	for _, stack := range report.Stacks {
		got[stack.Owner.Label()] = stack.Containers[0].Name
	}
	for label, container := range want {
		if got[label] != container {
			t.Errorf("stack %q holds %q, want %q (all: %v)", label, got[label], container, got)
		}
	}
	// The goblin's group must come first: it is the one nothing may touch.
	if len(report.Stacks) == 0 || report.Stacks[0].Owner.Kind != OwnerGoblin {
		t.Errorf("first stack owner = %+v, want the live goblin first", report.Stacks[0].Owner)
	}
}

func TestBuildFlagsARestartLoopAndLeavesHealthyContainersAlone(t *testing.T) {
	report := Build("", reportFixture())
	var looping []string
	for _, stack := range report.Stacks {
		for _, container := range stack.Looping() {
			looping = append(looping, container.Name)
		}
	}
	if len(looping) != 1 || looping[0] != "supabase_vector_peakcraftsman-demo" {
		t.Fatalf("looping = %v, want only the vector container", looping)
	}
}

func TestRestartLoopingCountsAPatternNotASingleRetry(t *testing.T) {
	cases := []struct {
		state string
		count int
		want  bool
	}{
		{"running", 0, false},
		{"running", 1, false},
		{"running", 2, false},
		{"running", 3, true},
		{"restarting", 0, true},
		{"exited", 0, false},
	}
	for _, testCase := range cases {
		container := Container{State: testCase.state, RestartCount: testCase.count}
		if got := container.RestartLooping(); got != testCase.want {
			t.Errorf("(%s, %d).RestartLooping() = %v, want %v", testCase.state, testCase.count, got, testCase.want)
		}
	}
}

func TestBuildReportsOnlyVolumesNothingMounts(t *testing.T) {
	report := Build("", reportFixture())
	var names []string
	for _, volume := range report.LooseVolumes {
		names = append(names, volume.Name)
	}
	if len(names) != 2 {
		t.Fatalf("loose volumes = %v, want the two nothing mounts", names)
	}
	byName := map[string]LooseVolume{}
	for _, volume := range report.LooseVolumes {
		byName[volume.Name] = volume
	}
	if _, mounted := byName["supabase_db_peakcraftsman-demo"]; mounted {
		t.Error("a mounted volume was reported as orphaned")
	}
	// The one whose name still carries a retired task must say so and point
	// at reap; the one whose prefix names nothing must claim no owner.
	retired := byName["gb-siqsermon-draft-flow_qdrant_data"]
	if retired.Owner.TaskID != "siqsermon-draft-flow" || !retired.Owner.Reap {
		t.Errorf("owner = %+v, want the retired task named with a reap pointer", retired.Owner)
	}
	if unknown := byName["pd-vi_dev-qdrant-data"]; unknown.Owner.TaskID != "" {
		t.Errorf("owner = %+v, want no task invented for an unrecognised prefix", unknown.Owner)
	}
}

// The verdict is the whole point of the servers section: a live goblin's
// server must never read as safe to stop, and a retired one must.
func TestBuildAnswersWhichServersAreSafeToStop(t *testing.T) {
	report := Build("", reportFixture())
	verdicts := map[int]Server{}
	for _, server := range report.Servers {
		verdicts[server.Port] = server
	}

	if got := verdicts[3201]; got.Stop != StopNo || got.Directory != DirLiveWorktree {
		t.Errorf("port 3201 = %+v, want no/live worktree", got)
	}
	if got := verdicts[3300]; got.Stop != StopSafe || got.Directory != DirRetiredWorktree {
		t.Errorf("port 3300 = %+v, want safe/retired worktree", got)
	}
	if !strings.Contains(verdicts[3300].StopReason(), "cfo reap") {
		t.Errorf("reason = %q, want it to hand the decision to cfo reap", verdicts[3300].StopReason())
	}
	if got := verdicts[3200]; got.Stop != StopAsk || got.Directory != DirCheckout {
		t.Errorf("port 3200 = %+v, want ask/main checkout", got)
	}
}

// A dev server is started in the directory it serves - gb-X\frontend - not at
// the root of the worktree, so an attribution that matched the worktree path
// alone told the Overlord a working goblin's server was nobody's.
func TestBuildAttributesAServerStartedInsideAWorktree(t *testing.T) {
	inv := reportFixture()
	inv.Listeners = append(inv.Listeners,
		Listener{Port: 3202, PID: 41000, Process: "node.exe", WorkDir: peakLiveTree + `\frontend`},
		Listener{Port: 3301, PID: 41001, Process: "node.exe", WorkDir: peakDeadTree + `\frontend`},
	)
	servers := map[int]Server{}
	for _, server := range Build("", inv).Servers {
		servers[server.Port] = server
	}

	live := servers[3202]
	if live.Stop != StopNo || live.Directory != DirLiveWorktree || live.Owner.TaskID != "peak-inbox-connect" {
		t.Errorf("port 3202 = %+v, want the live goblin's own server, never safe to stop", live)
	}
	stale := servers[3301]
	if stale.Stop != StopSafe || stale.Directory != DirRetiredWorktree {
		t.Errorf("port 3301 = %+v, want safe/retired worktree", stale)
	}
}

// A worktree whose id appears in neither the live records nor the archive is
// not proof the task finished - it is proof the fleet cannot see it. Calling
// that "safe" is how a working goblin's server gets stopped.
func TestBuildRefusesToCallARecordlessWorktreeSafeToStop(t *testing.T) {
	inv := reportFixture()
	recordless := `C:\dev\peakCraftsman\.worktrees\gb-peak-no-record`
	inv.Listeners = append(inv.Listeners,
		Listener{Port: 3500, PID: 42000, Process: "node.exe", WorkDir: recordless})
	inv.Present[normalize(recordless)] = true

	var server Server
	for _, candidate := range Build("", inv).Servers {
		if candidate.Port == 3500 {
			server = candidate
		}
	}
	if server.Stop == StopSafe {
		t.Fatalf("port 3500 = %+v, want anything but safe for a task the fleet cannot see", server)
	}
	if server.Stop != StopUnknown || server.Directory != DirRecordlessWorktree {
		t.Errorf("port 3500 = %+v, want unknown/no task record", server)
	}
	if !strings.Contains(server.StopReason(), "task record") {
		t.Errorf("reason = %q, want it to say the task record could not be read", server.StopReason())
	}
}

// Every listener left out is accounted for by port. A reader who cannot find
// the port they came for must see that it was folded and why, not conclude
// that nothing is listening on it.
func TestBuildFoldsNoiseButAccountsForEveryPort(t *testing.T) {
	inv := reportFixture()
	report := Build("", inv)

	listed := map[int]bool{}
	for _, server := range report.Servers {
		listed[server.Port] = true
	}
	for _, port := range []int{54422, 5432, 49664} {
		if listed[port] {
			t.Errorf("port %d was listed as a dev server", port)
		}
	}
	for _, port := range []int{3200, 3201, 3300} {
		if !listed[port] {
			t.Errorf("port %d was folded, want it listed", port)
		}
	}

	counted := map[int]bool{}
	for _, folded := range report.Folded {
		for _, port := range folded.Ports {
			counted[port] = true
		}
	}
	if len(listed)+len(counted) != len(inv.Listeners) {
		t.Errorf("listed %d + folded %d, want all %d listeners accounted for", len(listed), len(counted), len(inv.Listeners))
	}
	if !counted[54422] || !counted[5432] || !counted[49664] {
		t.Errorf("folded = %+v, want every skipped port named", report.Folded)
	}
}

// A stack whose containers declare no limit has no ceiling, and the report
// must not imply one by printing a total that only covers the containers that
// happened to declare something.
func TestBuildCountsUndeclaredMemoryLimits(t *testing.T) {
	report := Build("", reportFixture())
	var demo StackLimit
	for _, limit := range report.Limits {
		if limit.Stack == demoStack && limit.Owner == "project peakCraftsman" {
			demo = limit
		}
	}
	if demo.Bytes != 2<<30 {
		t.Errorf("declared limit = %d, want the one container that declared 2 GiB", demo.Bytes)
	}
	if demo.Undeclared != 1 {
		t.Errorf("undeclared = %d, want the restarting container counted as having no ceiling", demo.Undeclared)
	}
	// An exited stack is not consuming anything and must not appear.
	for _, limit := range report.Limits {
		if limit.Stack == "gb-siqsermon-draft-flow" {
			t.Error("an exited stack was given a running memory limit")
		}
	}
}

func TestBytesRendersEachScale(t *testing.T) {
	cases := []struct {
		value int64
		want  string
	}{
		{0, "0 B"},
		{783, "783 B"},
		{2048, "2.0 KB"},
		{5 << 20, "5.0 MB"},
		{2 << 30, "2.0 GB"},
		{3 << 40, "3.0 TB"},
	}
	for _, testCase := range cases {
		if got := Bytes(testCase.value); got != testCase.want {
			t.Errorf("Bytes(%d) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
}

// A missing source reads as zero, and dividing by it must produce a dash
// rather than a confident percentage of nothing.
func TestPercentGuardsAnUnreadSource(t *testing.T) {
	if got := Percent(5, 0); got != "-" {
		t.Errorf("Percent(5, 0) = %q, want a dash", got)
	}
	if got := Percent(8, 32); got != "25%" {
		t.Errorf("Percent(8, 32) = %q, want 25%%", got)
	}
}

// A volume outlives every container that touched it, so its own name is its
// only evidence. Borrowing a sibling container's working directory let
// whichever service sorted first alphabetically decide the owner, and the
// live-worktree rule then handed a live goblin a volume it never created.
func TestBuildNeverAttributesALooseVolumeToAGoblinFromASiblingContainer(t *testing.T) {
	inv := reportFixture()
	// The goblin's container sorts first in this stack, which is exactly the
	// ordering that used to decide the owner.
	inv.Containers = []Container{
		{Name: "aaa_goblin_service", State: "running", Stack: demoStack, WorkDir: peakLiveTree},
		{Name: "zzz_project_service", State: "running", Stack: demoStack, WorkDir: peakDemoTree},
	}
	inv.Volumes = []Volume{{Name: demoStack + "_db_data", SizeBytes: 1 << 20}}

	report := Build("", inv)
	if len(report.LooseVolumes) != 1 {
		t.Fatalf("loose volumes = %+v, want the one nothing mounts", report.LooseVolumes)
	}
	owner := report.LooseVolumes[0].Owner
	if owner.Kind == OwnerGoblin {
		t.Fatalf("owner = %+v, want the volume not handed to a live goblin", owner)
	}
	// The project's committed claim on the stack name is what owns it.
	if owner.Kind != OwnerProject || owner.Project != "peakCraftsman" {
		t.Errorf("owner = %+v, want the project that declares the stack name", owner)
	}
	if strings.Contains(owner.Evidence, peakLiveTree) {
		t.Errorf("evidence = %q, want no borrowed working directory in it", owner.Evidence)
	}
}

// compose takes a stack's name from the directory it was brought up in, so a
// stack named after a checkout still names its project once every container
// is gone - without borrowing any directory.
func TestBuildNamesAVolumesProjectFromItsStackNameAlone(t *testing.T) {
	inv := reportFixture()
	inv.Containers = nil
	inv.Volumes = []Volume{{Name: "supabase_edge_runtime_peakCraftsman", SizeBytes: 1 << 20}}

	report := Build("", inv)
	owner := report.LooseVolumes[0].Owner
	if owner.Kind != OwnerProject || owner.Project != "peakCraftsman" {
		t.Errorf("owner = %+v, want the checkout its stack name matches", owner)
	}
}
