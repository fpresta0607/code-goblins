package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type proofRunner struct {
	stdout string
	exit   int
	err    error
}

const vercelStatusContextJSON = `{"__typename":"StatusContext","context":"Vercel","startedAt":"2026-09-12T21:54:33Z","state":"SUCCESS","targetUrl":"https://vercel.com/siqstack-llc/siqshift/HmkjwsSpLBbfinmpmLtU9arpEUFf"}`

func (r proofRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if req.Name != "gh" || len(req.Args) < 2 || req.Args[0] != "pr" || req.Args[1] != "view" {
		return execx.Result{}, errors.New("unexpected command")
	}
	return execx.Result{ExitCode: r.exit, Stdout: []byte(r.stdout), Stderr: []byte("gh failed")}, r.err
}

func TestVerifyPRReadyAcceptsOnlyGreenTerminalEvidence(t *testing.T) {
	r := proofRunner{stdout: readyPRView(`{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"NEUTRAL"},{"__typename":"CheckRun","name":"docs","status":"COMPLETED","conclusion":"SKIPPED"}`)}
	proof, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", r)
	if err != nil {
		t.Fatal(err)
	}
	if proof.HeadRefOID != "abc123" || proof.Checks != 3 {
		t.Fatalf("proof=%+v", proof)
	}
}

func TestVerifyPRReadyAcceptsObservedVercelStatusContext(t *testing.T) {
	proof, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(vercelStatusContextJSON)})
	if err != nil {
		t.Fatal(err)
	}
	if proof.HeadRefOID != "abc123" || proof.Checks != 1 {
		t.Fatalf("proof=%+v", proof)
	}
}

func TestVerifyPRReadyAcceptsMixedCheckRunsAndStatusContexts(t *testing.T) {
	checks := `{"__typename":"CheckRun","name":"verify","status":"COMPLETED","conclusion":"SUCCESS"},` + vercelStatusContextJSON
	proof, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(checks)})
	if err != nil {
		t.Fatal(err)
	}
	if proof.Checks != 2 {
		t.Fatalf("proof=%+v", proof)
	}
}

