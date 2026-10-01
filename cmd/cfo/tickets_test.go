package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// ticketsRuntime fakes GitHub for cfo tickets and records which checkout it
// was asked to read.
func ticketsRuntime(activity tickets.Activity, err error, readFrom *string) commandRuntime {
	return commandRuntime{repoActivity: func(_ context.Context, checkout string, _ time.Time) (tickets.Activity, error) {
		*readFrom = checkout
		return activity, err
	}}
}

func teammateRepository() tickets.Activity {
	recently := time.Now().Add(-2 * time.Hour)
	ana := tickets.Actor{Login: "ana-teammate", AvatarURL: "https://avatars.githubusercontent.com/u/9?v=4"}
	return tickets.Activity{
		Repository:    "fpresta0607/northwind-api",
		Viewer:        tickets.Actor{Login: "fpresta0607"},
		DefaultBranch: "main",
		Events:        []tickets.Event{{Kind: tickets.EventPullRequest, Number: 412, Author: ana, At: recently}},
		PullRequests: []tickets.PullRequest{{Number: 412, Title: "fix(orders): close the 10 defects from the checkout review", URL: "https://github.com/fpresta0607/northwind-api/pull/412",
			Author: ana, HeadRef: "fix/order-session-defects", CreatedAt: recently, Files: []string{"api/routes_orders.py", "tasks/billing_sync.py", "web/src/App.tsx"}}},
	}
}

func TestTicketsReportsTeammatePullRequestsThatOverlapTheBrief(t *testing.T) {
	// Arrange
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("# Brief nw-sync\n\n## Task\n\nSay why in tasks/billing_sync.py.\n\n## Constraints\n\n- web/src is nw-other's.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var readFrom string
	var stdout, stderr strings.Builder
	checkout := filepath.Join(t.TempDir(), "northwind-api")

	// Act
	code := runTickets([]string{checkout, "--brief", brief, "--files", "api/routes_orders.py"}, &stdout, &stderr, ticketsRuntime(teammateRepository(), nil, &readFrom))

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if readFrom != checkout {
		t.Fatalf("read %q, want the checkout %q", readFrom, checkout)
	}
	for _, want := range []string{
		"Collaborative: 1 person besides fpresta0607 worked here in the last 30 days.",
		"- #412 fix(orders): close the 10 defects from the checkout review (ana-teammate, opened 2h ago, 3 files, branch fix/order-session-defects)",
		"Overlaps with api/routes_orders.py, tasks/billing_sync.py\n",
		"- PR #412 by ana-teammate changes api/routes_orders.py, tasks/billing_sync.py\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestTicketsRendersJSON(t *testing.T) {
	// Arrange
	var readFrom string
	var stdout, stderr strings.Builder

	// Act
	code := runTickets([]string{`C:\dev\northwind-api`, "--json", "--files", "web/src"}, &stdout, &stderr, ticketsRuntime(teammateRepository(), nil, &readFrom))

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	var report tickets.Report
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if !report.Collaborative || len(report.Contributors) != 1 || report.Contributors[0].AvatarURL == "" {
		t.Fatalf("report = %+v", report)
	}
	if report.Overlaps == nil || len(report.Overlaps.Files) != 1 || report.Overlaps.Files[0].Files[0] != "web/src/App.tsx" {
		t.Fatalf("overlaps = %+v", report.Overlaps)
	}
}

func TestTicketsRefusesWhatItCannotRead(t *testing.T) {
	missingBrief := filepath.Join(t.TempDir(), "missing.md")
	cases := []struct {
		name     string
		args     []string
		readErr  error
		wantCode int
		want     string
	}{
		{name: "no project", args: nil, wantCode: 2, want: "a project is required"},
		{name: "a flag where the project goes", args: []string{"--json"}, wantCode: 2, want: "a project is required"},
		{name: "an extra argument", args: []string{`C:\dev\p`, "extra"}, wantCode: 2, want: "unexpected arguments"},
		{name: "a brief that is not there", args: []string{`C:\dev\p`, "--brief", missingBrief}, wantCode: 1, want: "read the brief"},
		{name: "GitHub cannot be read", args: []string{`C:\dev\p`}, readErr: errors.New("gh api graphql exited 1: HTTP 401: Bad credentials"), wantCode: 1, want: "HTTP 401: Bad credentials"},
		{name: "a bare name with no projects root", args: []string{"northwind-api"}, wantCode: 1, want: "projects root is not set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var readFrom string
			var stdout, stderr strings.Builder
			code := runTickets(tc.args, &stdout, &stderr, ticketsRuntime(teammateRepository(), tc.readErr, &readFrom))
			if code != tc.wantCode || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit = %d, stderr = %q, want %d and %q", code, stderr.String(), tc.wantCode, tc.want)
			}
		})
	}
}

