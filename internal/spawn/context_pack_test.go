package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// packGit answers the git reads of a context pack by their subcommand.
type packGit map[string]execx.Result

func (answers packGit) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	return answers[req.Args[0]], nil
}

func packHome(t *testing.T) (Service, state.TaskMeta) {
	t.Helper()
	root := t.TempDir()
	data, stateDir := filepath.Join(root, "data"), filepath.Join(root, "state")
	for name, content := range map[string]string{
		"data/overlord.md":             "## Harbor deploys, 2026-11-05\n\nMerging to main deploys the harbor site to production.\n",
		"data/memory/harbor-saves.md":  "---\nname: harbor-saves\ndescription: harbor saves go to the hosted database\nmetadata:\n  projects: [harbor]\n---\nharbor saves body\n",
		"state/harbor-other.meta":      "project=C:\\dev\\harbor\ntitle=Gallery captions\ngoblin_name=Ivy\nworktree=C:\\wt\\harbor-other\n",
		"state/harbor-other.status":    "2026-11-09T09:00:00Z working: PR https://github.com/acme/harbor/pull/11 opened\n2026-11-09T10:00:00Z working: captions under review\n",
		"state/northwind-task.meta":    "project=C:\\dev\\northwind\ntitle=Northwind cache\nworktree=C:\\wt\\northwind-task\n",
		"state/northwind-task.status":  "2026-11-09T10:00:00Z working: cache\n",
		"state/tasktmp/harbor-saves/x": "",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git := packGit{
		"branch":     {Stdout: []byte("feat/gallery-captions\n")},
		"merge-base": {Stdout: []byte("base123\n")},
		"log":        {Stdout: []byte("abc1234 feat(gallery): lazy images\n")},
	}
	meta := state.TaskMeta{ID: "harbor-saves", Project: `C:\dev\harbor`, Title: "Harbor configurator saves", Worktree: filepath.Join(root, "wt"), TaskTmp: filepath.Join(stateDir, "tasktmp", "harbor-saves")}
	return Service{StateDir: stateDir, DataDir: data, Commands: git}, meta
}

func readPack(t *testing.T, meta state.TaskMeta) string {
	t.Helper()
	content, err := fsx.ReadFile(filepath.Join(meta.TaskTmp, contextPackName))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestWriteContextPackDrawsMemoryAndTheRepositorysLiveState(t *testing.T) {
	service, meta := packHome(t)
	brief := "## Task\n\nStore harbor saves in the hosted database before the deploy.\n"

	instruction, recordDelivery, err := service.writeContextPack(context.Background(), meta, brief, false)

	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(instruction, filepath.Join(meta.TaskTmp, contextPackName)) || !strings.Contains(instruction, "within 6000 estimated tokens") || !strings.Contains(instruction, `cfo memory recall "<question>" --project harbor`) {
		t.Fatalf("instruction %q", instruction)
	}
	record := filepath.Join(meta.TaskTmp, contextPackRecord)
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("the pack was recorded as delivered before the goblin was given it: %v", err)
	}
	if err := recordDelivery(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("a delivered pack was not recorded: %v", err)
	}
	pack := readPack(t, meta)
	for _, want := range []string{"Merging to main deploys the harbor site to production.", "harbor-saves (", "harbor-other (Ivy): Gallery captions; branch feat/gallery-captions; pull request https://github.com/acme/harbor/pull/11; last report: working: captions under review", "on main since your base: abc1234 feat(gallery): lazy images"} {
		if !strings.Contains(pack, want) {
			t.Fatalf("the pack lacks %q:\n%s", want, pack)
		}
	}
	if strings.Contains(pack, "northwind") {
		t.Fatalf("another project's task reached the pack:\n%s", pack)
	}
}

// deliverPack writes a pack and records it delivered, as a start whose goblin
// was given its instruction does.
func deliverPack(t *testing.T, service Service, meta state.TaskMeta, brief string, isResumed bool) string {
	t.Helper()
	_, recordDelivery, err := service.writeContextPack(context.Background(), meta, brief, isResumed)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordDelivery(); err != nil {
		t.Fatal(err)
	}
	return readPack(t, meta)
}

func TestWriteContextPackGivesAResumedConversationOnlyWhatChanged(t *testing.T) {
	service, meta := packHome(t)
	brief := "## Task\n\nStore harbor saves in the hosted database before the deploy.\n"
	deliverPack(t, service, meta, brief, false)
	if err := state.AppendStatus(service.StateDir, "harbor-other", "done: PR https://github.com/acme/harbor/pull/12"); err != nil {
		t.Fatal(err)
	}

	resumed := deliverPack(t, service, meta, brief, true)
	fresh := deliverPack(t, service, meta, brief, false)

	if !strings.Contains(resumed, "- harbor-other (Ivy): Gallery captions; branch feat/gallery-captions; last report: done: PR https://github.com/acme/harbor/pull/12\n") || strings.Contains(resumed, "pull/11") || strings.Contains(resumed, "Merging to main deploys") || !strings.Contains(resumed, "already hold from your last pack are unchanged") {
		t.Fatalf("a resumed conversation was not given only the change:\n%s", resumed)
	}
	if !strings.Contains(fresh, "Merging to main deploys") || strings.Contains(fresh, "already hold") {
		t.Fatalf("a new conversation was not given the whole pack:\n%s", fresh)
	}
}

func TestWriteContextPackNeverClaimsAnUndeliveredPackOrTrustsAnUnreadableRecord(t *testing.T) {
	service, meta := packHome(t)
	brief := "## Task\n\nStore harbor saves in the hosted database before the deploy.\n"
	record := filepath.Join(meta.TaskTmp, contextPackRecord)
	deliverPack(t, service, meta, brief, false)
	if err := state.AppendStatus(service.StateDir, "harbor-other", "done: PR https://github.com/acme/harbor/pull/12"); err != nil {
		t.Fatal(err)
	}

	// A resumed relaunch whose goblin was never given its instruction leaves
	// the record as it was, so the retry still shows the change.
	if _, _, err := service.writeContextPack(context.Background(), meta, brief, true); err != nil {
		t.Fatal(err)
	}
	retried := deliverPack(t, service, meta, brief, true)
	// A new conversation's start drops the old record before its launch.
	if _, _, err := service.writeContextPack(context.Background(), meta, brief, false); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(record)
	if err := os.WriteFile(record, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable := deliverPack(t, service, meta, brief, true)

	if !strings.Contains(retried, "pull/12") {
		t.Fatalf("a retried resume lost the change its failed launch never delivered:\n%s", retried)
	}
	if !os.IsNotExist(statErr) {
		t.Fatalf("a new conversation's start kept the last conversation's record: %v", statErr)
	}
	if !strings.Contains(unreadable, "The record of your last pack could not be read") || !strings.Contains(unreadable, "Merging to main deploys") {
		t.Fatalf("an unreadable record did not give the whole pack and say why:\n%s", unreadable)
	}
}

func TestWriteContextPackNamesATaskWhoseRecordCannotBeRead(t *testing.T) {
	service, meta := packHome(t)
	if err := os.Mkdir(filepath.Join(service.StateDir, "harbor-broken.meta"), 0o755); err != nil {
		t.Fatal(err)
	}

	pack := deliverPack(t, service, meta, "## Task\n\nharbor saves\n", false)

	if !strings.Contains(pack, "- harbor-broken: its task record could not be read: ") || !strings.Contains(pack, "harbor-other (Ivy)") {
		t.Fatalf("an unreadable task record was not named beside the readable ones:\n%s", pack)
	}
}

func TestWriteContextPackReadsItsBudgetAndSaysWhatItCouldNotRead(t *testing.T) {
	service, meta := packHome(t)
	writeConfig := filepath.Join(filepath.Dir(service.DataDir), "config", "context-pack-budget")
	if err := os.MkdirAll(filepath.Dir(writeConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writeConfig, []byte("1200\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(service.DataDir, "overlord.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(service.DataDir, "overlord.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	service.Commands = packGit{"merge-base": {ExitCode: 128, Stderr: []byte("fatal: Not a valid object name origin/HEAD")}, "branch": {ExitCode: 128, Stderr: []byte("fatal: not a git repository")}}

	instruction, _, err := service.writeContextPack(context.Background(), meta, "## Task\n\nharbor\n", false)

	if err != nil || !strings.Contains(instruction, "within 1200 estimated tokens") {
		t.Fatalf("%q: %v", instruction, err)
	}
	if pack := readPack(t, meta); !strings.Contains(pack, "The fleet's memory could not be read for this pack") {
		t.Fatalf("an unreadable memory was not said:\n%s", pack)
	}
	if err := os.Remove(filepath.Join(service.DataDir, "overlord.md")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.writeContextPack(context.Background(), meta, "## Task\n\nharbor\n", false); err != nil {
		t.Fatal(err)
	}
	if pack := readPack(t, meta); !strings.Contains(pack, "could not be read: no merge base with origin/HEAD: git merge-base: fatal: Not a valid object name origin/HEAD") || !strings.Contains(pack, "branch unreadable: git branch") {
		t.Fatalf("an unreadable repository was not said:\n%s", pack)
	}
}

func TestWriteContextPackWritesNothingForAServiceWithNoMemory(t *testing.T) {
	service, meta := packHome(t)
	service.DataDir = ""

	instruction, recordDelivery, err := service.writeContextPack(context.Background(), meta, "brief", false)

	if err != nil || instruction != "" || recordDelivery() != nil {
		t.Fatalf("%q: %v", instruction, err)
	}
	if _, err := os.Stat(filepath.Join(meta.TaskTmp, contextPackName)); !os.IsNotExist(err) {
		t.Fatalf("a pack was written: %v", err)
	}
}
