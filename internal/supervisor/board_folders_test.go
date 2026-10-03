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
	"github.com/fpresta0607/code-goblins/internal/tickets"
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
			service.notify()
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
			service.notify()
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

func TestBoardsAndTicketsShareAnUnreadableInboxSnapshot(t *testing.T) {
	store, home := testStore(t)
	service := &Service{Store: store}
	blockNativeInbox(t, home.State)
	builds := 0
	service.buildSnapshot = func() (Snapshot, error) {
		builds++
		return service.buildBoardSnapshot()
	}
	handler := NewHTTP(service, "board.local", nil)

	for range 3 {
		if snapshot := readBoard(t, handler); !strings.Contains(snapshot.Error, "native-inbox cannot be read") {
			t.Fatalf("board omitted the folder warning: %q", snapshot.Error)
		}
	}
	if snapshot, err := service.boardSnapshot(); err != nil || !strings.Contains(snapshot.Error, "native-inbox cannot be read") {
		t.Fatalf("ticket snapshot lost the folder warning: %+v %v", snapshot, err)
	}
	if builds != 1 {
		t.Fatalf("boards and tickets built %d snapshots for one revision, want one", builds)
	}
}

func blockNativeInbox(t *testing.T, stateDir string) {
	t.Helper()
	directory := nativehook.SpoolDir(stateDir)
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("folder is unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBoardShowsAnUnreadableNativeInboxOnceAfterACycle(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
	handler := NewHTTP(service, "board.local", nil)
	blockNativeInbox(t, home.State)

	// Act
	service.cycle(context.Background(), false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/snapshot", nil))

	// Assert
	var snapshot Snapshot
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("board update=%d %s", response.Code, response.Body)
	}
	if count := strings.Count(snapshot.Error, "native-inbox cannot be read"); count != 1 {
		t.Fatalf("folder problem shown %d times: %q", count, snapshot.Error)
	}
}

func TestOrdinaryCycleClearsTheInboxWarningOnceAnEmptyInboxIsReadableAgain(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
	handler := NewHTTP(service, "board.local", nil)
	blockNativeInbox(t, home.State)
	service.cycle(context.Background(), false)
	if !strings.Contains(service.lastError, "native-inbox") {
		t.Fatalf("folder problem not published: %q", service.lastError)
	}
	directory := nativehook.SpoolDir(home.State)
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}

	// Act
	service.cycle(context.Background(), false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/snapshot", nil))

	// Assert
	var snapshot Snapshot
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("board update=%d %s", response.Code, response.Body)
	}
	if service.lastError != "" || snapshot.Error != "" {
		t.Fatalf("stale folder warning: cycle=%q board=%q", service.lastError, snapshot.Error)
	}
}

func TestDeletedStateRootIsNeitherRecreatedNorTreatedAsAnInboxProblem(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	if err := os.Rename(home.State, home.State+".held"); err != nil {
		t.Fatal(err)
	}
	isReconciled := false
	service := &Service{Store: store, Options: Options{Reconcile: func(context.Context) error { isReconciled = true; return nil }}}

	// Act
	ingestErr := store.Ingest()
	service.cycle(context.Background(), true)

	// Assert
	if _, err := os.Stat(home.State); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state root was recreated: %v", err)
	}
	if ingestErr == nil || strings.Contains(ingestErr.Error(), "State folder native-inbox") {
		t.Fatalf("state root failure reported as an inbox problem: %v", ingestErr)
	}
	if isReconciled || service.lastError == "" || strings.Contains(service.lastError, "State folder native-inbox") {
		t.Fatalf("state root failure did not stop the cycle: reconciled=%t error=%q", isReconciled, service.lastError)
	}
}

