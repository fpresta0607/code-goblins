package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBoardKeepsUpdatingWhenNativeInboxDisappearsOrIsUnreadable(t *testing.T) {
	for _, isUnreadable := range []bool{false, true} {
		name := "deleted"
		if isUnreadable {
			name = "unreadable"
		}
		t.Run(name, func(t *testing.T) {
			store, home := testStore(t)
			service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
			handler := NewHTTP(service, "board.local", nil)
			read := func() Snapshot {
				t.Helper()
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/snapshot", nil))
				var snapshot Snapshot
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
					t.Fatalf("board update=%d %s", response.Code, response.Body)
				}
				return snapshot
			}
			if snapshot := read(); snapshot.Error != "" {
				t.Fatalf("healthy scratch home: %s", snapshot.Error)
			}
			directory := nativehook.SpoolDir(home.State)
			if err := os.Rename(directory, directory+".held"); err != nil {
				t.Fatal(err)
			}
			if isUnreadable {
				if err := os.WriteFile(directory, []byte("folder is unavailable"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := read()
			if !strings.Contains(snapshot.Error, "native-inbox") || snapshot.Instance != service.Instance {
				t.Fatalf("folder problem or serve identity missing: %+v", snapshot)
			}
			if !isUnreadable {
				if info, err := os.Stat(directory); err != nil || !info.IsDir() || !strings.Contains(snapshot.Error, "recreated") {
					t.Fatalf("deleted folder not recreated: %v, %s", err, snapshot.Error)
				}
			}
			meta, err := state.ReadTaskMeta(home.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Title = "new evidence while the folder is unavailable"
			if err := state.WriteTaskMeta(home.State, meta); err != nil {
				t.Fatal(err)
			}
			service.notify()
			updated := read()
			if updated.Revision <= snapshot.Revision || !slices.ContainsFunc(updated.Tasks, func(task Task) bool { return task.Title == meta.Title }) {
				t.Fatalf("board stopped receiving fresh evidence: %+v", updated)
			}
			if err := os.Remove(directory); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(directory+".held", directory); err != nil {
				t.Fatal(err)
			}
			if snapshot := read(); snapshot.Error != "" {
				t.Fatalf("folder stayed broken after recovery: %s", snapshot.Error)
			}
		})
	}
}

func TestNativeInboxFailureDoesNotStopIndependentReconciliation(t *testing.T) {
	store, home := testStore(t)
	directory := nativehook.SpoolDir(home.State)
	if err := nativehook.Spool(home.State, event(t, home, "SessionStart", "saved-session", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(directory, directory+".held"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("folder is unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	isReconciled := false
	service := &Service{Store: store, Options: Options{Reconcile: func(context.Context) error { isReconciled = true; return nil }}}
	service.cycle(context.Background(), true)
	if !isReconciled || !strings.Contains(service.lastError, "native-inbox") {
		t.Fatalf("reconciled=%t board error=%q", isReconciled, service.lastError)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(directory+".held", directory); err != nil {
		t.Fatal(err)
	}
	service.cycle(context.Background(), true)
	if service.lastError != "" || store.Snapshot().Sessions["codex/saved-session"].NativeID != "saved-session" {
		t.Fatalf("events did not recover: error=%q sessions=%+v", service.lastError, store.Snapshot().Sessions)
	}
}

func TestIngestionRecreatesADeletedNativeInbox(t *testing.T) {
	store, home := testStore(t)
	if err := os.Remove(nativehook.SpoolDir(home.State)); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(); err == nil || !strings.Contains(err.Error(), "recreated") {
		t.Fatalf("missing folder was not reported and recreated: %v", err)
	}
	if err := nativehook.Spool(home.State, event(t, home, "SessionStart", "next-session", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(); err != nil || store.Snapshot().Sessions["codex/next-session"].NativeID != "next-session" {
		t.Fatalf("new events require a restart: %v", err)
	}
}

func TestBoardDoesNotHideAnUnrelatedFolderFailure(t *testing.T) {
	store, home := testStore(t)
	if err := os.Rename(home.State, home.State+".held"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.State, []byte("state root is unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/snapshot", nil))
	if response.Code != 503 {
		t.Fatalf("unrelated state failure was hidden: %d %s", response.Code, response.Body)
	}
}

func TestBoardNamesAPermissionFailureOnlyForTheNativeInbox(t *testing.T) {
	for _, directory := range []string{"native-inbox", "questions"} {
		t.Run(directory, func(t *testing.T) {
			failure := &os.PathError{Op: "open", Path: filepath.Join(t.TempDir(), directory), Err: os.ErrPermission}
			isNativeInbox := isNativeInboxReadFailure(filepath.Dir(failure.Path), failure)
			if isNativeInbox != (directory == "native-inbox") || isNativeInbox && !errors.Is(recoverNativeInbox(filepath.Dir(failure.Path), failure), os.ErrPermission) {
				t.Fatalf("permission failure classification changed: %t %v", isNativeInbox, failure)
			}
		})
	}
}

func TestBoardStreamStaysOpenAcrossAnUnreadableNativeInbox(t *testing.T) {
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
	handler := NewHTTP(service, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	read := func() Snapshot {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("board stream closed: %v", err)
			}
			if strings.HasPrefix(line, "data: ") {
				var snapshot Snapshot
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot); err != nil {
					t.Fatal(err)
				}
				return snapshot
			}
		}
	}
	initial := read()
	directory := nativehook.SpoolDir(home.State)
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("folder is unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	service.notify()
	problem := read()
	if problem.Revision <= initial.Revision || !strings.Contains(problem.Error, "native-inbox") {
		t.Fatalf("stream omitted folder problem: %+v", problem)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	service.notify()
	recovered := read()
	if recovered.Error != "" || recovered.Revision <= problem.Revision || recovered.Instance != initial.Instance {
		t.Fatalf("stream did not recover in place: %+v", recovered)
	}
}
