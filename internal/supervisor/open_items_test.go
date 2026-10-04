package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenItemsReadsOnlyWhatWaitsWithoutChangingTheStore(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, ".supervisor.json")
	data, err := json.Marshal(Database{
		Schema:      1,
		Questions:   []Question{{ID: "question-open", Text: "Which plan?", Status: "pending"}, {ID: "question-done", Status: "succeeded"}},
		Reviews:     []Review{{ID: "review-open", Task: "task-1", Title: "Read the plan", State: "open"}, {ID: "review-done", State: "answered"}},
		Runs:        []Run{{ID: "run-ready", Title: "Install the build", State: "ready"}, {ID: "run-done", State: "succeeded"}},
		Credentials: []CredentialRequest{{ID: "credential-open", Task: "task-2", Project: "example", Names: []string{"DATABASE_URL"}, Why: "Start the database", State: "open"}, {ID: "credential-done", State: "closed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	items, err := OpenItems(directory)

	if err != nil {
		t.Fatal(err)
	}
	want := []OpenItem{
		{Kind: "question", ID: "question-open", Text: "Which plan?"},
		{Kind: "review", ID: "review-open", Task: "task-1", Text: "Read the plan"},
		{Kind: "run", ID: "run-ready", Text: "Install the build"},
		{Kind: "credential", ID: "credential-open", Task: "task-2", Text: "Credentials for example (DATABASE_URL): Start the database"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("open items = %#v, want %#v", items, want)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("reading the open items changed the database: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("reading the open items created files: %v, %v", entries, err)
	}
}

func TestOpenItemsReportsInvalidStateAndTreatsAMissingStoreAsEmpty(t *testing.T) {
	for _, test := range []struct {
		name       string
		data       string
		shouldFail bool
	}{
		{name: "missing"},
		{name: "corrupt", data: "{broken", shouldFail: true},
		{name: "unknown schema", data: `{"schema":2}`, shouldFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if test.data != "" {
				if err := os.WriteFile(filepath.Join(directory, ".supervisor.json"), []byte(test.data), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			items, err := OpenItems(directory)

			if (err != nil) != test.shouldFail || len(items) != 0 {
				t.Errorf("open items = %v, error = %v", items, err)
			}
		})
	}
}
