package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

const authoredBrief = "## Task\nBuild the requested change.\n## Acceptance criteria\nThe requested behavior works.\n## Constraints\nChange only the requested behavior.\n## Authentication\ncredentials: none\nmcp: none\n## Delivery\nmode: no-mistakes\nready: true"

func TestBriefReadinessRequiresAuthoredRequirementsAndFinalLaunchChoices(t *testing.T) {
	tests := []struct {
		name    string
		content string
		isReady bool
	}{
		{name: "empty"},
		{name: "whitespace", content: " \r\n\t"},
		{name: "task placeholder", content: "{TASK - describe work}"},
		{name: "acceptance placeholder", content: "{ACCEPTANCE - outcomes}\n## Delivery\nready: true"},
		{name: "constraints placeholder", content: "{CONSTRAINTS - boundaries}\n## Delivery\nready: true"},
		{name: "still authoring choices", content: "## Task\nWork.\n## Delivery\nready: false"},
		{name: "empty readiness", content: "## Delivery\nready:"},
		{name: "invalid readiness", content: "## Delivery\nready: yes"},
		{name: "duplicate readiness", content: "## Delivery\nready: true\nready: true"},
		{name: "conflicting readiness", content: "## Delivery\nready: true\nready: false"},
		{name: "missing requirements", content: "## Delivery\nmode: no-mistakes\nready: true"},
		{name: "missing task", content: strings.ReplaceAll(authoredBrief, "Build the requested change.", "")},
		{name: "comment instead of task", content: strings.ReplaceAll(authoredBrief, "Build the requested change.", "<!-- Author the task here. -->")},
		{name: "missing acceptance", content: strings.ReplaceAll(authoredBrief, "The requested behavior works.", "")},
		{name: "missing constraints", content: strings.ReplaceAll(authoredBrief, "Change only the requested behavior.", "")},
		{name: "missing credentials", content: strings.ReplaceAll(authoredBrief, "credentials: none\n", "")},
		{name: "comment in credential key", content: strings.ReplaceAll(authoredBrief, "credentials: none", "creden<!-- unfinished -->tials: none")},
		{name: "missing mcp", content: strings.ReplaceAll(authoredBrief, "mcp: none\n", "")},
		{name: "malformed credentials", content: strings.ReplaceAll(authoredBrief, "credentials: none", "credentials: none, github")},
		{name: "missing mode", content: strings.ReplaceAll(authoredBrief, "mode: no-mistakes\n", "")},
		{name: "invalid mode", content: strings.ReplaceAll(authoredBrief, "mode: no-mistakes", "mode: perhaps")},
		{name: "missing finalization", content: strings.ReplaceAll(authoredBrief, "ready: true", "")},
		{name: "finalized choices", content: authoredBrief, isReady: true},
		{name: "CRLF", content: strings.ReplaceAll(authoredBrief, "\n", "\r\n"), isReady: true},
		{name: "task text is not metadata", content: strings.ReplaceAll(authoredBrief, "Build the requested change.", "Explain ready: false."), isReady: true},
		{name: "quoted issue example is not a placeholder", content: strings.ReplaceAll(authoredBrief, "Build the requested change.", "Fix the issue reporting this example:\n> {TASK - describe work}"), isReady: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBriefReadiness([]byte(test.content))
			if (err == nil) != test.isReady || err != nil && !errors.Is(err, ErrBriefNotReady) {
				t.Fatalf("readiness = %v, want ready %v", err, test.isReady)
			}
		})
	}
}

func TestQueuedBriefRecordsFinalDefaultCredentialsBeforeAdmission(t *testing.T) {
	for _, hasDefaults := range []bool{false, true} {
		t.Run(fmt.Sprint(hasDefaults), func(t *testing.T) {
			// Arrange
			h := home.Home{Data: t.TempDir(), State: t.TempDir()}
			if hasDefaults {
				path := auth.ManifestPath(h.Data, "example")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"project":"example","services":[{"name":"source","method":"env","env":["FIXTURE_SOURCE"],"default":true}]}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			queued := QueuedTask{Row: BacklogRow{ID: "queued-task", Title: "Deliver the requested change", Repo: "example", Mode: "local-only"}}

			// Act
			if err := WriteQueuedBrief(h, queued); err != nil {
				t.Fatal(err)
			}
			content, err := fsx.ReadFile(filepath.Join(h.Data, "queued-task", "brief.md"))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateBriefReadiness(content); err != nil {
				t.Fatal(err)
			}
			need, err := auth.NeedFromBrief(string(content))
			if err != nil || need.IsUnstated || need.IsMCPUnstated || len(need.Services) != map[bool]int{false: 0, true: 1}[hasDefaults] || hasDefaults && need.Services[0] != "source" {
				t.Fatalf("recorded choices = %+v, %v", need, err)
			}
		})
	}
}

func TestPartialBriefWritesNeverBecomeReady(t *testing.T) {
	for end := range len(authoredBrief) {
		t.Run(fmt.Sprint(end), func(t *testing.T) {
			if err := ValidateBriefReadiness([]byte(authoredBrief[:end])); !errors.Is(err, ErrBriefNotReady) {
				t.Fatalf("partial brief ending at %d admitted: %v", end, err)
			}
		})
	}
}
