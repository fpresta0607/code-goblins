package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A task with no container records lets host git run; a record that cannot
// be read, and a task whose record names no tasktmp folder, hold it back.
func TestRunningContainerRefusalReadsTheTasksRecords(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T) string
		want    string
	}{
		{"no record folder", func(t *testing.T) string { return t.TempDir() }, ""},
		{"an empty record folder", func(t *testing.T) string {
			taskTmp := t.TempDir()
			if err := os.MkdirAll(filepath.Join(taskTmp, RunsDir), 0o755); err != nil {
				t.Fatal(err)
			}
			return taskTmp
		}, ""},
		{"a record being written", func(t *testing.T) string {
			taskTmp := t.TempDir()
			writeRecord(t, taskTmp, ".goblins-tmp-123", "")
			return taskTmp
		}, ""},
		{"a record that cannot be read", func(t *testing.T) string {
			taskTmp := t.TempDir()
			writeRecord(t, taskTmp, "ceo-sandbox-x-1", "{")
			return taskTmp
		}, "ceo-sandbox-x-1 of this task may still run, and its record"},
		{"no tasktmp folder", func(t *testing.T) string { return "" }, "names no tasktmp folder"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			taskTmp := test.arrange(t)

			// Act
			err := RunningContainerRefusal(taskTmp)

			// Assert
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("RunningContainerRefusal = %v, want %q", err, test.want)
			}
		})
	}
}

func writeRecord(t *testing.T, taskTmp, name, text string) {
	t.Helper()
	runs := filepath.Join(taskTmp, RunsDir)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
