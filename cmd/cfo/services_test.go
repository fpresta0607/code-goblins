package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// commandDocker is an engine that is already running, with nothing of the
// stack up until Up starts it.
type commandDocker struct {
	running []string
	calls   []string
}

func (d *commandDocker) EngineRunning(context.Context) (bool, error) { return true, nil }
func (d *commandDocker) StartEngine(context.Context) error           { return nil }
func (d *commandDocker) StopEngine(context.Context) error            { return nil }
func (d *commandDocker) RunningContainers(context.Context) ([]string, error) {
	return d.running, nil
}
func (d *commandDocker) Running(context.Context, services.Compose) ([]string, error) {
	return d.running, nil
}
func (d *commandDocker) Up(_ context.Context, _ services.Compose, names []string) error {
	d.calls = append(d.calls, "up "+strings.Join(names, ","))
	d.running = append(d.running, names...)
	return nil
}
func (d *commandDocker) Down(_ context.Context, _ services.Compose, names []string) error {
	d.calls = append(d.calls, "down "+strings.Join(names, ","))
	d.running = nil
	return nil
}

// servicesFixture is a primary home holding a live task pd-check, and a
// checkout whose project declares one local service.
func servicesFixture(t *testing.T, docker *commandDocker) (commandRuntime, string) {
	t.Helper()
	h := primaryHomeFixture(t)
	checkout := filepath.Join(t.TempDir(), "PrecisionDocs-AI")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.TaskMetaPath(h.State, "pd-check"), []byte("backend=native\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := services.ManifestPath(h.Data, checkout)
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"project":"PrecisionDocs-AI","compose":"docker-compose.dev.yml","services":["backend"],"memory_estimate_gb":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		projectServices: func(h home.Home, progress io.Writer) services.Service {
			return services.Service{
				StateDir: h.State, DataDir: h.Data, Docker: docker,
				Memory: func() (services.Memory, error) {
					return services.Memory{Available: 16 << 30, CommitAvailable: 16 << 30}, nil
				},
				Floor: 4 << 30, IsLive: services.LiveIn(h.State),
				Now: time.Now, Sleep: func(time.Duration) {}, Poll: time.Second, Out: progress,
			}
		},
	}
	return runtime, checkout
}

func TestServicesUpStartsTheStackAndDownStopsIt(t *testing.T) {
	// Arrange
	docker := &commandDocker{}
	runtime, checkout := servicesFixture(t, docker)
	var stdout, stderr bytes.Buffer

	// Act
	up := runServices([]string{"up", checkout, "--task", "pd-check"}, &stdout, &stderr, runtime)
	down := runServices([]string{"down", checkout, "--task", "pd-check"}, &stdout, &stderr, runtime)

	// Assert
	if up != 0 || down != 0 || stderr.Len() != 0 {
		t.Fatalf("up exit %d, down exit %d, stderr %q, want both 0", up, down, stderr.String())
	}
	if !slices.Equal(docker.calls, []string{"up backend", "down "}) {
		t.Errorf("docker calls = %q, want the stack started then taken down", docker.calls)
	}
	for _, want := range []string{
		"cfo services: started PrecisionDocs-AI's services for pd-check",
		"cfo services: pd-check was the last to hold them: stopped PrecisionDocs-AI's services",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestServicesUpReportsAProjectThatDeclaresNone(t *testing.T) {
	docker := &commandDocker{}
	runtime, _ := servicesFixture(t, docker)
	undeclared := filepath.Join(t.TempDir(), "siqsermon")
	if err := os.MkdirAll(undeclared, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	exit := runServices([]string{"up", undeclared, "--task", "pd-check"}, &stdout, &stderr, runtime)

	if exit != 1 || !strings.Contains(stderr.String(), "siqsermon declares no local services: write ") {
		t.Fatalf("exit %d stderr %q, want 1 naming the manifest to write", exit, stderr.String())
	}
	if len(docker.calls) != 0 {
		t.Errorf("docker calls = %q, want none", docker.calls)
	}
}

func TestServicesArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no verb", nil, "usage: cfo services up"},
		{"unknown verb", []string{"restart", "PrecisionDocs-AI", "--task", "pd-check"}, "usage: cfo services up"},
		{"no task", []string{"up", "PrecisionDocs-AI"}, "usage: cfo services up"},
		{"no project", []string{"down", "--task", "pd-check"}, "usage: cfo services up"},
		{"two projects", []string{"up", "a", "b", "--task", "pd-check"}, "usage: cfo services up"},
		{"a negative wait", []string{"up", "PrecisionDocs-AI", "--task", "pd-check", "--wait", "-1m"}, "usage: cfo services up"},
		{"a wait on down", []string{"down", "PrecisionDocs-AI", "--task", "pd-check", "--wait", "1m"}, "flag provided but not defined: -wait"},
		{"a task id that is no task id", []string{"up", "PrecisionDocs-AI", "--task", "../pd"}, "task"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runServices(test.args, &stdout, &stderr, commandRuntime{})
			if exit != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit=%d stderr=%q, want 2 with %q", exit, stderr.String(), test.want)
			}
		})
	}
}
