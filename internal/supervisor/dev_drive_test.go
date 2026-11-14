package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
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

// The panel's note under the Dev Drive row says how much of the system drive
// the drive's file holds and that space goblins free stays taken there.
func TestTheNoteSaysHowMuchOfTheSystemDriveTheDevDriveFileHolds(t *testing.T) {
	// Arrange
	machine := devDriveMachine()
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted, Free: 188 << 30, Total: 200 << 30,
		Image: devdrive.Image{Path: `C:\DevDrives\CodeGoblins.vhdx`, Size: 142 << 30 / 10}})
	s := devDriveService(t, &machine)
	if err := home.WriteDevDriveConfig(s.Store.Home.Root, home.DevDriveConfig{Root: `D:\CodeGoblins`, VHD: `C:\DevDrives\CodeGoblins.vhdx`, MovedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	want := `The worktrees and caches are on D:\CodeGoblins. C:\DevDrives\CodeGoblins.vhdx holds 14.2 GB of C:, and stays that size when goblins free space on D:.`
	if view := s.devDriveViewNow(); view == nil || view.State != "on" || view.Note != want {
		t.Errorf("view = %+v, want on with the note %q", view, want)
	}
}

// Set up makes one item at a time, in order: the create step as
// administrator, then, once it ran and the drive is there, the move.
func TestSetUpMakesTheCreateItemThenTheMoveItem(t *testing.T) {
	// Arrange
	machine := devDriveMachine()
	s := devDriveService(t, &machine)
	if err := s.askDevDrive(true); err != nil {
		t.Fatal(err)
	}

	// Act
	s.keepDevDrive(context.Background())
	s.keepDevDrive(context.Background())

	// Assert: one create item, as administrator, its file recorded.
	items := devDriveItems(s)
	if len(items) != 1 || items[0].DevDrive != "create" || !items[0].Admin || items[0].State != "ready" || !strings.Contains(items[0].Title, "Create the Code Goblins Dev Drive D:") || !strings.Contains(items[0].Command, "Format-Volume -DevDrive") {
		t.Fatalf("items = %+v, want one create item as administrator", items)
	}
	config, err := home.ReadDevDriveConfig(s.Store.Home.Root)
	if err != nil || config.VHD != `C:\DevDrives\CodeGoblins.vhdx` || config.Offered["create"].IsZero() {
		t.Fatalf("config = %+v, %v; want the file and the offer recorded", config, err)
	}
	if view := s.devDriveViewNow(); view.Waiting != "create" || view.Action != "" {
		t.Errorf("view = %+v, want the create item waiting and nothing to press", view)
	}

	// Act: he runs it, and D: is a trusted Dev Drive.
	endDevDriveItem(t, s, 0)
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted, Free: 199 << 30})
	machine.Letters |= 1 << 3
	s.keepDevDrive(context.Background())

	// Assert: the move, without administrator, runs this build in the home.
	items = devDriveItems(s)
	move := items[len(items)-1]
	if len(items) != 2 || move.DevDrive != "move" || move.Admin || !strings.Contains(move.Command, "dev-drive move --to 'D:\\CodeGoblins'") || !strings.Contains(move.Command, "$env:GOBLINS_HOME = '"+s.Store.Home.Root+"'") {
		t.Fatalf("items = %+v, want the move item next", items)
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

// A Dev Drive Windows does not trust gets the trust item before the move,
// and a home whose drive went missing gets the attach item by itself.
func TestAnUntrustedOrMissingDriveGetsItsItem(t *testing.T) {
	t.Run("untrusted", func(t *testing.T) {
		machine := devDriveMachine()
		machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Untrusted})
		s := devDriveService(t, &machine)
		if err := s.askDevDrive(true); err != nil {
			t.Fatal(err)
		}
		s.keepDevDrive(context.Background())
		if items := devDriveItems(s); len(items) != 1 || items[0].DevDrive != "trust" || !items[0].Admin || !strings.Contains(items[0].Command, "fsutil devdrv trust E:") {
			t.Errorf("items = %+v, want the trust item for E:", items)
		}
	})
	t.Run("missing", func(t *testing.T) {
		machine := devDriveMachine()
		s := devDriveService(t, &machine)
		if err := home.WriteDevDriveConfig(s.Store.Home.Root, home.DevDriveConfig{Root: `D:\CodeGoblins`, VHD: `C:\DevDrives\CodeGoblins.vhdx`, MovedAt: time.Now().Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		s.keepDevDrive(context.Background())
		items := devDriveItems(s)
		if len(items) != 1 || items[0].DevDrive != "attach" || !items[0].Admin || !strings.Contains(items[0].Title, "Attach the Code Goblins Dev Drive D: again") {
			t.Fatalf("items = %+v, want the attach item", items)
		}
		endDevDriveItem(t, s, 1)
		s.keepDevDrive(context.Background())
		if items := devDriveItems(s); len(items) != 1 {
			t.Errorf("the attach item came back at once: %+v", items)
		}
		if view := s.devDriveViewNow(); view.State != "missing" || view.Action != devDriveAttach {
			t.Errorf("view = %+v, want missing with Attach", view)
		}
	})
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

// gateStep is a board whose heavy folders are on D:\CodeGoblins while the
// gate's home is still in the profile, with the gate runs in flight and the
// carries the supervisor started under the test's hand.
type gateStep struct {
	s        *Service
	gateHome string
	runs     int
	runsErr  error
	// started takes the folder of each carry the supervisor started, and
	// ended ends the one running with what it came to.
	started chan string
	ended   chan error
}

func newGateStep(t *testing.T, want bool) *gateStep {
	t.Helper()
	machine := devDriveMachine()
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted, Free: 199 << 30})
	machine.Letters |= 1 << 3
	g := &gateStep{s: devDriveService(t, &machine), gateHome: `C:\Users\someone\.no-mistakes`, started: make(chan string, 8), ended: make(chan error)}
	g.s.Options.DevDrive.GateHome = func() (string, error) { return g.gateHome, nil }
	g.s.Options.DevDrive.GateRuns = func(_ context.Context, root string) (int, error) {
		if root != g.gateHome {
			t.Errorf("gate runs read in %s, want %s", root, g.gateHome)
		}
		return g.runs, g.runsErr
	}
	g.s.Options.DevDrive.CarryGateHome = func(_ context.Context, folder string) error {
		g.started <- folder
		return <-g.ended
	}
	if want {
		if err := g.s.askDevDrive(true); err != nil {
			t.Fatal(err)
		}
		<-g.s.devDriveNow
	}
	config, err := home.ReadDevDriveConfig(g.s.Store.Home.Root)
	if err != nil {
		t.Fatal(err)
	}
	config.Root = `D:\CodeGoblins`
	if err := home.WriteDevDriveConfig(g.s.Store.Home.Root, config); err != nil {
		t.Fatal(err)
	}
	return g
}

