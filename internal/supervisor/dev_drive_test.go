package supervisor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// devDriveMachine is a machine that can have a Dev Drive and has none, C: its
// only drive letter.
func devDriveMachine() devdrive.Machine {
	return devdrive.Machine{Build: 26200, Revision: 9457, Defender: true, SystemDrive: "C:", SystemFree: 374 << 30, Letters: 1 << 2,
		Volumes: []devdrive.Volume{{Root: `C:\`, FileSystem: "NTFS", Free: 374 << 30, Total: 924 << 30}}}
}

// devDriveService is a board that reads *machine for its Dev Drive.
func devDriveService(t *testing.T, machine *devdrive.Machine) *Service {
	t.Helper()
	store, _ := testStore(t)
	return &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1), devDriveNow: make(chan struct{}, 1),
		Options: Options{DevDrive: &DevDrive{Read: func(context.Context) (devdrive.Machine, error) { return *machine, nil }}}}
}

func devDriveItems(s *Service) []Run {
	var items []Run
	for _, r := range s.Store.Snapshot().Runs {
		if r.DevDrive != "" {
			items = append(items, r)
		}
	}
	return items
}

// endDevDriveItem ends the waiting Dev Drive item as its run would.
func endDevDriveItem(t *testing.T, s *Service, code int) {
	t.Helper()
	s.Store.mu.Lock()
	for i := range s.Store.db.Runs {
		if r := &s.Store.db.Runs[i]; r.DevDrive != "" && r.State == "ready" {
			now := time.Now().UTC()
			r.State, r.ExitCode, r.FinishedAt = "failed", &code, &now
			if code == 0 {
				r.State = "succeeded"
			}
		}
	}
	err := s.Store.save()
	s.Store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

// Nothing reaches the Command Center until the person asks for a Dev Drive:
// the panel offers Set up, with its one short note, and the first run's offer
// has the sentence saying what one is.
func TestNoDevDriveItemIsMadeUntilThePersonAsks(t *testing.T) {
	// Arrange
	machine := devDriveMachine()
	s := devDriveService(t, &machine)

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); len(items) != 0 {
		t.Fatalf("items made without an ask: %+v", items)
	}
	view := s.devDriveViewNow()
	if view == nil || view.State != "absent" || view.Action != devDriveSetUp || view.Note != "An optional drive that Defender scans in performance mode, so files open faster." || !strings.Contains(view.Explain, "not a Defender exclusion") {
		t.Errorf("view = %+v, want absent with Set up, its note and the explanation", view)
	}
}

// A machine that could not be read is said in the panel's note in plain
// words: the error is goblins doctor's to show, never the board's.
func TestAnUnreadableMachineIsOnePlainNote(t *testing.T) {
	// Arrange
	machine := devDriveMachine()
	s := devDriveService(t, &machine)
	s.Options.DevDrive.Read = func(context.Context) (devdrive.Machine, error) {
		return devdrive.Machine{}, errors.New("Get-Volume: exit status 1")
	}

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	if view := s.devDriveViewNow(); view == nil || view.State != "unreadable" || view.Note != "This machine's drives could not be read." || view.Action != "" {
		t.Errorf("view = %+v, want unreadable with its plain note and nothing to press", view)
	}
}

// A step that failed or expired waits for the person: the panel offers Try
// again, which makes its item once more.
func TestAFailedStepWaitsForTryAgain(t *testing.T) {
	// Arrange
	machine := devDriveMachine()
	s := devDriveService(t, &machine)
	if err := s.askDevDrive(true); err != nil {
		t.Fatal(err)
	}
	s.keepDevDrive(context.Background())
	endDevDriveItem(t, s, 1)

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); len(items) != 1 {
		t.Fatalf("a failed step was made again by itself: %+v", items)
	}
	if view := s.devDriveViewNow(); view.Action != devDriveTryAgain {
		t.Fatalf("view = %+v, want Try again", view)
	}

	// Act: he presses Try again.
	if err := s.askDevDrive(true); err != nil {
		t.Fatal(err)
	}
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); len(items) != 2 || items[1].DevDrive != "create" || items[1].State != "ready" {
		t.Errorf("items = %+v, want a second create item", items)
	}
}

// The panel's buttons and the first run's answer reach the home's config:
// wanted asks for the next step now, declined keeps the offer away.
func TestTheBoardKeepsTheAnswerToTheDevDriveOffer(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	machine := devDriveMachine()
	handler.Service.Options.DevDrive = &DevDrive{Read: func(context.Context) (devdrive.Machine, error) { return machine, nil }}
	post := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://board.local/api/dev-drive", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://board.local")
		request.Header.Set("X-CEO-Token", orderToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	// Act and assert
	if response := post(`{"want":false}`); response.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", response.Code, response.Body)
	}
	if config, err := home.ReadDevDriveConfig(h.Root); err != nil || config.Choice != home.DevDriveDeclined || !config.AskedAt.IsZero() {
		t.Fatalf("config = %+v, %v; want declined", config, err)
	}
	if response := post(`{"want":true}`); response.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", response.Code, response.Body)
	}
	if config, err := home.ReadDevDriveConfig(h.Root); err != nil || config.Choice != home.DevDriveWanted || config.AskedAt.IsZero() {
		t.Fatalf("config = %+v, %v; want wanted, asked now", config, err)
	}
	if response := post(`{}`); response.Code != http.StatusBadRequest {
		t.Errorf("an answer that says nothing = %d, want 400", response.Code)
	}
	handler.Service.Options.DevDrive = nil
	if response := post(`{"want":true}`); response.Code != http.StatusConflict {
		t.Errorf("a board without the setting = %d, want 409", response.Code)
	}
}

// An answer given outside the board, by goblins dev-drive setup or the install,
// reaches the watch at its next tick, not at its half-hourly look.
func TestTheWatchNoticesAnAnswerGivenElsewhere(t *testing.T) {
	// Arrange
	machine := devDriveMachine()
	s := devDriveService(t, &machine)
	s.devDriveTick = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.watchDevDrive(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for deadline := time.Now().Add(time.Minute); s.devDriveViewNow() == nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the watch made no first look")
		}
	}

	// Act
	if err := home.AnswerDevDrive(s.Store.Home.Root, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Assert
	for deadline := time.Now().Add(time.Minute); len(devDriveItems(s)) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the answer given elsewhere made no item")
		}
	}
	if items := devDriveItems(s); items[0].DevDrive != "create" {
		t.Errorf("items = %+v, want the create item", items)
	}
}
