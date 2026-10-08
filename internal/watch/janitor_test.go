package watch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/janitor"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/services"
)

// TestStrayWakeFiresOnlyForAStrayNotReportedBefore: the janitor reports the
// same strays on every pass, and one going away is nothing the CFO must act
// on; only a stray it has not been told about wakes it.
func TestStrayWakeFiresOnlyForAStrayNotReportedBefore(t *testing.T) {
	strays := func(paths ...string) janitor.Record {
		var record janitor.Record
		for _, path := range paths {
			record.Strays = append(record.Strays, janitor.Item{Kind: "worktree", Path: path, Detail: "no task records it"})
		}
		return record
	}
	cases := []struct {
		name     string
		previous janitor.Record
		current  janitor.Record
		wantWake bool
	}{
		{"the first stray", strays(), strays(`C:\dev\a`), true},
		{"the same strays again", strays(`C:\dev\a`, `C:\dev\b`), strays(`C:\dev\b`, `C:\dev\a`), false},
		{"the same stray in another case", strays(`C:\dev\a`), strays(`c:\DEV\a`), false},
		{"one of two went away", strays(`C:\dev\a`, `C:\dev\b`), strays(`C:\dev\a`), false},
		{"a new one beside the old", strays(`C:\dev\a`), strays(`C:\dev\a`, `C:\dev\c`), true},
		{"none left", strays(`C:\dev\a`), strays(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			detail := strayWake(c.previous, c.current)
			if (detail != "") != c.wantWake {
				t.Fatalf("strayWake = %q, want a wake %v", detail, c.wantWake)
			}
			for _, stray := range c.current.Strays {
				if c.wantWake && !strings.Contains(detail, stray.Path) {
					t.Errorf("strayWake = %q, want every stray named, %s among them", detail, stray.Path)
				}
			}
		})
	}
}

// dockerRunner answers docker as an engine running one stack and records
// every call.
type dockerRunner struct {
	calls []string
}

func (r *dockerRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	call := request.Name + " " + strings.Join(request.Args, " ")
	r.calls = append(r.calls, call)
	switch {
	case strings.HasPrefix(call, "docker info"):
		return execx.Result{Stdout: []byte("29.7.2\n")}, nil
	case strings.HasPrefix(call, "docker compose") && strings.Contains(call, " ps "):
		return execx.Result{Stdout: []byte("backend\nredis\n")}, nil
	}
	return execx.Result{}, nil
}

// The watcher's janitor pass stops a stack cfo services started whose
// holders have all ended, through docker compose in the stack's checkout.
func TestTheJanitorPassStopsALocalServicesStackNoLiveTaskHolds(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(root, "PrecisionDocs-AI")
	record := services.Record{Stacks: map[string]services.Stack{"PrecisionDocs-AI": {
		Project: "PrecisionDocs-AI", Checkout: checkout, Compose: "docker-compose.dev.yml", Owned: true,
		Holders: []services.Hold{{Task: "ended-task"}},
	}}}
	if err := services.WriteRecord(h.State, record); err != nil {
		t.Fatal(err)
	}
	runner := &dockerRunner{}

	runJanitor(Config{Home: h, Reap: &reap.Service{Commands: runner}, JanitorEvery: time.Hour}, reap.Inventory{})

	if !slices.Contains(runner.calls, "docker compose --file docker-compose.dev.yml down") {
		t.Errorf("docker calls = %q, want the stack taken down", runner.calls)
	}
	after, err := services.ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if stack := after.Stacks["PrecisionDocs-AI"]; stack.IsUp() || stack.Owned {
		t.Errorf("stack = %+v, want it released and stopped", stack)
	}
	swept, err := janitor.ReadRecord(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(swept.Removed) != 1 || swept.Removed[0].Kind != "services" {
		t.Errorf("janitor removed = %+v, want the stopped stack recorded", swept.Removed)
	}
}