func TestTicketsSaysWhenTheBriefNamesNoPath(t *testing.T) {
	// Arrange
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("## Task\n\nMake the refund email honest.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var readFrom string
	var stdout, stderr strings.Builder

	// Act
	code := runTickets([]string{`C:\dev\p`, "--brief", brief}, &stdout, &stderr, ticketsRuntime(teammateRepository(), nil, &readFrom))

	// Assert
	if code != 0 || !strings.Contains(stderr.String(), "the brief names no repository path") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestTicketsReadsAFolderFromTheBriefOnlyWhenTheCheckoutHasIt(t *testing.T) {
	// Arrange
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("## Task\n\nRestyle web/src and/or the CI/CD badge.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var readFrom string
	var stdout, stderr strings.Builder

	// Act
	code := runTickets([]string{checkout, "--brief", brief}, &stdout, &stderr, ticketsRuntime(teammateRepository(), nil, &readFrom))

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Overlaps with web/src\n") || strings.Contains(stdout.String(), "and/or") {
		t.Fatalf("output names the wrong area:\n%s", stdout.String())
	}
}

func TestTicketsSaysWhyNoPathIsCompared(t *testing.T) {
	cases := []struct {
		name    string
		brief   string
		files   []string
		want    []string
		notWant string
	}{
		{
			name:    "a brief with only prose slash words",
			brief:   "## Task\n\nRetry and/or refund the charge; keep CI/CD green.\n",
			want:    []string{"the brief names no repository path"},
			notWant: "neither",
		},
		{
			name:    "--files with an absolute Windows path",
			files:   []string{`C:\dev\northwind-api\api\x.py`},
			want:    []string{`--files C:\dev\northwind-api\api\x.py is not a repository path`, "--files names no repository path"},
			notWant: "the brief",
		},
		{
			name:    "--files with a URL",
			files:   []string{"https://github.com/fpresta0607/northwind-api/blob/main/api/x.py", "/srv/northwind-api/api/y.py"},
			want:    []string{"--files https://github.com/fpresta0607/northwind-api/blob/main/api/x.py is not a repository path", "--files /srv/northwind-api/api/y.py is not a repository path", "--files names no repository path"},
			notWant: "the brief",
		},
		{
			name:  "a brief and --files that both name none",
			brief: "## Task\n\nMake the refund email honest.\n",
			files: []string{`C:\dev\northwind-api\api\x.py`},
			want:  []string{`--files C:\dev\northwind-api\api\x.py is not a repository path`, "neither the brief nor --files names a repository path"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			args := []string{t.TempDir()}
			if tc.brief != "" {
				brief := filepath.Join(t.TempDir(), "brief.md")
				if err := os.WriteFile(brief, []byte(tc.brief), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--brief", brief)
			}
			for _, file := range tc.files {
				args = append(args, "--files", file)
			}
			var readFrom string
			var stdout, stderr strings.Builder

			// Act
			code := runTickets(args, &stdout, &stderr, ticketsRuntime(teammateRepository(), nil, &readFrom))

			// Assert
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr = %q, want it to say %q", stderr.String(), want)
				}
			}
			if tc.notWant != "" && strings.Contains(stderr.String(), tc.notWant) {
				t.Fatalf("stderr = %q, want nothing about %q", stderr.String(), tc.notWant)
			}
		})
	}
}