// isCarrying says a carry the supervisor started runs now.
func (g *gateStep) isCarrying() bool {
	g.s.gateCarry.mu.Lock()
	defer g.s.gateCarry.mu.Unlock()
	return g.s.gateCarry.isRunning
}

// carriedInto is the folder of the carry the supervisor started.
func (g *gateStep) carriedInto(t *testing.T) string {
	t.Helper()
	select {
	case folder := <-g.started:
		return folder
	case <-time.After(time.Minute):
		t.Fatal("the supervisor started no carry")
		return ""
	}
}

// end ends the running carry with err and waits until the supervisor took
// it in, which asks the watch for another look.
func (g *gateStep) end(t *testing.T, err error) {
	t.Helper()
	g.ended <- err
	select {
	case <-g.s.devDriveNow:
	case <-time.After(time.Minute):
		t.Fatal("the supervisor never asked for another look after the carry ended")
	}
}

const gateStepDone = `The worktrees and caches are on D:\CodeGoblins. The gate's home is not there yet.`

// Once the person asked for a Dev Drive, the supervisor carries the gate's
// home itself: while a gate run is in flight it waits and says so in the row,
// at the first look that finds none it starts the carry, once, and the row
// says so; no Command Center item is made and there is nothing to press
// (#2525). Once the home is there the row goes.
func TestTheSupervisorCarriesTheGateHomeItselfOnceNoGateRunIsInFlight(t *testing.T) {
	// Arrange
	g := newGateStep(t, true)
	g.runs = 1

	// Act
	g.s.keepDevDrive(context.Background())

	// Assert
	if view := g.s.devDriveViewNow(); view.Action != "" || view.Waiting != "" || view.Status != gateStepDone+" Waiting for 1 gate run to finish, then the gate's home moves by itself." {
		t.Fatalf("view = %+v, want it waiting for the gate run with nothing to press", view)
	}
	if g.isCarrying() {
		t.Fatal("a carry started while a gate run was in flight")
	}

	// Act: two runs, then none, and a second look while the carry runs.
	g.runs = 2
	g.s.keepDevDrive(context.Background())
	waitingForTwo := g.s.devDriveViewNow().Status
	g.runs = 0
	g.s.keepDevDrive(context.Background())
	g.s.keepDevDrive(context.Background())

	// Assert
	if waitingForTwo != gateStepDone+" Waiting for 2 gate runs to finish, then the gate's home moves by itself." {
		t.Errorf("status = %q, want it waiting for 2 gate runs", waitingForTwo)
	}
	if view := g.s.devDriveViewNow(); view.Action != "" || view.Status != gateStepDone+" It is moving there now, by itself." {
		t.Errorf("view = %+v, want it moving with nothing to press", view)
	}

	if folder := g.carriedInto(t); folder != `D:\CodeGoblins` {
		t.Errorf("carried into %s, want the Dev Drive folder", folder)
	}

	// Act: the carry ends with the home on the drive.
	g.gateHome = `D:\CodeGoblins\no-mistakes`
	g.end(t, nil)
	g.s.keepDevDrive(context.Background())

	// Assert
	if g.isCarrying() || len(g.started) != 0 {
		t.Errorf("a second carry started: running %v, %d more started", g.isCarrying(), len(g.started))
	}
	if view := g.s.devDriveViewNow(); view.Action != "" || view.Status != "" {
		t.Errorf("view = %+v, want no row once the gate's home is on the drive", view)
	}
	if items := devDriveItems(g.s); len(items) != 0 {
		t.Errorf("items = %+v, want no Command Center item for the gate step", items)
	}
}

