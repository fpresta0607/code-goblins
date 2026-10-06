package homemove

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkoutHome is a code-goblins checkout an older build made its home: its
// source tracked, and beside it the fleet's state, data, config and caches,
// and a goblin's worktree under .worktrees.
func checkoutHome(t *testing.T) (string, map[string]bool) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "code-goblins")
	write(t, filepath.Join(root, "AGENTS.md"), "contract")
	write(t, filepath.Join(root, "data", "routing.json"), `{"lanes":[]}`)
	write(t, filepath.Join(root, "config", "pipeline.json"), `{"classes":{}}`)
	write(t, filepath.Join(root, ".gitignore"), "/projects/\n/state/\n/data/*\n!/data/routing.json\n/caches/\n/config/fleet.json\n.worktrees/\n/cfo.exe\n")
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "source")
	write(t, filepath.Join(root, "state", "g1.meta"), strings.Join([]string{
		"brief=" + filepath.Join(root, "data", "g1", "brief.md"),
		"project=C:\\dev\\app",
		"tasktmp=" + filepath.Join(root, "state", "tasktmp", "g1"),
		"worktree=" + filepath.Join(root, ".worktrees", "gb-g1"),
	}, "\n")+"\n")
	write(t, filepath.Join(root, "state", "g1.status"), "working: in "+filepath.Join(root, ".worktrees", "gb-g1")+"\n")
	write(t, filepath.Join(root, "state", "tasktmp", "g1", "auth.ps1"), "secret-free")
	write(t, filepath.Join(root, "state", "wake.jsonl"), `{"seq":1}`)
	write(t, filepath.Join(root, "data", "backlog.md"), "## Queued\n")
	write(t, filepath.Join(root, "data", "g1", "brief.md"), "do the work")
	write(t, filepath.Join(root, "config", "fleet.json"), `{"max_live_goblins":4}`)
	write(t, filepath.Join(root, "caches", "uv", "blob"), "downloaded")
	write(t, filepath.Join(root, ".worktrees", "gb-g1", "work.go"), "package work")
	write(t, filepath.Join(root, "cfo.exe"), "old build")
	write(t, filepath.Join(root, "projects", "old-clone", "README.md"), "a clone kept under the home")
	tracked := map[string]bool{}
	for _, name := range strings.Split(gitIn(t, root, "ls-files"), "\n") {
		tracked[name] = true
	}
	return root, tracked
}

func TestPlanMoveListsEveryFileWithItsHashAndProvesNoneDropped(t *testing.T) {
	// Arrange
	from, tracked := checkoutHome(t)
	to := filepath.Join(t.TempDir(), "CodeGoblins")

	// Act
	plan, err := PlanMove(from, to, tracked, time.Now())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	listing := strings.Join(plan.Listing(), "\n")
	for _, want := range []string{"move state/g1.meta ", "move state/tasktmp/g1/auth.ps1 ", "move data/g1/brief.md ", "copy data/routing.json ", "copy config/pipeline.json ", "move config/fleet.json ", "move caches (1 files, 10 bytes)", "rewrite state/g1.meta ", "names the old home state/g1.status", "stays .worktrees", "stays projects (no part", "remove cfo.exe ("} {
		if !strings.Contains(listing, want) {
			t.Errorf("the listing lacks %q:\n%s", want, listing)
		}
	}
	for _, unwanted := range []string{"AGENTS.md", "\nmove cfo.exe", "work.go"} {
		if strings.Contains(listing, unwanted) {
			t.Errorf("the listing moves %s, which is source, a binary or a worktree:\n%s", unwanted, listing)
		}
	}
	proof := plan.Proof()
	if proof.Files != 9 || proof.Copied != 2 || proof.Rewritten != 1 || proof.BeforeDigest == "" || proof.BeforeDigest != proof.AfterDigest {
		t.Errorf("proof = %+v, want 9 files, 2 copied, 1 rewritten and one digest before and after", proof)
	}
	again, err := PlanMove(from, to, tracked, plan.Now)
	if err != nil || again.Digest() != plan.Digest() {
		t.Errorf("the same plan made again has digest %s, %v; want %s", again.Digest(), err, plan.Digest())
	}
}

