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

func readyPRView(checks string) string {
	return `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefOid":"abc123","statusCheckRollup":[` + checks + `]}`
}