// A carry that gave up because gate runs stayed in flight is started again
// by the supervisor, with no item and nothing to press; one that failed for
// another reason says why in the row and waits for Try again, the one press
// the supervisor cannot make itself.
func TestTheSupervisorRetriesABusyCarryAndAFailedOneWaitsForTryAgain(t *testing.T) {
	// Arrange
	g := newGateStep(t, true)
	g.s.keepDevDrive(context.Background())
	g.carriedInto(t)

	// Act: the gate stayed busy all the while the carry waited.
	g.end(t, fmt.Errorf("nothing moved: %w", gatehome.ErrGateBusy))
	g.s.keepDevDrive(context.Background())

	// Assert
	if !g.isCarrying() {
		t.Fatal("the busy carry was not started again by itself")
	}
	g.carriedInto(t)
	if view := g.s.devDriveViewNow(); view.Action != "" {
		t.Errorf("view = %+v, want nothing to press while the supervisor retries", view)
	}

	// Act: the copy failed.
	g.end(t, errors.New(`gate home: the copy to D:\CodeGoblins\no-mistakes failed, so nothing moved: disk full`))
	g.s.keepDevDrive(context.Background())
	g.s.keepDevDrive(context.Background())

	// Assert
	if g.isCarrying() {
		t.Fatal("a failed carry was started again by itself")
	}
	if view := g.s.devDriveViewNow(); view.Action != devDriveTryAgain || view.Status != gateStepDone+` It did not move: gate home: the copy to D:\CodeGoblins\no-mistakes failed, so nothing moved: disk full` {
		t.Errorf("view = %+v, want why it failed and Try again", view)
	}

	// Act: he presses Try again.
	if err := g.s.askDevDrive(true); err != nil {
		t.Fatal(err)
	}
	g.s.keepDevDrive(context.Background())

	// Assert
	if !g.isCarrying() {
		t.Fatal("Try again started no carry")
	}
	g.carriedInto(t)
	if items := devDriveItems(g.s); len(items) != 0 {
		t.Errorf("items = %+v, want no Command Center item for the gate step", items)
	}
	g.ended <- nil
}

// The gate step is taken only on the person's word: until he asked for a Dev
// Drive the row offers Set up and nothing is carried, and gate runs that
// cannot be read are said in the row and read again.
func TestTheGateStepWaitsForThePersonsWordAndSaysWhatItCannotRead(t *testing.T) {
	// Arrange
	g := newGateStep(t, false)

	// Act
	g.s.keepDevDrive(context.Background())

	// Assert
	if view := g.s.devDriveViewNow(); view.Action != devDriveSetUp || view.Status != gateStepDone || g.isCarrying() {
		t.Fatalf("view = %+v, carrying %v; want Set up and nothing carried", view, g.isCarrying())
	}

	// Act: he asks, and the gate's database cannot be read.
	if err := g.s.askDevDrive(true); err != nil {
		t.Fatal(err)
	}
	g.runsErr = errors.New("pipeline: state query timed out")
	busy := g.s.keepDevDrive(context.Background())

	// Assert
	if view := g.s.devDriveViewNow(); view.Action != "" || view.Status != gateStepDone+" It moves by itself once the gate's runs can be read: pipeline: state query timed out" || g.isCarrying() {
		t.Errorf("view = %+v, carrying %v; want the unread runs said and nothing carried", view, g.isCarrying())
	}
	if busy != time.Minute {
		t.Errorf("next look in %s, want a minute while the gate step is under way", busy)
	}
}

