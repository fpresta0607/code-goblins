package retention

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cleanup"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

type endpoint struct{ active bool }

func (p endpoint) Inspect(context.Context, state.TaskMeta) (monitor.EndpointSample, error) {
	agent := herdr.AgentDead
	if p.active {
		agent = herdr.AgentAlive
	}
	return monitor.EndpointSample{Verdict: monitor.ProbePresent, Agent: agent}, nil
}

type cleanupTransport struct{}

func (cleanupTransport) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "git" {
		return (execx.OSRunner{}).Run(ctx, req)
	}
	if req.Name == "herdr" && len(req.Args) > 1 && req.Args[0] == "api" && req.Args[1] == "snapshot" {
		return execx.Result{Stdout: []byte(`{"result":{"type":"session_snapshot","snapshot":{"protocol":22,"panes":[],"agents":[]}}}`)}, nil
	}
	if req.Name == "herdr" && req.Args[0] == "tab" && req.Args[1] == "close" {
		return execx.Result{Stdout: []byte(`{"result":{"type":"ok"}}`)}, nil
	}
	return execx.Result{}, errors.New("unexpected lifecycle operation")
}

func TestRealGitCleanupThenRetentionAfterMetadataAndWorktreeRemoval(t *testing.T) {
	service, paths, project := fixture(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	git(t, project, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	git(t, project, "update-ref", "-d", "refs/remotes/origin/main")
	wt := filepath.Join(project, ".worktrees", "gb-task")
	git(t, project, "worktree", "add", "-b", "completed", wt, "HEAD")
	if err := os.WriteFile(filepath.Join(wt, "landed.txt"), []byte("local-only landed content"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", "landed.txt")
	git(t, wt, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "Local-only work")
	git(t, project, "merge", "--ff-only", "completed")
	meta, err := state.ReadTaskMeta(service.Home.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	meta.Worktree = wt
	meta.Backend = "herdr"
	meta.HerdrSession = "fixture"
	meta.HerdrWorkspaceID = "w"
	meta.HerdrTabID = "t"
	meta.HerdrPaneID = "p"
	meta.TaskTmp = filepath.Join(service.Home.State, "tasktmp", "task")
	if err := os.MkdirAll(meta.TaskTmp, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(service.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	commands := cleanupTransport{}
	cleaner := cleanup.Service{StateDir: service.Home.State, Commands: commands, Herdr: &herdr.Client{Commands: commands, Session: "fixture"}, Worktrees: worktree.Service{Commands: commands}}
	if _, err := cleaner.Cleanup(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree not returned: %v", err)
	}
	if _, err := state.ReadTaskMeta(service.Home.State, "task"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live metadata not retired: %v", err)
	}
	if _, err := taskcontext.ReadRetirement(service.Home, "task"); err != nil {
		t.Fatal(err)
	}
	service.Now = func() time.Time { return time.Now().Add(100 * 24 * time.Hour) }
	entries, err := service.Run(context.Background(), "task", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.Removed {
			t.Fatalf("retired data stuck forever: %+v", entry)
		}
	}
	m, err := taskcontext.Refresh(context.Background(), service.Home, "task", execx.OSRunner{})
	if err != nil || !m.MetadataMissing || m.Pointers["recap"] != "present" || m.Pointers["worktree"] != "missing" || m.Pointers["browser_profile"] != "missing" {
		t.Fatalf("resume context lost: %+v %v", m, err)
	}
	if _, err := os.Stat(paths.Tests); err != nil {
		t.Fatal(err)
	}
}

type processList []reap.Process

func (p processList) List(context.Context) ([]reap.Process, error) { return p, nil }

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, output, err)
	}
	return strings.TrimSpace(string(output))
}
func fixture(t *testing.T) (Service, taskcontext.Paths, string) {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, project, "init", "-b", "main")
	git(t, project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "Synthetic initial content")
	git(t, project, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, project, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	meta := state.TaskMeta{ID: "task", Project: project, Worktree: project, Harness: "codex", Mode: "local-only", Brief: filepath.Join(root, "brief.md")}
	if err := os.WriteFile(meta.Brief, []byte("Synthetic brief"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "task", "done: synthetic merged task"); err != nil {
		t.Fatal(err)
	}
	manifest, err := taskcontext.Refresh(context.Background(), h, "task", execx.OSRunner{})
	if err != nil {
		t.Fatal(err)
	}
	paths := manifest.Paths
	for _, dir := range []string{paths.Browser.Profile, paths.Browser.Evidence} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "synthetic.txt")
		if err := os.WriteFile(file, []byte("synthetic browser evidence"), 0600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-100 * 24 * time.Hour)
		for _, path := range []string{file, dir} {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, path := range []string{paths.Recap, paths.Tests} {
		if err := os.WriteFile(path, []byte("retained synthetic summary"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return Service{Home: h, Commands: execx.OSRunner{}, Prober: endpoint{}, Processes: processList{}}, paths, project
}

func TestDryRunThenApplyKeepsContextBriefRecapAndTests(t *testing.T) {
	service, paths, _ := fixture(t)
	dry, err := service.Run(context.Background(), "task", false)
	if err != nil || len(dry) != 2 {
		t.Fatalf("%+v %v", dry, err)
	}
	for _, entry := range dry {
		if entry.Hold != "" || entry.Bytes == 0 || entry.Removed {
			t.Fatalf("dry run %+v", entry)
		}
	}
	applied, err := service.Run(context.Background(), "task", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range applied {
		if !entry.Removed {
			t.Fatalf("apply %+v", entry)
		}
	}
	for _, path := range []string{paths.Manifest, paths.Recap, paths.Tests, filepath.Join(filepath.Dir(paths.Manifest), "brief.md")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("context lost: %s %v", path, err)
		}
	}
	refreshed, err := taskcontext.Refresh(context.Background(), service.Home, "task", execx.OSRunner{})
	if err != nil || refreshed.Pointers["browser_profile"] != "missing" || refreshed.Pointers["recap"] != "present" {
		t.Fatalf("stale pointers: %+v %v", refreshed, err)
	}
}

func TestRetentionHoldsActiveUnmergedUnresolvedAndRecentData(t *testing.T) {
	for _, reason := range []string{"active", "unmerged", "unresolved", "browser", "recent"} {
		t.Run(reason, func(t *testing.T) {
			service, paths, project := fixture(t)
			switch reason {
			case "active":
				service.Prober = endpoint{active: true}
			case "browser":
				service.Processes = processList{{Name: "chrome.exe", CommandLine: `chrome --user-data-dir="` + paths.Browser.Profile + `"`}}
			case "unmerged":
				git(t, project, "checkout", "-b", "unmerged")
				if err := os.WriteFile(filepath.Join(project, "unpublished.txt"), []byte("preserve this work"), 0600); err != nil {
					t.Fatal(err)
				}
				git(t, project, "add", "unpublished.txt")
				git(t, project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "Unmerged work")
			case "unresolved":
				if err := state.AppendStatus(service.Home.State, "task", "blocked: preserve my decision"); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 220; i++ {
					if err := state.AppendStatus(service.Home.State, "task", "done: later status is not an answer"); err != nil {
						t.Fatal(err)
					}
				}
			case "recent":
				for _, dir := range []string{paths.Browser.Profile, paths.Browser.Evidence} {
					if err := os.Chtimes(dir, time.Now(), time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			entries, err := service.Run(context.Background(), "task", true)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Removed || entry.Hold == "" {
					t.Fatalf("unsafe removal: %+v", entry)
				}
			}
			if _, err := os.Stat(filepath.Join(paths.Browser.Profile, "synthetic.txt")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLinkedBrowserDataDoesNotTouchUnrelatedFiles(t *testing.T) {
	service, paths, _ := fixture(t)
	unrelated := t.TempDir()
	ownerFile := filepath.Join(unrelated, "owner.txt")
	if err := os.WriteFile(ownerFile, []byte("personal fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unrelated, filepath.Join(paths.Browser.Profile, "linked")); err != nil {
		t.Skipf("symbolic link unavailable: %v", err)
	}
	entries, err := service.Run(context.Background(), "task", true)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Removed || !strings.Contains(entries[0].Hold, "linked") {
		t.Fatalf("link followed: %+v", entries[0])
	}
	if data, err := os.ReadFile(ownerFile); err != nil || string(data) != "personal fixture" {
		t.Fatalf("unrelated data altered: %s %v", data, err)
	}
	if _, err := os.Stat(paths.Manifest); errors.Is(err, os.ErrNotExist) {
		t.Fatal("context deleted")
	}
}
