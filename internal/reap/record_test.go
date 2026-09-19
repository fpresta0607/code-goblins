package reap

import (
	"os"
	"strings"
	"testing"
)

// TestRenderCollapsesOrphanStatusLogs: a long-lived home accumulates hundreds
// of finished tasks' status logs, and listing every one would bury the one
// unsupervised harness that actually costs something.
func TestRenderCollapsesOrphanStatusLogs(t *testing.T) {
	findings := []Finding{{Class: OrphanProcess, PID: 31032, Detail: "no pane", Action: "kill the process tree"}}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		findings = append(findings, Finding{Class: OrphanStatus, TaskID: id, Detail: "status log with no matching meta", Action: "archive the log"})
	}
	findings = append(findings, Finding{Class: OrphanWorktree, TaskID: "z", Path: `C:\wt`, Detail: "no live pane", Action: "return the worktree through cfo cleanup"})
	SortFindings(findings)

	var out strings.Builder
	if err := Render(&out, Result{Findings: findings}); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if strings.Count(rendered, "status log with no matching meta") != statusListCap {
		t.Fatalf("listed status logs = %d, want %d:\n%s", strings.Count(rendered, "status log with no matching meta"), statusListCap, rendered)
	}
	if !strings.Contains(rendered, "(+2 more orphan status logs") {
		t.Fatalf("the overflow line is missing:\n%s", rendered)
	}
	// The classes that matter must still be listed in full.
	if !strings.Contains(rendered, "pid=31032") || !strings.Contains(rendered, `orphan_worktree z`) {
		t.Fatalf("a collapsed listing hid a class that matters:\n%s", rendered)
	}
	// The overflow line belongs with the logs it summarizes, not at the end.
	if strings.Index(rendered, "(+2 more") > strings.Index(rendered, "orphan_worktree z") {
		t.Fatalf("the overflow line drifted away from its class:\n%s", rendered)
	}
}

// TestAPreviousSchemaIsRefusedRatherThanMisread is the danger in dropping a
// field that was persisted: a v1 record carries the rendered hold and no
// refusals, so under v2 rules it would decode into findings with nothing
// holding them, and the session-start digest would tell the operator the sweep
// will kill processes it had in fact refused to touch. Saying the audit cannot
// be read is true; rendering it as actionable is not.
func TestAPreviousSchemaIsRefusedRatherThanMisread(t *testing.T) {
	h := testHome(t)
	v1 := `{"schema":"cfo-reap.v1","time":"2026-09-19T16:00:00Z","digest":"abc","findings":[` +
		`{"class":"orphan_process","pid":31032,"detail":"claude.exe has no pane","action":"kill the process tree",` +
		`"hold":"busy: burned 2.1s of processor time in 3s, which is a process doing work"}]}`
	if err := os.WriteFile(RecordPath(h.State), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadRecord(h.State); err == nil {
		t.Fatal("a record from the previous schema was accepted, so its held findings would render as actionable")
	}

	var out strings.Builder
	if err := RenderRecord(&out, h.State, fixtureStart); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "UNREADABLE") {
		t.Fatalf("rendered %q, want the digest to say the audit could not be read", rendered)
	}
	if strings.Contains(rendered, "would kill the process tree") {
		t.Fatalf("rendered %q, want no claim that the sweep would act on a finding the last sweep held", rendered)
	}
}
