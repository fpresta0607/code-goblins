package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type runnerFunc func(context.Context, execx.Request) (execx.Result, error)

func (f runnerFunc) Run(c context.Context, r execx.Request) (execx.Result, error) { return f(c, r) }

func testPolicy(t *testing.T) Policy {
	t.Helper()
	p, err := Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderPreservesUnownedConfigAndIsIdempotent(t *testing.T) {
	p := testPolicy(t)
	before := []byte("# machine config\nagent: [pi]\nauto_fix:\n  review: 10\n  document: 4\nci_timeout: 168h\nprivate_token: sentinel-secret\n")
	after, drift, err := Render(before, p)
	if err != nil || len(drift) == 0 {
		t.Fatalf("render: %v %v", drift, err)
	}
	for _, keep := range []string{"# machine config", "document: 4", "ci_timeout: 168h", "private_token: sentinel-secret"} {
		if !strings.Contains(string(after), keep) {
			t.Fatalf("lost %q", keep)
		}
	}
	if strings.Contains(strings.Join(drift, " "), "sentinel-secret") {
		t.Fatal("drift leaked secret")
	}
	again, drift, err := Render(after, p)
	if err != nil || len(drift) != 0 || string(again) != string(after) {
		t.Fatalf("not idempotent: %v %v", drift, err)
	}
}

func TestRenderRejectsAmbiguousYAML(t *testing.T) {
	for _, source := range []string{"agent: [claude]\nagent: [pi]\n", "agent: [claude]\n---\nagent: [pi]\n", "auto_fix: &x {review: 10}\nother: *x\n", "auto_fix: nope\n"} {
		if _, _, err := Render([]byte(source), testPolicy(t)); err == nil {
			t.Errorf("accepted ambiguous YAML %q", source)
		}
	}
}

func TestApplyRefusesBusyAndBacksUpIdleConfig(t *testing.T) {
	for _, busy := range []bool{true, false} {
		t.Run(map[bool]string{true: "busy", false: "idle"}[busy], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			before := "agent: [pi]\n"
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			applies := 0
			c := Config{Path: path, Policy: testPolicy(t), Idle: func(context.Context) (func() error, error) {
				applies++
				if busy {
					return nil, ErrBusy
				}
				return func() error { return nil }, nil
			}}
			result, err := c.Apply(context.Background())
			now, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if busy {
				if err == nil || string(now) != before || result.Backup != "" {
					t.Fatalf("busy mutation: %+v %v", result, err)
				}
			} else {
				if err != nil || result.Backup == "" || applies != 1 {
					t.Fatalf("apply: %+v %v", result, err)
				}
				old, err := os.ReadFile(result.Backup)
				if err != nil || string(old) != before {
					t.Fatalf("backup: %s %v", old, err)
				}
				_, drift, err := Render(now, testPolicy(t))
				if err != nil || len(drift) != 0 {
					t.Fatalf("applied drift: %v %v", drift, err)
				}
			}
		})
	}
}
