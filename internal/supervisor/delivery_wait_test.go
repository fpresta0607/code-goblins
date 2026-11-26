package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// approveDelivery gives the checkout's project fast checks under a
// no-mistakes posture and keeps the registered primary CEO's exception
// approving mode for task id, as goblins brief or spawn keeps it.
func approveDelivery(t *testing.T, h home.Home, id, checkout, mode string) {
	t.Helper()
	manifest := writeChecks(t, h, checkout, project.Command{"git", "status"})
	if err := project.RecordDeliveryException(h.Data, id, mode, "the Overlord approved it", "registration", manifest); err != nil {
		t.Fatal(err)
	}
}

func writeChecks(t *testing.T, h home.Home, checkout string, fast project.Command) project.Manifest {
	t.Helper()
	manifest := project.Manifest{Project: "demo", Delivery: project.DeliveryPolicy{Posture: "no-mistakes"}, Verification: project.Verification{Fast: []project.Command{fast}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := project.LoadForCheckout(h.Data, checkout)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// Issue 2146: a brief whose mode is narrower than its project's posture and
// assessment select starts automatically, or from the board, only under the
// exception the registered primary CEO approved for it. Without one the start
// waits and says how to approve it, so it never freezes no-mistakes ahead of
// the CEO's explicit spawn bringing the exception.
func TestAStartOfANarrowerModeWaitsForTheExceptionThePrimaryCEOApproved(t *testing.T) {
	for _, test := range []struct {
		name, approved string
		hasNewChecks   bool
		want           []string
	}{
		{name: "no exception kept", want: []string{"asks for direct-PR delivery and waits for its approval", "is not authorized", "goblins spawn next-task --project code-goblins --brief", "--delivery-exception"}},
		{name: "an exception for another mode", approved: "local-only", want: []string{"waits for its approval", "approves local-only, not direct-PR"}},
		{name: "project checks changed since approval", approved: "direct-PR", hasNewChecks: true, want: []string{"waits for its approval", "checks changed since the delivery exception kept for next-task was approved"}},
		{name: "an approved exception", approved: "direct-PR"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{output: "spawned next-task harness=claude kind=ship"}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", strings.NewReplacer("C:\\dev\\code-goblins", "code-goblins", "mode: no-mistakes", "mode: direct-PR").Replace(plainBrief))
			writeChecks(t, h, "code-goblins", project.Command{"git", "status"})
			if test.approved != "" {
				approveDelivery(t, h, "next-task", "code-goblins", test.approved)
			}
			if test.hasNewChecks {
				writeChecks(t, h, "code-goblins", project.Command{"git", "status", "--short"})
			}

			// Act
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

			// Assert
			if len(test.want) == 0 {
				if response.Code != 202 {
					t.Fatalf("start = %d %s, want 202", response.Code, response.Body)
				}
				waitStarted(t, handler, "next-task")
				if calls := spawner.recorded(); len(calls) != 1 || !slices.Contains(calls[0], "direct-PR") {
					t.Fatalf("goblins spawn ran %q, want once with direct-PR", calls)
				}
				return
			}
			if response.Code != 409 || len(spawner.recorded()) != 0 {
				t.Fatalf("start = %d %s with %d spawns, want 409 and none", response.Code, response.Body, len(spawner.recorded()))
			}
			for _, want := range test.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("refusal %s; want %q", response.Body, want)
				}
			}
		})
	}
}