func TestTicketsKeepMovingWhileTheNativeInboxIsUnreadable(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	blockNativeInbox(t, h.State)
	github := &fakeTicketWriter{collaboration: tickets.Collaboration{Repository: ticketRepository, IsCollaborative: true, IsPrivate: true}}
	service := &Service{Store: store, tickets: newTicketKeeper(h, github.writer(filepath.Join(h.Root, "work")))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Act
	go func() {
		defer close(done)
		service.keepTickets(ctx, time.Hour, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(github.appliedSoFar()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	// Assert
	applied := github.appliedSoFar()
	if len(applied) != 1 || applied[0].ticket.TaskID != "task-1" {
		t.Fatalf("applied = %+v, want task-1's ticket written while the inbox is unreadable", applied)
	}
}

func readBoard(t *testing.T, handler *HTTP) Snapshot {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/snapshot", nil))
	var snapshot Snapshot
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("board update=%d %s", response.Code, response.Body)
	}
	return snapshot
}

func repairInboxFromTheTicketLoop(t *testing.T, service *Service, github *fakeTicketWriter) {
	t.Helper()
	if err := os.Remove(nativehook.SpoolDir(service.Store.Home.State)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.keepTickets(ctx, time.Hour, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(github.appliedSoFar()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if info, err := os.Stat(nativehook.SpoolDir(service.Store.Home.State)); err != nil || !info.IsDir() {
		t.Fatalf("ticket loop did not recreate the inbox: %v", err)
	}
}

func TestBoardNamesAnInboxRepairTheTicketLoopMade(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	github := &fakeTicketWriter{collaboration: tickets.Collaboration{Repository: ticketRepository, IsCollaborative: true, IsPrivate: true}}
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}, tickets: newTicketKeeper(h, github.writer(filepath.Join(h.Root, "work")))}
	handler := NewHTTP(service, "board.local", nil)

	// Act
	repairInboxFromTheTicketLoop(t, service, github)
	service.cycle(context.Background(), false)
	afterRepairCycle := readBoard(t, handler)
	service.cycle(context.Background(), false)
	afterOrdinaryCycle := readBoard(t, handler)

	// Assert
	for _, snapshot := range []Snapshot{afterRepairCycle, afterOrdinaryCycle} {
		if count := strings.Count(snapshot.Error, "native-inbox was missing and has been recreated"); count != 1 {
			t.Fatalf("repair named %d times: %q", count, snapshot.Error)
		}
	}
}

func TestNextOrdinaryCycleKeepsTheInboxRepairNotice(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
	handler := NewHTTP(service, "board.local", nil)
	if err := os.Remove(nativehook.SpoolDir(home.State)); err != nil {
		t.Fatal(err)
	}
	service.cycle(context.Background(), false)

	// Act
	service.cycle(context.Background(), false)
	snapshot := readBoard(t, handler)

	// Assert
	if count := strings.Count(snapshot.Error, "native-inbox was missing and has been recreated"); count != 1 {
		t.Fatalf("repair notice after an ordinary cycle shown %d times: %q", count, snapshot.Error)
	}
}

func TestInboxRepairNoticeKeepsAnUnrelatedBoardError(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	github := &fakeTicketWriter{collaboration: tickets.Collaboration{Repository: ticketRepository, IsCollaborative: true, IsPrivate: true}}
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}, tickets: newTicketKeeper(h, github.writer(filepath.Join(h.Root, "work"))), Options: Options{Reconcile: func(context.Context) error { return errors.New("unrelated board problem") }}}
	handler := NewHTTP(service, "board.local", nil)

	// Act
	repairInboxFromTheTicketLoop(t, service, github)
	service.cycle(context.Background(), true)
	snapshot := readBoard(t, handler)

	// Assert
	if !strings.Contains(snapshot.Error, "unrelated board problem") || !strings.Contains(snapshot.Error, "native-inbox was missing and has been recreated") {
		t.Fatalf("board error = %q, want the unrelated problem and the repair", snapshot.Error)
	}
}

func TestUnchangedUnreadableInboxIsNotRepublishedEveryCycle(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
	handler := NewHTTP(service, "board.local", nil)
	blockNativeInbox(t, home.State)
	service.cycle(context.Background(), false)
	first := readBoard(t, handler)

	// Act
	for range 3 {
		service.cycle(context.Background(), false)
	}
	repeated := readBoard(t, handler)
	meta, err := state.ReadTaskMeta(home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Title = "new evidence while the warning stands"
	if err := state.WriteTaskMeta(home.State, meta); err != nil {
		t.Fatal(err)
	}
	service.notify()
	fresh := readBoard(t, handler)
	directory := nativehook.SpoolDir(home.State)
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	service.cycle(context.Background(), false)
	restored := readBoard(t, handler)

	// Assert
	if strings.Count(first.Error, "native-inbox cannot be read") != 1 {
		t.Fatalf("first cycle did not show the folder problem once: %q", first.Error)
	}
	if repeated.Revision != first.Revision || repeated.Error != first.Error {
		t.Fatalf("unchanged warning republished: revision %d -> %d, %q", first.Revision, repeated.Revision, repeated.Error)
	}
	if fresh.Revision <= repeated.Revision || !slices.ContainsFunc(fresh.Tasks, func(task Task) bool { return task.Title == meta.Title }) || strings.Count(fresh.Error, "native-inbox cannot be read") != 1 {
		t.Fatalf("fresh evidence or warning missing: %+v", fresh)
	}
	if restored.Revision <= fresh.Revision || restored.Error != "" {
		t.Fatalf("restored empty inbox did not clear the warning: revision %d, %q", restored.Revision, restored.Error)
	}
}

func TestRacingInboxRepairsReportOneRepairAndNoUnreadableFolder(t *testing.T) {
	// Arrange
	_, home := testStore(t)
	directory := nativehook.SpoolDir(home.State)
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	_, stale := os.ReadDir(directory)
	results := make(chan error, 8)

	// Act
	for range cap(results) {
		go func() { results <- recoverNativeInbox(home.State, stale) }()
	}

	// Assert
	repairs := 0
	for range cap(results) {
		result := <-results
		if errors.Is(result, errNativeInboxRecreated) {
			repairs++
		} else if result != nil {
			t.Fatalf("racing repair reported a readable inbox as a problem: %v", result)
		}
	}
	if info, err := os.Stat(directory); repairs != 1 || err != nil || !info.IsDir() {
		t.Fatalf("repairs=%d folder=%v, want one repair of a usable folder", repairs, err)
	}
}

func TestStaleRepairStillReportsAnObstructingFile(t *testing.T) {
	// Arrange
	_, home := testStore(t)
	directory := nativehook.SpoolDir(home.State)
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	_, stale := os.ReadDir(directory)
	if err := os.WriteFile(directory, []byte("folder is unavailable"), 0600); err != nil {
		t.Fatal(err)
	}

	// Act
	result := recoverNativeInbox(home.State, stale)

	// Assert
	if result == nil || !strings.Contains(result.Error(), "native-inbox cannot be read") {
		t.Fatalf("obstruction not reported: %v", result)
	}
	if data, err := os.ReadFile(directory); err != nil || string(data) != "folder is unavailable" {
		t.Fatalf("obstruction was changed: %q %v", data, err)
	}
}