// A gate item an earlier build left in the Command Center holds nothing up:
// the supervisor takes the gate step itself.
func TestAGateItemOfAnEarlierBuildDoesNotHoldTheGateStep(t *testing.T) {
	// Arrange
	g := newGateStep(t, true)
	now := time.Now().UTC()
	if _, err := g.s.recordBoardRun(Run{ID: "dev-drive-gate-0102030405060708", Identity: strings.Repeat("0a", 32), Title: "Move the gate's home", Shell: "powershell", Command: "exit 1", Cwd: g.s.Store.Home.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), DevDrive: "gate"}); err != nil {
		t.Fatal(err)
	}

	// Act
	g.s.keepDevDrive(context.Background())

	// Assert
	if view := g.s.devDriveViewNow(); view.Waiting != "" || !g.isCarrying() {
		t.Errorf("view = %+v, carrying %v; want the supervisor carrying the home itself", view, g.isCarrying())
	}
	g.carriedInto(t)
	g.ended <- nil
}

// The row says in plain words what is done, what is left and what it waits
// on, and offers a button only where a press does what the supervisor cannot.
func TestTheDevDriveRowSaysWhatIsDoneAndWhatIsLeft(t *testing.T) {
	dev := devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted, Free: 199 << 30}
	untrusted := dev
	untrusted.Trust = devdrive.Untrusted
	moved := home.DevDriveConfig{Root: `D:\CodeGoblins`, VHD: `C:\DevDrives\CodeGoblins.vhdx`, MovedAt: time.Now().Add(-time.Hour)}
	for _, test := range []struct {
		name    string
		volumes []devdrive.Volume
		config  home.DevDriveConfig
		// asks is whether the person asked for a Dev Drive, and ends how the
		// item that made ends: -1 leaves it waiting.
		asks       bool
		ends       int
		wantAction string
		wantStatus string
	}{
		{name: "nothing asked", ends: -1, wantAction: devDriveSetUp, wantStatus: "No Dev Drive is set up yet."},
		{name: "a drive not used yet", volumes: []devdrive.Volume{dev}, ends: -1, wantAction: devDriveSetUp, wantStatus: "D: is a Dev Drive, not used yet."},
		{name: "the create item waits", asks: true, ends: -1, wantStatus: "No Dev Drive is set up yet. Creating the drive waits for you in the Command Center."},
		{name: "the create item failed", asks: true, ends: 1, wantAction: devDriveTryAgain, wantStatus: "No Dev Drive is set up yet. Creating the drive is left: its Command Center item did not finish."},
		{name: "the trust item waits", volumes: []devdrive.Volume{untrusted}, asks: true, ends: -1, wantStatus: "D: is a Dev Drive, not used yet. Trusting it, which turns on Defender's performance mode there, waits for you in the Command Center."},
		{name: "the move item waits", volumes: []devdrive.Volume{dev}, asks: true, ends: -1, wantStatus: "D: is a Dev Drive, not used yet. Moving the worktrees and caches onto it waits for you in the Command Center."},
		{name: "the attach item waits", config: moved, ends: -1, wantStatus: "D: is not attached, so no goblin starts. Attaching it again waits for you in the Command Center."},
		{name: "the attach item failed", config: moved, ends: 1, wantAction: devDriveAttach, wantStatus: "D: is not attached, so no goblin starts."},
		{name: "everything is on the drive", volumes: []devdrive.Volume{dev}, config: moved, asks: true, ends: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			machine := devDriveMachine()
			machine.Volumes = append(machine.Volumes, test.volumes...)
			if len(test.volumes) > 0 {
				machine.Letters |= 1 << 3
			}
			s := devDriveService(t, &machine)
			if err := home.WriteDevDriveConfig(s.Store.Home.Root, test.config); err != nil {
				t.Fatal(err)
			}
			if test.asks {
				if err := s.askDevDrive(true); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			s.keepDevDrive(context.Background())
			if test.ends >= 0 {
				endDevDriveItem(t, s, test.ends)
				s.keepDevDrive(context.Background())
			}

			// Assert
			if view := s.devDriveViewNow(); view.Action != test.wantAction || view.Status != test.wantStatus {
				t.Errorf("row = %q with the button %q, want %q with %q", view.Status, view.Action, test.wantStatus, test.wantAction)
			}
		})
	}
}