func TestVerifyPRReadyRefusesNonSuccessfulCheckRuns(t *testing.T) {
	cases := []struct {
		name  string
		check string
		want  string
	}{
		{"pending", `{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":""}`, "test is IN_PROGRESS, not completed"},
		{"cancelled", `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"CANCELLED"}`, "test concluded CANCELLED"},
		{"timed out", `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"TIMED_OUT"}`, "test concluded TIMED_OUT"},
		{"failed", `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}`, "test concluded FAILURE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := proofRunner{stdout: readyPRView(tc.check)}
			_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestVerifyPRReadyRefusesNonSuccessfulStatusContexts(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  string
	}{
		{"pending", "PENDING", "Vercel is PENDING, not completed"},
		{"failed", "FAILURE", "Vercel concluded FAILURE"},
		{"error", "ERROR", "Vercel concluded ERROR"},
		{"unknown", "STALE", "Vercel is STALE, not completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := `{"__typename":"StatusContext","context":"Vercel","state":"` + tc.state + `"}`
			_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(check)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestVerifyPRReadyValidatesEveryMixedRollupEntry(t *testing.T) {
	checks := vercelStatusContextJSON + `,{"__typename":"CheckRun","name":"verify","status":"COMPLETED","conclusion":"FAILURE"}`
	_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(checks)})
	if err == nil || !strings.Contains(err.Error(), "verify concluded FAILURE") {
		t.Fatalf("err=%v", err)
	}
}

func TestVerifyPRReadyRefusesUnknownRollupShapes(t *testing.T) {
	cases := []struct {
		name  string
		check string
		want  string
	}{
		{"missing type", `{"context":"Vercel","state":"SUCCESS"}`, `missing __typename (name="" context="Vercel" status="" state="SUCCESS" conclusion="")`},
		{"unsupported type", `{"__typename":"DeploymentStatus","name":"deploy","status":"COMPLETED","conclusion":"SUCCESS"}`, `unsupported status check type "DeploymentStatus" (name="deploy" context="" status="COMPLETED" state="" conclusion="SUCCESS")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(tc.check)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestVerifyPRReadyRefusesUnverifiedPR(t *testing.T) {
	r := proofRunner{stdout: `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"","headRefOid":"abc123","statusCheckRollup":[]}`}
	_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", r)
	if err == nil || !strings.Contains(err.Error(), "no status checks") {
		t.Fatalf("err=%v", err)
	}
}

func TestVerifyPRReadyRefusesDraftConflictAndRequestedChanges(t *testing.T) {
	views := []string{
		`{"state":"OPEN","isDraft":true,"mergeable":"MERGEABLE","headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
		`{"state":"OPEN","isDraft":false,"mergeable":"CONFLICTING","headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
		`{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"CHANGES_REQUESTED","headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
	}
	for _, view := range views {
		if _, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: view}); err == nil {
			t.Fatalf("expected refusal for %s", view)
		}
	}
}

// A draft's run skips its jobs, and GitHub counts a skipped check as
// passing, so a pull request marked ready reads green on its draft run's
// checks until its ready run registers. A rollup in which no check ran tested
// nothing, and nothing merges on it.
func TestVerifyPRReadyRefusesARollupInWhichNoCheckRan(t *testing.T) {
	for name, checks := range map[string]string{
		"every check skipped":        `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SKIPPED"},{"__typename":"CheckRun","name":"plan","status":"COMPLETED","conclusion":"SKIPPED"}`,
		"skipped and neutral checks": `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SKIPPED"},{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"NEUTRAL"}`,
		// The workflows' draft job reads whether the pull request is a draft
		// and tests nothing: on a draft it succeeds while every other job is
		// skipped (#963).
		"a draft's run, whose draft job succeeded": `{"__typename":"CheckRun","name":"draft state","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SKIPPED"}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(checks)})

			// Assert
			if err == nil || !strings.Contains(err.Error(), "no check ran") {
				t.Fatalf("err=%v, want a refusal saying no check ran", err)
			}
		})
	}
}

// The workflows' draft job tests nothing, so how it concluded neither proves
// a head nor refuses one: a read of the draft state that failed ran every job
// all the same, and those passed (#963).
func TestVerifyPRReadyPassesOverTheDraftStateCheck(t *testing.T) {
	// Arrange
	checks := `{"__typename":"CheckRun","name":"draft state","status":"COMPLETED","conclusion":"FAILURE"},{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}`

	// Act
	proof, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(checks)})

	// Assert
	if err != nil || proof.Checks != 1 {
		t.Fatalf("proof=%+v err=%v, want the head proved by its one check that tested it", proof, err)
	}
}

func readyPRView(checks string) string {
	return `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefOid":"abc123","statusCheckRollup":[` + checks + `]}`
}

func TestVerifyPRReadyCountsARateLimitedDeployAsNotRunNeverAsFailed(t *testing.T) {
	const (
		passed  = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}`
		failed  = `{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE"}`
		skipped = `{"__typename":"CheckRun","name":"e2e","status":"COMPLETED","conclusion":"SKIPPED"}`
		limited = `{"__typename":"StatusContext","context":"Vercel","state":"FAILURE","description":"Deployment rate limited - retry in 24 hours"}`
		broken  = `{"__typename":"StatusContext","context":"Vercel","state":"FAILURE","description":"Deployment has failed"}`
	)
	cases := []struct {
		name   string
		checks string
		want   string
	}{
		{"a rate-limited deploy alone among passing checks passes", passed + "," + limited, ""},
		{"a rate-limited deploy beside a real failure does not", passed + "," + failed + "," + limited, "lint concluded FAILURE"},
		{"a failed deploy with another description does not", passed + "," + broken, "Vercel concluded FAILURE"},
		{"only rate-limited and skipped checks do not", skipped + "," + limited, "no check ran on the PR's head, every one was skipped or neutral, and the host rate limited the deploy of Vercel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			proof, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: readyPRView(tc.checks)})

			// Assert
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err=%v, want %q", err, tc.want)
				}
				return
			}
			if err != nil || proof.Checks != 1 || len(proof.RateLimited) != 1 || proof.RateLimited[0] != "Vercel" {
				t.Fatalf("proof=%+v err=%v, want one green check and Vercel's deploy named rate limited", proof, err)
			}
		})
	}
}
