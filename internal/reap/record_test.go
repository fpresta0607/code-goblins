package reap

import (
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
