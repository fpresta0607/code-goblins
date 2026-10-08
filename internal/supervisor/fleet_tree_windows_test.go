package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// treeGoblin records a native Claude goblin whose harness is this test
// process, with a conversation in which it started a sub-agent; without a
// host record its terminal has ended.
func treeGoblin(t *testing.T, stateDir, userHome, id string, isRunning bool) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{ID: id, Project: t.TempDir(), Worktree: filepath.Join(t.TempDir(), id), Harness: "claude", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: "s" + strconv.FormatInt(time.Now().Add(-time.Hour).UnixNano(), 10)}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	if !isRunning {
		return meta
	}
	processes, err := fleettree.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var self fleettree.Process
	for _, process := range processes {
		if process.PID == os.Getpid() {
			self = process
		}
	}
	record, err := json.Marshal(host.Record{ID: id, Pipe: `\\.\pipe\code-goblins-host-tree-` + id, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: self.Started, Started: self.Started})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "hosts", id+".json"), string(record))
	session := id + "-conversation"
	writeFile(t, filepath.Join(userHome, ".claude", "sessions", strconv.Itoa(os.Getpid())+".json"), `{"pid":`+strconv.Itoa(os.Getpid())+`,"sessionId":"`+session+`","procStart":"`+strconv.FormatInt(self.Created, 10)+`"}`)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	writeFile(t, filepath.Join(userHome, ".claude", "projects", "C--work-"+id, session+".jsonl"), strings.Join([]string{
		`{"type":"assistant","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_A","name":"Agent","input":{"description":"Map the monitor","subagent_type":"Explore"}}]}}`,
		`{"type":"user","timestamp":"` + stamp + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_A","content":"launched"}]},"toolUseResult":{"status":"async_launched","agentId":"a1"}}`,
	}, "\n")+"\n")
	return meta
}

// The board serves each live goblin's tree on its card, and none for a
// goblin that is paused or whose terminal has ended, which run nothing.
func TestSnapshotCarriesEachLiveGoblinsTree(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	// One test process stands for both harnesses, so the running goblin's
	// conversation is the one its process record names last.
	paused := treeGoblin(t, h.State, userHome, "paused", true)
	running := treeGoblin(t, h.State, userHome, "running", true)
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: paused.ID, Generation: paused.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
		t.Fatal(err)
	}
	treeGoblin(t, h.State, userHome, "ended", false)
	service := &Service{Store: store, Instance: "tree", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}

	// Act
	service.readTrees(context.Background())
	snapshot, err := service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	trees := map[string]*fleettree.Tree{}
	for _, task := range snapshot.Tasks {
		trees[task.ID] = task.Tree
	}
	tree := trees[running.ID]
	if tree == nil || tree.Generation != running.SpawnGen || len(tree.Children) != 1 || tree.Children[0].ID != "subagent:toolu_A" || tree.Children[0].Label != "Map the monitor" || tree.Memory == 0 {
		t.Fatalf("running goblin's tree = %+v, want its sub-agent and its memory", tree)
	}
	if trees[paused.ID] != nil || trees["ended"] != nil {
		t.Errorf("paused tree = %+v, ended tree = %+v; want none for goblins that run nothing", trees[paused.ID], trees["ended"])
	}
	data, err := json.Marshal(snapshot)
	if err != nil || !strings.Contains(string(data), `"tree":{"task_id":"running"`) || !strings.Contains(string(data), `"source_updated_at"`) || !strings.Contains(string(data), `"fetched_at"`) {
		t.Errorf("the snapshot's JSON carries no tree with its freshness: %s", data)
	}
}

// A helper hangs under its parent in the family tree, where its own report
// and lifecycle say how it stands, and its own card names its parent; a
// helper whose parent's tree is not read keeps a card of its own only.
func TestAHelperHangsUnderItsParentInTheFamilyTree(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	helper := treeGoblin(t, h.State, userHome, "g1-h1", true)
	helper.Parent, helper.Title = "g1", "Accounts migration"
	if err := state.WriteTaskMeta(h.State, helper); err != nil {
		t.Fatal(err)
	}
	treeGoblin(t, h.State, userHome, "g1", true)
	orphan := treeGoblin(t, h.State, userHome, "g2-h1", true)
	orphan.Parent = "g2"
	if err := state.WriteTaskMeta(h.State, orphan); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "g1-h1", "done: ready for g1 to merge"); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "g1-h1", "starting: a status line that is no report"); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Instance: "tree", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}

	// Act
	service.readTrees(context.Background())
	snapshot, err := service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	tasks := map[string]Task{}
	for _, task := range snapshot.Tasks {
		tasks[task.ID] = task
	}
	parent := tasks["g1"]
	if parent.Tree == nil {
		t.Fatal("g1 has no tree")
	}
	var found *fleettree.Node
	for i, child := range parent.Tree.Children {
		if child.Kind == fleettree.KindHelper {
			found = &parent.Tree.Children[i]
		}
	}
	if found == nil || found.ID != "helper:g1-h1" || found.Label != "Accounts migration" || found.State != fleettree.Done {
		t.Errorf("g1's helper child = %+v, want g1-h1 done", found)
	}
	if tasks["g1-h1"].Parent != "g1" || tasks["g1"].Parent != "" || tasks["g2-h1"].Parent != "g2" {
		t.Errorf("parents = %q, %q, %q; want each task's own", tasks["g1-h1"].Parent, tasks["g1"].Parent, tasks["g2-h1"].Parent)
	}
	if tree := tasks["g1-h1"].Tree; tree != nil {
		for _, child := range tree.Children {
			if child.Kind == fleettree.KindHelper {
				t.Errorf("the helper's own tree holds a helper: %+v", child)
			}
		}
	}
}
