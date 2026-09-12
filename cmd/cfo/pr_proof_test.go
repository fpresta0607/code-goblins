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

func (r proofRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if req.Name != "gh" || len(req.Args) < 2 || req.Args[0] != "pr" || req.Args[1] != "view" {
		return execx.Result{}, errors.New("unexpected command")
	}
	return execx.Result{ExitCode: r.exit, Stdout: []byte(r.stdout), Stderr: []byte("gh failed")}, r.err
}

func TestVerifyPRReadyAcceptsOnlyGreenTerminalEvidence(t *testing.T) {
	r := proofRunner{stdout: `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefOid":"abc123","statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"},{"name":"lint","status":"COMPLETED","conclusion":"NEUTRAL"}]}`}
	proof, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", r)
	if err != nil {
		t.Fatal(err)
	}
	if proof.HeadRefOID != "abc123" || proof.Checks != 2 {
		t.Fatalf("proof=%+v", proof)
	}
}

func TestVerifyPRReadyRefusesPendingOrFailedChecks(t *testing.T) {
	cases := []struct {
		name  string
		check string
		want  string
	}{
		{"pending", `{"name":"test","status":"IN_PROGRESS","conclusion":""}`, "not completed"},
		{"failed", `{"name":"test","status":"COMPLETED","conclusion":"FAILURE"}`, "concluded FAILURE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := proofRunner{stdout: `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefOid":"abc123","statusCheckRollup":[` + tc.check + `]}`}
			_, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", r)
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
		`{"state":"OPEN","isDraft":true,"mergeable":"MERGEABLE","headRefOid":"abc","statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
		`{"state":"OPEN","isDraft":false,"mergeable":"CONFLICTING","headRefOid":"abc","statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
		`{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"CHANGES_REQUESTED","headRefOid":"abc","statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]}`,
	}
	for _, view := range views {
		if _, err := verifyPRReady(context.Background(), "https://github.com/o/r/pull/1", proofRunner{stdout: view}); err == nil {
			t.Fatalf("expected refusal for %s", view)
		}
	}
}
