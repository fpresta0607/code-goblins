package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestLifecycleReadsPreserveMissingAndReadErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		id      string
		content string
		isDir   bool
	}{
		{name: "missing", id: "task"},
		{name: "invalid identity", id: "../task"},
		{name: "malformed", id: "task", content: "{"},
		{name: "mismatched identity", id: "task", content: `{"id":"other"}`},
		{name: "invalid record", id: "task", content: `{"id":"task"}`},
		{name: "directory", id: "task", isDir: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			service := &Service{Store: store}
			path := state.LifecyclePath(h.State, test.id)
			if test.content != "" || test.isDir {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if test.isDir {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				end := service.reads.begin(filepath.Dir(path))
				want, wantErr := state.ReadLifecycle(h.State, test.id)
				got, err := service.lifecycle(test.id)
				end()
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(err, wantErr) {
					t.Fatalf("lifecycle reader changed its result: got=%+v %v, want=%+v %v", got, err, want, wantErr)
				}
			}
		})
	}
}

func TestLifecycleMissingReadTracksParentAcrossSnapshots(t *testing.T) {
	store, h := testStore(t)
	service := &Service{Store: store}
	path := state.LifecyclePath(h.State, "task")
	for _, hasParent := range []bool{false, true, false} {
		if hasParent {
			if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(filepath.Dir(path)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		_, wantErr := state.ReadLifecycle(h.State, "task")
		end := service.reads.begin(filepath.Dir(path))
		_, err := service.lifecycle("task")
		if !reflect.DeepEqual(err, wantErr) {
			t.Fatalf("missing lifecycle error with parent=%t: got=%v, want=%v", hasParent, err, wantErr)
		}
		before := fsx.Opens()
		_, err = service.lifecycle("task")
		end()
		if !reflect.DeepEqual(err, wantErr) || fsx.Opens() != before {
			t.Fatalf("unchanged absence opened a file or changed its error: opens=%d error=%v want=%v", fsx.Opens()-before, err, wantErr)
		}
	}
}

func TestLifecycleRecordAppearsInTheNextSnapshot(t *testing.T) {
	store, h := testStore(t)
	service := &Service{Store: store}
	if _, err := service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	record := state.Lifecycle{ID: "task-1", Generation: "g1", Operation: "pause", Action: "pause", Phase: "paused"}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == record.ID })
	if index < 0 || snapshot.Tasks[index].Lifecycle == nil || snapshot.Tasks[index].Phase != "paused" || snapshot.Tasks[index].Generation != record.Generation {
		t.Fatalf("new snapshot kept a missing lifecycle: %+v", snapshot.Tasks)
	}
	before := fsx.Opens()
	got, err := service.lifecycle(record.ID)
	if err != nil || !reflect.DeepEqual(got, record) || fsx.Opens()-before != 1 {
		t.Fatalf("present lifecycle was not freshly read: opens=%d record=%+v error=%v", fsx.Opens()-before, got, err)
	}
}

func TestLifecycleReadKeepsErrorsWhenARecordAppearsDuringSnapshot(t *testing.T) {
	store, h := testStore(t)
	service := &Service{Store: store}
	path := state.LifecyclePath(h.State, "task")
	end := service.reads.begin(filepath.Dir(path))
	defer end()
	if _, err := service.reads.look(path); !os.IsNotExist(err) {
		t.Fatalf("fixture lifecycle already exists: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"id":"task","operation":"pause","action":"unknown","phase":"paused"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want, wantErr := state.ReadLifecycle(h.State, "task")
	got, err := service.lifecycle("task")
	if want.ID != "task" || wantErr == nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(err, wantErr) {
		t.Fatalf("appearing invalid record lost its result: got=%+v %v, want=%+v %v", got, err, want, wantErr)
	}
}
