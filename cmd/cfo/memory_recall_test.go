package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/memory"
)

// writeRecallHome is a home with one ruling and two facts, one superseding
// the other.
func writeRecallHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"data/overlord.md":                    "## Harbor deploys, 2026-11-05\n\nMerging to main deploys the harbor site to production.\n",
		"data/memory/harbor-no-database.md":   "---\nname: harbor-no-database\ndescription: harbor has no hosted database\nmetadata:\n  modified: 2026-10-01T00:00:00Z\n---\nNo hosted database.\n",
		"data/memory/harbor-database-live.md": "---\nname: harbor-database-live\ndescription: harbor's hosted database is live\nmetadata:\n  modified: 2026-11-05T00:00:00Z\n  supersedes: [harbor-no-database]\n  projects: [harbor]\n---\nThe hosted database is live.\n",
		"state/.keep":                         "",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestMemoryRecallRanksItemsFromAnIndexBuiltHere(t *testing.T) {
	root := writeRecallHome(t)
	data := filepath.Join(root, "data")
	for _, args := range [][]string{
		{"recall", "is the harbor database hosted", "--data", data, "--project", "harbor"},
		{"recall", "--data", data, "--project", "harbor", "is the harbor database hosted"},
	} {
		var output, diagnostics bytes.Buffer

		exit := runMemory(args, &output, &diagnostics)

		text := output.String()
		if exit != 0 || !strings.Contains(text, "1. harbor-database-live  fact  2026-11-05  ") || strings.Contains(text, "harbor-no-database  fact") || !strings.Contains(text, "1 superseded or archived matches hidden") || !strings.Contains(text, "index built in this process, built ") {
			t.Fatalf("%v: %d %s %s", args, exit, text, diagnostics.String())
		}
	}
	var history bytes.Buffer
	if exit := runMemory([]string{"recall", "harbor hosted database", "--data", data, "--history"}, &history, new(bytes.Buffer)); exit != 0 || !strings.Contains(history.String(), "harbor-no-database  fact  2026-10-01") || !strings.Contains(history.String(), "superseded") {
		t.Fatalf("history: %d %s", exit, history.String())
	}
	var bounded bytes.Buffer
	if exit := runMemory([]string{"recall", "harbor hosted database", "--data", data, "--history", "--until", "2026-10-01"}, &bounded, new(bytes.Buffer)); exit != 0 || strings.Contains(bounded.String(), "harbor-database-live") || !strings.Contains(bounded.String(), "harbor-no-database") {
		t.Fatalf("until: %d %s", exit, bounded.String())
	}
}

func TestMemoryRecallWithNoSupervisorBuildsItsOwnIndexAndSaysSo(t *testing.T) {
	root := writeRecallHome(t)
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	var output, diagnostics bytes.Buffer

	exit := runMemory([]string{"recall", "what does merging to main deploy"}, &output, &diagnostics)

	if exit != 0 || !strings.Contains(output.String(), "index built in this process; the supervisor is not running") || !strings.Contains(output.String(), "1. ruling-") {
		t.Fatalf("%d %s %s", exit, output.String(), diagnostics.String())
	}
}

func TestMemoryRecallEvaluatesAQuestionSet(t *testing.T) {
	root := writeRecallHome(t)
	data := filepath.Join(root, "data")
	cases := filepath.Join(root, "cases.json")
	encoded, err := json.Marshal([]memory.EvalCase{
		{Question: "is the harbor database hosted", Expect: []string{"harbor-database-live"}},
		{Question: "what does merging deploy", Expect: []string{"text:deploys the harbor site"}},
		{Question: "unicorn stables", Expect: []string{"harbor-database-live"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cases, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer

	exit := runMemory([]string{"recall", "--eval", cases, "--data", data}, &output, &diagnostics)

	var report memory.EvalReport
	if exit != 0 || json.Unmarshal(output.Bytes(), &report) != nil || report.Questions != 3 || report.HitsAtDepth != 2 || report.Depth != 5 || len(report.Misses) != 1 {
		t.Fatalf("%d %s %s", exit, output.String(), diagnostics.String())
	}
}

func TestMemoryRecallRefusesWhatItCannotAsk(t *testing.T) {
	root := writeRecallHome(t)
	data := filepath.Join(root, "data")
	for name, args := range map[string][]string{
		"no question":             {"recall", "--data", data},
		"two questions":           {"recall", "one", "--data", data, "two"},
		"question and evaluation": {"recall", "one", "--eval", "cases.json", "--data", data},
		"since is not a day":      {"recall", "harbor", "--since", "yesterday", "--data", data},
		"until is not a day":      {"recall", "harbor", "--until", "2026-13-01", "--data", data},
		"zero limit":              {"recall", "harbor", "--limit", "0", "--data", data},
		"only stop words":         {"recall", "what is the", "--data", data},
		"missing evaluation set":  {"recall", "--eval", filepath.Join(root, "absent.json"), "--data", data},
		"unknown flag":            {"recall", "harbor", "--bogus", "--data", data},
	} {
		var output, diagnostics bytes.Buffer
		if exit := runMemory(args, &output, &diagnostics); exit == 0 || output.Len() != 0 || diagnostics.Len() == 0 {
			t.Errorf("%s: exit %d output %q", name, exit, output.String())
		}
	}
}
