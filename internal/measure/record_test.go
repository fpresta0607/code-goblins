package measure

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A run's record is kept a line each and read back as it was written, none
// before the first is; a task's next run starts where its last recorded one
// ended.
func TestAppendKeepsEachRunForTheNextToStartFrom(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	first := Record{Task: "task", Project: "project", Outcome: "done", From: windowFrom, To: at(60), Runs: []string{"run"}, Roles: []Role{{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Calls: 1, Usage: Usage{InputTokens: 10}}}}
	other := Record{Task: "other", Outcome: "failed", From: windowFrom, To: at(90), Roles: []Role{}}
	second := Record{Task: "task", Outcome: "done", From: at(60), To: at(120), Roles: []Role{}, Unmeasured: []string{"lead: no conversation"}}
	before, err := Read(stateDir)
	if err != nil || before != nil {
		t.Fatalf("Read before any run = %v, %v; want none", before, err)
	}

	// Act
	for _, record := range []Record{first, other, second} {
		if err := Append(stateDir, record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := Read(stateDir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := []Record{first, other, second}; !reflect.DeepEqual(records, want) {
		t.Errorf("records =\n%+v\nwant\n%+v", records, want)
	}
	if since, isRecorded := Since(records, "task"); !isRecorded || !since.Equal(at(120)) {
		t.Errorf("Since(task) = %v, %v; want %v, true", since, isRecorded, at(120))
	}
	if since, isRecorded := Since(records, "unknown"); isRecorded {
		t.Errorf("Since(unknown) = %v, true; want none", since)
	}
}

// A line that is no record is refused by its number, never skipped, so the
// totals never pass over a run.
func TestReadRefusesALineThatIsNoRecord(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, FileName), []byte(`{"task":"task","roles":[]}`+"\n"+`{"task":`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := Read(stateDir)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "line 2 of "+FileName) {
		t.Errorf("Read error = %v; want one naming line 2", err)
	}
}

// Totals sum each role's rows over the runs that recorded them, by harness,
// model, effort and agent, in the role table's order.
func TestTotalsSumEachRolesRowsInTheRoleTablesOrder(t *testing.T) {
	// Arrange
	records := []Record{
		{Roles: []Role{
			{Role: RoleGate, Harness: "claude", Model: "claude-opus-5-5", Agent: "document", Calls: 1, WallSeconds: 3},
			{Role: RoleVerify, Harness: "kimi", Model: "kimi-k3", Calls: 1, Untallied: 1, Failed: 1},
			{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh", Calls: 1, Usage: Usage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 80}, WallSeconds: 60},
		}},
		{Roles: []Role{
			{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh", Calls: 1, Usage: Usage{InputTokens: 50, OutputTokens: 5, CacheReadTokens: 40}, WallSeconds: 30},
			{Role: RoleVerify, Harness: "kimi", Model: "kimi-k3", Calls: 2, Passed: 1},
			{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "high", Calls: 1},
		}},
	}

	// Act
	totals := Totals(records)

	// Assert
	want := []Total{
		{Role: Role{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "high", Calls: 1}, Runs: 1},
		{Role: Role{Role: RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh", Calls: 2, Usage: Usage{InputTokens: 150, OutputTokens: 15, CacheReadTokens: 120}, WallSeconds: 90}, Runs: 2},
		{Role: Role{Role: RoleVerify, Harness: "kimi", Model: "kimi-k3", Calls: 3, Untallied: 1, Passed: 1, Failed: 1}, Runs: 2},
		{Role: Role{Role: RoleGate, Harness: "claude", Model: "claude-opus-5-5", Agent: "document", Calls: 1, WallSeconds: 3}, Runs: 1},
	}
	if !reflect.DeepEqual(totals, want) {
		t.Errorf("totals =\n%+v\nwant\n%+v", totals, want)
	}
}