func TestApplyMovesTheHomeAndReadsItBackIdentical(t *testing.T) {
	// Arrange
	from, tracked := checkoutHome(t)
	to := filepath.Join(t.TempDir(), "CodeGoblins")
	plan, err := PlanMove(from, to, tracked, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Act
	result, err := plan.Apply()

	// Assert
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.AfterDigest != plan.Proof().BeforeDigest || result.Files != 9 || result.CacheFiles != 1 || len(result.Differ) != 0 {
		t.Errorf("read back %+v, want every file as planned under one digest", result)
	}
	meta, err := os.ReadFile(filepath.Join(to, "state", "g1.meta"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tasktmp=" + filepath.Join(to, "state", "tasktmp", "g1"), "brief=" + filepath.Join(to, "data", "g1", "brief.md"), "worktree=" + filepath.Join(from, ".worktrees", "gb-g1")} {
		if !strings.Contains(string(meta), want) {
			t.Errorf("the moved record lacks %q:\n%s", want, meta)
		}
	}
	for _, gone := range []string{"state", "caches", filepath.Join("config", "fleet.json"), filepath.Join("data", "g1")} {
		if _, err := os.Stat(filepath.Join(from, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the old home: %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(from, ".worktrees", "gb-g1", "work.go")); err != nil {
		t.Errorf("the goblin's worktree moved: %v", err)
	}
	if status := gitIn(t, from, "status", "--porcelain"); status != "" {
		t.Errorf("the checkout's git status after the move = %q, want its source as it was", status)
	}
}

// A real home holds gigabytes, so on one drive the move renames and never
// needs room for a second copy: a folder with nothing tracked moves whole,
// every untracked file beside a tracked one moves by rename, and only a
// tracked file is copied. The large folders here are large in count and
// depth rather than bytes, a thousand files and a path past Windows'
// 260-character limit, because a file that keeps its identity was renamed
// however big it is.
func TestApplyRenamesOnOneDriveSoALargeHomeNeedsNoRoomToMove(t *testing.T) {
	// Arrange
	from, tracked := checkoutHome(t)
	deep := strings.Join([]string{"caches", "go-mod", strings.Repeat("d", 60), strings.Repeat("e", 60), strings.Repeat("f", 60), strings.Repeat("g", 60), "testcases.go"}, "/")
	write(t, filepath.Join(from, filepath.FromSlash(deep)), "package testcases")
	for i := 0; i < 1000; i++ {
		write(t, filepath.Join(from, "state", "archive", fmt.Sprintf("task-%02d", i/50), fmt.Sprintf("log-%03d.txt", i)), fmt.Sprintf("line %d", i))
	}
	renamed := []string{"state/archive/task-00/log-000.txt", "state/archive/task-19/log-999.txt", "state/wake.jsonl", deep, "data/backlog.md", "config/fleet.json"}
	before := map[string]os.FileInfo{}
	for _, rel := range append(renamed, "data/routing.json") {
		info, err := os.Stat(filepath.Join(from, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		// Comparing a file with itself reads its identity now, while its path
		// still leads to it.
		if !os.SameFile(info, info) {
			t.Fatalf("%s has no readable identity", rel)
		}
		before[rel] = info
	}
	to := filepath.Join(filepath.Dir(from), "CodeGoblins")
	plan, err := PlanMove(from, to, tracked, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Act
	result, err := plan.Apply()

	// Assert
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.AfterDigest != plan.Proof().BeforeDigest || result.Files != 1009 || result.CacheFiles != 2 || len(result.Differ) != 0 {
		t.Errorf("read back %+v, want every file as planned under one digest", result)
	}
	for _, rel := range renamed {
		after, err := os.Stat(filepath.Join(to, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s is not in the new home: %v", rel, err)
		}
		if !os.SameFile(before[rel], after) {
			t.Errorf("%s was copied into the new home, want it renamed", rel)
		}
	}
	kept, err := os.Stat(filepath.Join(from, "data", "routing.json"))
	if err != nil || !os.SameFile(before["data/routing.json"], kept) {
		t.Errorf("the checkout's tracked data/routing.json did not stay where it was: %v", err)
	}
	if moved, err := os.Stat(filepath.Join(to, "data", "routing.json")); err != nil || os.SameFile(kept, moved) {
		t.Errorf("the new home's data/routing.json is not a copy of the tracked file: %v", err)
	}
}

func TestPlanMoveNeverMergesIntoAHome(t *testing.T) {
	from, tracked := checkoutHome(t)
	to := t.TempDir()
	write(t, filepath.Join(to, "state", "x.meta"), "id=x")

	if _, err := PlanMove(from, to, tracked, time.Now()); err == nil || !strings.Contains(err.Error(), "never merges") {
		t.Errorf("PlanMove into a home = %v, want a refusal", err)
	}
}

func TestApplyRefusesARecordThatChangedAndPutsNothingBackWrong(t *testing.T) {
	from, tracked := checkoutHome(t)
	to := filepath.Join(t.TempDir(), "CodeGoblins")
	plan, err := PlanMove(from, to, tracked, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(from, "state", "g1.meta"), "changed after the plan\n")

	if _, err := plan.Apply(); err == nil {
		t.Fatal("Apply over a record that changed since the plan succeeded")
	}
	if got, _ := os.ReadFile(filepath.Join(to, "state", "g1.meta")); string(got) != "changed after the plan\n" {
		t.Errorf("the changed record = %q, want it moved as it is and not rewritten", got)
	}
}
