package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

func TestAnUndispatchedRootWithoutVerifiedIdentityNeverReachesSpawn(t *testing.T) {
	// Arrange: only the old root brief remains, as after the resolved task's
	// records were lost. No backlog row, live status or archive protects it.
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	id := "root-widgets-7"
	writeFile(t, filepath.Join(h.Data, id, "brief.md"), rootBrief("widgets", id, rootIssue(7)))

	// Act: the real fleet reading discovers and schedules briefs.
	err := handler.Service.checkFleet(t.Context(), time.Now().UTC())
	waitRootReading(t, handler.Service)
	startErr := handler.Service.startQueued(id, false)

	// Assert: spawn is the boundary before worktree allocation, installation
	// and harness launch. Neither automatic admission path may cross it.
	if err != nil {
		t.Fatal(err)
	}
	if startErr == nil || !strings.Contains(startErr.Error(), "Root issue") {
		t.Errorf("start = %v, want an explicit root verification refusal", startErr)
	}
	if calls := spawner.recorded(); len(calls) != 0 {
		t.Errorf("spawn calls = %v, want none", calls)
	}
	handler.Service.mu.Lock()
	defer handler.Service.mu.Unlock()
	if handler.Service.scheduling == nil || !strings.Contains(handler.Service.scheduling.Text, "Root issue") {
		t.Errorf("scheduling = %+v, want the root's reconciliation reason", handler.Service.scheduling)
	}
}

func TestRootVerificationDoesNotWaitOnGitHubToStartOrdinaryWork(t *testing.T) {
	// Arrange: the root is first, and GitHub holds its read open.
	service, h, spawner, _ := rootStartBoard(t, "open", nil)
	reading, release := make(chan struct{}), make(chan struct{})
	service.Options.RootIssues.IssueState = func(ctx context.Context, repository string, number int) (string, error) {
		close(reading)
		select {
		case <-release:
			return "", errors.New("GitHub unavailable")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	t.Cleanup(func() { close(release); waitRootReading(t, service) })
	writeFile(t, filepath.Join(h.Data, "z-new-work", "brief.md"), plainBrief)

	// Act
	if err := service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}

	// Assert
	select {
	case <-reading:
	case <-time.After(30 * time.Second):
		t.Fatal("no root read began")
	}
	if calls := awaitDispatch(t, service, spawner, 1); calls[0][1] != "z-new-work" {
		t.Fatalf("spawns=%v, want unrelated work while the root read waits", calls)
	}
}

func TestRootVerificationUsesTheConfiguredIssueRepository(t *testing.T) {
	service, h, spawner, _ := rootStartBoard(t, "open", nil)
	service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "acme/private-source", nil }
	writeFile(t, filepath.Join(h.Data, "projects", "widgets", "project.json"), `{"project":"widgets","issue_repository":"acme/widgets"}`)
	if err := service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	waitRootReading(t, service)
	if err := service.startQueued("root-widgets-7", true); err != nil {
		t.Fatal(err)
	}
	awaitDispatch(t, service, spawner, 1)
}

func TestRootStartRechecksProjectIdentityAfterAnOpenVerification(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *Service, home.Home)
	}{
		{name: "configured issue repository changes", change: func(t *testing.T, s *Service, h home.Home) {
			writeFile(t, filepath.Join(h.Data, "projects", "widgets", "project.json"), `{"project":"widgets","issue_repository":"acme/other"}`)
		}},
		{name: "checkout origin changes", change: func(t *testing.T, s *Service, h home.Home) {
			s.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "acme/other", nil }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: GitHub verified the old mapping while the root waited.
			service, h, spawner, reads := rootStartBoard(t, "open", nil)
			if err := service.startQueued("root-widgets-7", false); err == nil {
				t.Fatal("unverified root started")
			}
			waitRootReading(t, service)
			test.change(t, service, h)

			// Act
			err := service.startQueued("root-widgets-7", false)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "differs") {
				t.Errorf("Start=%v, want the new mapping refused", err)
			}
			if len(spawner.recorded()) != 0 || reads.Load() != 1 {
				t.Errorf("spawns=%v reads=%d", spawner.recorded(), reads.Load())
			}
			if err == nil {
				awaitDispatch(t, service, spawner, 1)
			}
		})
	}
}

func TestAnExplicitOrdinaryRowIsNotARootJustBecauseOfItsName(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] root-widget-7 - New authorized work (repo: widgets)\n")
	if err := handler.Service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][1] != "root-widget-7" {
		t.Fatalf("spawns=%v", calls)
	}
}

func TestAnExplicitRootRowCannotIgnoreAnUnreadablePickupIdentity(t *testing.T) {
	// Arrange: the row alone could be new work, but its earlier identity
	// cannot be read, so absence of a pickup has not been established.
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] root-widget-7 - Work (repo: widgets)\n")
	writeFile(t, filepath.Join(h.State, rootPickupsFile), "unreadable json")

	// Act
	err := handler.Service.startQueued("root-widget-7", false)

	// Assert
	if err == nil {
		awaitDispatch(t, handler.Service, spawner, 1)
		t.Fatal("unreadable root identity authorized a start")
	}
	if !strings.Contains(err.Error(), "pickup identity cannot be read") || len(spawner.recorded()) != 0 {
		t.Fatalf("Start=%v spawns=%v", err, spawner.recorded())
	}
}

func TestRootStartKeepsActiveHeldAndRetiredPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, want string
		arrange    func(*testing.T, home.Home)
	}{
		{name: "active", want: "already runs", arrange: func(t *testing.T, h home.Home) {
			writeFile(t, filepath.Join(h.State, "root-widgets-7.meta"), "active")
		}},
		{name: "parked", want: "not queued", arrange: func(t *testing.T, h home.Home) {
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Parked\n- [ ] root-widgets-7 - Root (repo: widgets)\n")
		}},
		{name: "held", want: "waits for", arrange: func(t *testing.T, h home.Home) {
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] root-widgets-7 - Root (repo: widgets) blocked-by: until 2099-01-01T00:00Z\n")
		}},
		{name: "retired", want: "CFO retired", arrange: func(t *testing.T, h home.Home) {
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] root-widgets-7 - Root (repo: widgets)\n")
			writeOutcome(t, h, state.Outcome{ID: "root-widgets-7", Generation: "old", Phase: "stopped", At: time.Now().Add(time.Hour)})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, spawner, reads := rootStartBoard(t, "open", nil)
			test.arrange(t, h)
			err := service.startQueued("root-widgets-7", false)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Start=%v, want %s", err, test.want)
			}
			if reads.Load() != 0 || len(spawner.recorded()) != 0 {
				t.Fatal("a prior hold reached root verification or spawn")
			}
		})
	}
}

func TestRootVerificationRechecksChangedEvidenceAndExpiresFailedReads(t *testing.T) {
	service, h, _, reads := rootStartBoard(t, "closed", nil)
	if err := service.startQueued("root-widgets-7", false); err == nil {
		t.Fatal("unverified root started")
	}
	waitRootReading(t, service)
	service.mu.Lock()
	for root, reading := range service.rootStarts {
		reading.At = time.Now().Add(-rootPickupEvery)
		service.rootStarts[root] = reading
	}
	service.mu.Unlock()
	if err := service.startQueued("root-widgets-7", false); err == nil {
		t.Fatal("expired root started")
	}
	waitRootReading(t, service)
	if reads.Load() != 2 {
		t.Fatalf("reads=%d, want one retry after expiry", reads.Load())
	}
	if notices := fleetWakeRecords(t, h, "notify"); len(notices) != 1 {
		t.Fatalf("repeated notices: %v", notices)
	}
	writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), rootBrief("widgets", "root-widgets-7", rootIssue(8)))
	err := service.startQueued("root-widgets-7", true)
	if err == nil || !strings.Contains(err.Error(), "identities disagree") {
		t.Fatalf("changed brief reused cached state: %v", err)
	}
}

func TestRootStartUsesATicketWhenNoPickupOrGeneratedBriefRemains(t *testing.T) {
	service, h, spawner, _ := rootStartBoard(t, "closed", nil)
	if err := os.Remove(filepath.Join(h.State, rootPickupsFile)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), "## Project\nwidgets\n## Task\nResolve the root.\n")
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] root-widgets-7 - Resolve the root (repo: widgets)\n")
	if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "root-widgets-7", Repository: rootRepository, Number: 7, IsClaimed: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	waitRootReading(t, service)
	err := service.startQueued("root-widgets-7", false)
	if err == nil || !strings.Contains(err.Error(), "is closed") || len(spawner.recorded()) != 0 {
		t.Fatalf("Start=%v calls=%v", err, spawner.recorded())
	}
}

func waitRootReading(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		isReading := service.isReadingRoot
		service.mu.Unlock()
		if !isReading {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("root verification did not finish")
}

func rootStartBoard(t *testing.T, issueState string, issueErr error) (*Service, home.Home, *spawnRecorder, *atomic.Int32) {
	t.Helper()
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	service := handler.Service
	reads := &atomic.Int32{}
	service.Options.RootIssues = &RootIssues{
		Checkout:   func(string) (string, error) { return filepath.Join(h.Root, "widgets"), nil },
		Repository: func(context.Context, string) (string, error) { return rootRepository, nil },
		IssueState: func(ctx context.Context, repository string, number int) (string, error) {
			reads.Add(1)
			if deadline, exists := ctx.Deadline(); !exists || time.Until(deadline) > ghCallTimeout {
				t.Error("issue read has no bounded deadline")
			}
			if repository != rootRepository || number != 7 {
				t.Errorf("read %s#%d, want the persisted issue identity", repository, number)
			}
			return issueState, issueErr
		},
		ClosingPullRequests: func(context.Context, string, int) ([]string, error) { return nil, nil },
	}
	id := "root-widgets-7"
	writeFile(t, filepath.Join(h.Data, id, "brief.md"), rootBrief("widgets", id, rootIssue(7)))
	if err := writeRootPickups(h.State, []rootPickup{{Task: id, Repository: rootRepository, Number: 7, At: rootNow}}); err != nil {
		t.Fatal(err)
	}
	return service, h, spawner, reads
}

func TestRootDispatchReconcilesTheRecordedIssueBeforeAnySpawn(t *testing.T) {
	for _, test := range []struct {
		name, state, want string
		err               error
	}{
		{name: "closed", state: "closed", want: "is closed"},
		{name: "missing after repository recreation", state: "missing", want: "is missing"},
		{name: "replaced by a pull request", state: "pull request", want: "is pull request"},
		{name: "outage", err: errors.New("GitHub unavailable"), want: "cannot be read"},
		{name: "unknown state", want: "not a verified open issue"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, spawner, reads := rootStartBoard(t, test.state, test.err)
			id := "root-widgets-7"
			if !slices.Contains(queuedCandidates(h), id) {
				t.Fatal("the synthetic stale brief was not discovered")
			}

			// Act: the first pass learns, subsequent passes and Start recheck.
			for pass := range 3 {
				if err := service.checkFleet(t.Context(), rootNow.Add(time.Duration(pass)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				waitRootReading(t, service)
			}
			err := service.startQueued(id, false)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Start = %v, want %q", err, test.want)
			}
			if len(spawner.recorded()) != 0 || reads.Load() != 1 {
				t.Fatalf("spawns=%v reads=%d, want none and one bounded read", spawner.recorded(), reads.Load())
			}
			if records := fleetWakeRecords(t, h, "notify"); len(records) != 1 || !strings.Contains(records[0].Detail, test.want) {
				t.Fatalf("notices=%v, want one explicit reconciliation notice", records)
			}
		})
	}
}

func TestClosedRootStopsRepeatedIdleAndTurnWarnings(t *testing.T) {
	for _, test := range []struct {
		name, issueState string
		issueErr         error
		isWaiting        bool
	}{
		{name: "verified closed", issueState: "closed"},
		{name: "missing", issueState: "missing", isWaiting: true},
		{name: "auth failure", issueErr: errors.New("HTTP 401"), isWaiting: true},
		{name: "not found failure", issueErr: errors.New("HTTP 404"), isWaiting: true},
		{name: "timeout", issueErr: context.DeadlineExceeded, isWaiting: true},
		{name: "partial closed read", issueState: "closed", issueErr: errors.New("incomplete issue response"), isWaiting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: matching historical identity survives without a queued row.
			service, h, spawner, reads := rootStartBoard(t, test.issueState, test.issueErr)
			if !test.isWaiting {
				rootStartCheckout(t, h)
			}
			id := "root-widgets-7"
			if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: id, Repository: rootRepository, Number: 7, URL: rootIssue(7).URL, IsClaimed: true}); err != nil {
				t.Fatal(err)
			}
			paths := []string{filepath.Join(h.State, rootPickupsFile), filepath.Join(h.State, "tickets", id+".json"), filepath.Join(h.Data, id, "brief.md")}
			before := make([]string, len(paths))
			for index, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[index] = string(data)
			}
			if err := service.checkFleet(t.Context(), rootNow); err != nil {
				t.Fatal(err)
			}
			waitRootReading(t, service)
			memory, err := service.Options.Dispatch.Memory()
			if err != nil {
				t.Fatal(err)
			}

			// Act: repeat the scheduler, idle observer and independent turn reader.
			for _, minute := range []int{1, 31, 61} {
				now := rootNow.Add(time.Duration(minute) * time.Minute)
				if err := service.checkFleet(t.Context(), now); err != nil {
					t.Fatal(err)
				}
				waitRootReading(t, service)
				service.mu.Lock()
				waiting := slices.Clone(service.scheduling.Waiting)
				service.mu.Unlock()
				if got := len(waiting) > 0; got != test.isWaiting {
					t.Errorf("minute %d: Scheduling.Waiting=%v, want waiting=%v", minute, waiting, test.isWaiting)
				}
				if got := WorkWaits(h); got != test.isWaiting {
					t.Errorf("minute %d: WorkWaits=%v, want %v", minute, got, test.isWaiting)
				}
				if detail, err := IdleTurnWake(h, memory, now); err != nil || detail != "" {
					t.Errorf("minute %d: turn wake=%q err=%v, want no start instruction", minute, detail, err)
				}
			}

			// Assert: only unknown work warrants repeated idle warnings.
			if records := fleetWakeRecords(t, h, "idle"); (len(records) > 0) != test.isWaiting {
				t.Errorf("idle wakes=%v, want waiting=%v", records, test.isWaiting)
			}
			if notices := fleetWakeRecords(t, h, "notify"); len(notices) != 1 {
				t.Errorf("reconciliation notices=%v, want one", notices)
			}
			if len(spawner.recorded()) != 0 || reads.Load() != 1 {
				t.Errorf("spawns=%v reads=%d, want none and one bounded read", spawner.recorded(), reads.Load())
			}
			if _, err := state.ReadOutcome(h.State, id); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("closure manufactured an outcome: %v", err)
			}
			for index, path := range paths {
				if after, err := os.ReadFile(path); err != nil || string(after) != before[index] {
					t.Errorf("historical identity changed at %s: %v", path, err)
				}
			}
		})
	}
}

func rootStartCheckout(t *testing.T, h home.Home) string {
	t.Helper()
	t.Setenv(install.ProjectsRootVariable, h.Root)
	checkout := filepath.Join(h.Root, "widgets")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/acme/widgets.git"}} {
		result, err := (execx.OSRunner{}).Run(t.Context(), execx.Request{Name: "git", Args: append([]string{"-C", checkout}, args...)})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("git %v: %v %s", args, err, result.Stderr)
		}
	}
	return checkout
}

func TestIndependentRootReaderRequiresCurrentTypedClosure(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, home.Home, *fleetWakes)
	}{
		{name: "missing proof", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings = nil }},
		{name: "missing observation", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings[0].At = time.Time{} }},
		{name: "future observation", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].At = time.Now().Add(time.Hour)
		}},
		{name: "expired observation", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].At = time.Now().Add(-rootPickupEvery)
		}},
		{name: "different task", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].Root.Task = "root-other-7"
		}},
		{name: "different project", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings[0].Root.Project = "other" }},
		{name: "different repository", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].Root.Repository = "acme/other"
		}},
		{name: "different issue", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings[0].Root.Number = 8 }},
		{name: "different provenance", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].Root.Revision = "old revision"
		}},
		{name: "partial proof", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings[0].Reason = "" }},
		{name: "unknown result", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings[0].Result = "unrecognized" }},
		{name: "unknown says closed", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].Result = rootStateUnknown
		}},
		{name: "owned says closed", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) { watched.RootReadings[0].Result = rootStateOwned }},
		{name: "open proof grants no start authority", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings[0].Result, watched.RootReadings[0].Reason = rootVerifiedOpen, ""
		}},
		{name: "duplicate task proofs", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			watched.RootReadings = append(watched.RootReadings, watched.RootReadings[0])
		}},
		{name: "over limit", change: func(_ *testing.T, _ home.Home, watched *fleetWakes) {
			for len(watched.RootReadings) <= rootReadingsLimit {
				watched.RootReadings = append(watched.RootReadings, watched.RootReadings[0])
			}
		}},
		{name: "manifest repository changes", change: func(t *testing.T, h home.Home, _ *fleetWakes) {
			writeFile(t, filepath.Join(h.Data, "projects", "widgets", "project.json"), `{"project":"widgets","issue_repository":"acme/other"}`)
		}},
		{name: "manifest unreadable", change: func(t *testing.T, h home.Home, _ *fleetWakes) {
			writeFile(t, filepath.Join(h.Data, "projects", "widgets", "project.json"), "broken json")
		}},
		{name: "pickup provenance removed", change: func(t *testing.T, h home.Home, _ *fleetWakes) {
			if err := writeRootPickups(h.State, nil); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "brief changes", change: func(t *testing.T, h home.Home, _ *fleetWakes) {
			writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), rootBrief("widgets", "root-widgets-7", rootIssue(8)))
		}},
		{name: "matching ticket added", change: func(t *testing.T, h home.Home, _ *fleetWakes) {
			if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "root-widgets-7", Repository: rootRepository, Number: 7}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "live ownership appears", change: func(t *testing.T, h home.Home, _ *fleetWakes) {
			fileRootAsLiveTask(t, h, "widgets-follow-up", rootIssue(7).URL)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the normal fleet writer produces independently readable proof.
			_, h, spawner, _ := persistedRootBoard(t)
			watched, err := readFleetWakes(h.State)
			if err != nil {
				t.Fatal(err)
			}
			test.change(t, h, &watched)
			if err := writeFleetWakes(h.State, watched); err != nil {
				t.Fatal(err)
			}

			// Act
			isWaiting := WorkWaits(h)
			detail, err := IdleTurnWake(h, Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, rootNow)

			// Assert: neither a partial record nor English text proves closure.
			if !isWaiting || err != nil || detail != "" || len(spawner.recorded()) != 0 {
				t.Fatalf("WorkWaits=%v turn=%q err=%v spawns=%v", isWaiting, detail, err, spawner.recorded())
			}
		})
	}
}

func persistedRootBoard(t *testing.T) (*Service, home.Home, *spawnRecorder, *atomic.Int32) {
	t.Helper()
	service, h, spawner, reads := rootStartBoard(t, "closed", nil)
	t.Setenv(install.ProjectsRootVariable, h.Root)
	writeFile(t, filepath.Join(h.Root, "widgets", ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(h.Data, "projects", "widgets", "project.json"), `{"project":"widgets","issue_repository":"acme/widgets"}`)
	if err := service.checkFleet(t.Context(), rootNow); err != nil {
		t.Fatal(err)
	}
	waitRootReading(t, service)
	if err := service.checkFleet(t.Context(), rootNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if WorkWaits(h) {
		t.Fatal("the independent reader did not consume the fleet's valid closure proof")
	}
	return service, h, spawner, reads
}

func TestUnreadableFleetStateNeverProvesRootClosure(t *testing.T) {
	// Arrange
	_, h, _, _ := persistedRootBoard(t)
	writeFile(t, fleetWakesPath(h.State), `{"schema":"cfo-fleet-wakes.v1","root_readings":[`)

	// Act
	isWaiting := WorkWaits(h)

	// Assert
	if !isWaiting {
		t.Fatal("a partial state read silently closed the root")
	}
}

func TestNewRootReadingsReplacePersistedClosure(t *testing.T) {
	for _, test := range []struct {
		name, issueState string
		issueErr         error
	}{
		{name: "reopened", issueState: "open"},
		{name: "auth failure", issueErr: errors.New("HTTP 401")},
		{name: "partial closed read", issueState: "closed", issueErr: errors.New("partial response")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, spawner, reads := persistedRootBoard(t)
			service.Options.Dispatch.Spawn = nil
			service.Options.RootIssues.IssueState = func(context.Context, string, int) (string, error) {
				reads.Add(1)
				return test.issueState, test.issueErr
			}
			service.mu.Lock()
			for root, reading := range service.rootStarts {
				reading.At = time.Now().Add(-rootPickupEvery)
				service.rootStarts[root] = reading
			}
			service.mu.Unlock()
			if err := service.startQueued("root-widgets-7", false); err == nil {
				t.Fatal("expired closure started a task")
			}
			waitRootReading(t, service)

			// Act: the sole writer consumes the newly completed read.
			if err := service.checkFleet(t.Context(), rootNow.Add(2*time.Minute)); err != nil {
				t.Fatal(err)
			}

			// Assert
			watched, err := readFleetWakes(h.State)
			if err != nil || len(watched.RootReadings) != 1 || watched.RootReadings[0].Result == rootVerifiedClosed || !WorkWaits(h) || len(spawner.recorded()) != 0 {
				t.Fatalf("readings=%v err=%v spawns=%v, want new non-closure evidence", watched.RootReadings, err, spawner.recorded())
			}
		})
	}
}

func TestClosedRootEvidenceRechecksLocalIdentityAndOwnership(t *testing.T) {
	for _, test := range []struct {
		name, want string
		change     func(*testing.T, *Service, home.Home)
	}{
		{name: "manifest repository changes", want: "differs", change: func(t *testing.T, _ *Service, h home.Home) {
			writeFile(t, filepath.Join(h.Data, "projects", "widgets", "project.json"), `{"project":"widgets","issue_repository":"acme/other"}`)
		}},
		{name: "checkout repository changes", want: "differs", change: func(_ *testing.T, service *Service, _ home.Home) {
			service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "acme/other", nil }
		}},
		{name: "repository unreadable", want: "cannot read origin", change: func(_ *testing.T, service *Service, _ home.Home) {
			service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "", errors.New("cannot read origin") }
		}},
		{name: "brief changes", want: "identities disagree", change: func(t *testing.T, _ *Service, h home.Home) {
			writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), rootBrief("widgets", "root-widgets-7", rootIssue(8)))
		}},
		{name: "pickup changes", want: "identities disagree", change: func(t *testing.T, _ *Service, h home.Home) {
			if err := writeRootPickups(h.State, []rootPickup{{Task: "root-widgets-7", Repository: rootRepository, Number: 8, At: rootNow}}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pickup unreadable", want: "pickup identity cannot be read", change: func(t *testing.T, _ *Service, h home.Home) {
			writeFile(t, filepath.Join(h.State, rootPickupsFile), "broken json")
		}},
		{name: "ticket changes", want: "identities disagree", change: func(t *testing.T, _ *Service, h home.Home) {
			if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "root-widgets-7", Repository: "acme/other", Number: 7}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "live ownership appears", want: "belongs to widgets-follow-up", change: func(t *testing.T, _ *Service, h home.Home) {
			fileRootAsLiveTask(t, h, "widgets-follow-up", rootIssue(7).URL)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, spawner, reads := rootStartBoard(t, "closed", nil)
			if err := service.checkFleet(t.Context(), rootNow); err != nil {
				t.Fatal(err)
			}
			waitRootReading(t, service)
			var closed StartRefusal
			if err := service.startQueued("root-widgets-7", false); !errors.As(err, &closed) || !closed.Held || closed.Waits {
				t.Fatalf("verified closure = %v, want a non-actionable refusal", err)
			}
			test.change(t, service, h)

			// Act
			err := service.startQueued("root-widgets-7", false)

			// Assert
			var refusal StartRefusal
			if !errors.As(err, &refusal) || refusal.Held || !refusal.Waits || !strings.Contains(refusal.Reason, test.want) {
				t.Errorf("changed evidence = %v, want unknown or owned: %s", err, test.want)
			}
			if len(spawner.recorded()) != 0 || reads.Load() != 1 {
				t.Errorf("spawns=%v reads=%d, want none and the original read only", spawner.recorded(), reads.Load())
			}
		})
	}
}

func TestClearedLocalRootEvidenceNeverOutlivesItsCause(t *testing.T) {
	for _, test := range []struct {
		name, want     string
		arrange, clear func(*testing.T, *Service, home.Home)
	}{
		{name: "live owner ends", want: "belongs to widgets-follow-up", arrange: func(t *testing.T, _ *Service, h home.Home) {
			fileRootAsLiveTask(t, h, "widgets-follow-up", rootIssue(7).URL)
		}, clear: func(t *testing.T, _ *Service, h home.Home) {
			if err := os.Remove(state.TaskMetaPath(h.State, "widgets-follow-up")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "project mapping restored", want: "differs", arrange: func(t *testing.T, service *Service, _ home.Home) {
			if err := service.checkFleet(t.Context(), rootNow); err != nil {
				t.Fatal(err)
			}
			waitRootReading(t, service)
			service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "acme/other", nil }
		}, clear: func(_ *testing.T, service *Service, _ home.Home) {
			service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return rootRepository, nil }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: local evidence refuses the root over several passes.
			service, h, spawner, reads := rootStartBoard(t, "closed", nil)
			test.arrange(t, service, h)
			for _, minute := range []int{1, 2} {
				if err := service.checkFleet(t.Context(), rootNow.Add(time.Duration(minute)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				waitRootReading(t, service)
			}
			test.clear(t, service, h)

			// Act: the scheduler passes after the local evidence clears.
			for _, minute := range []int{3, 4} {
				if err := service.checkFleet(t.Context(), rootNow.Add(time.Duration(minute)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				waitRootReading(t, service)
			}
			err := service.startQueued("root-widgets-7", false)

			// Assert: GitHub's closure decides, not the cleared local reason.
			var refusal StartRefusal
			if !errors.As(err, &refusal) || !refusal.Held || refusal.Waits || !strings.Contains(refusal.Reason, "is closed") {
				t.Errorf("Start=%v, want the verified closure", err)
			}
			service.mu.Lock()
			waiting := slices.Clone(service.scheduling.Waiting)
			service.mu.Unlock()
			if len(waiting) != 0 || reads.Load() != 1 || len(spawner.recorded()) != 0 {
				t.Errorf("waiting=%v reads=%d spawns=%v, want none, one bounded read and none", waiting, reads.Load(), spawner.recorded())
			}
			var notices []string
			for _, record := range fleetWakeRecords(t, h, "notify") {
				if strings.Contains(record.Detail, test.want) {
					notices = append(notices, record.Detail)
				}
			}
			if len(notices) != 1 {
				t.Errorf("local notices=%v, want one", notices)
			}
		})
	}
}

func TestEachDistinctLocalRootProblemIsToldOnce(t *testing.T) {
	for _, test := range []struct {
		name, first    string
		arrange, clear func(*testing.T, *Service, home.Home)
	}{
		{name: "origin unreadable", first: "cannot read origin", arrange: func(_ *testing.T, service *Service, _ home.Home) {
			service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "", errors.New("cannot read origin") }
		}, clear: func(*testing.T, *Service, home.Home) {}},
		{name: "live tasks' filed roots unreadable", first: "filed root issues cannot be read", arrange: func(t *testing.T, _ *Service, h home.Home) {
			fileRootAsLiveTask(t, h, "widgets-other", rootIssue(8).URL)
			taskTmp := filepath.Join(h.State, "tasktmp", "widgets-other")
			entries, err := os.ReadDir(taskTmp)
			if err != nil || len(entries) != 1 {
				t.Fatalf("filed roots=%v err=%v, want the one record", entries, err)
			}
			filed := filepath.Join(taskTmp, entries[0].Name())
			if err := os.Remove(filed); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filed, 0o700); err != nil {
				t.Fatal(err)
			}
		}, clear: func(t *testing.T, _ *Service, h home.Home) {
			if err := os.Remove(state.TaskMetaPath(h.State, "widgets-other")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: GitHub verified the root closed.
			service, h, spawner, reads := rootStartBoard(t, "closed", nil)
			if err := service.checkFleet(t.Context(), rootNow); err != nil {
				t.Fatal(err)
			}
			waitRootReading(t, service)
			pass := func(minute int) {
				if err := service.checkFleet(t.Context(), rootNow.Add(time.Duration(minute)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				waitRootReading(t, service)
			}

			// Act: one local problem holds over two passes, then a second does.
			test.arrange(t, service, h)
			pass(1)
			pass(2)
			test.clear(t, service, h)
			service.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "acme/other", nil }
			pass(3)
			pass(4)

			// Assert: each distinct problem reaches the CFO exactly once.
			for _, want := range []string{test.first, "differs from the project's issue repository acme/other"} {
				var notices []string
				for _, record := range fleetWakeRecords(t, h, "notify") {
					if strings.Contains(record.Detail, want) {
						notices = append(notices, record.Detail)
					}
				}
				if len(notices) != 1 {
					t.Errorf("notices for %q = %v, want one", want, notices)
				}
			}
			if len(spawner.recorded()) != 0 || reads.Load() != 1 {
				t.Errorf("spawns=%v reads=%d, want none and the original read only", spawner.recorded(), reads.Load())
			}
		})
	}
}

func TestAReopenedRootReturnsToOrdinarySchedulingAfterClosureExpires(t *testing.T) {
	// Arrange
	service, _, spawner, reads := rootStartBoard(t, "closed", nil)
	if err := service.checkFleet(t.Context(), rootNow); err != nil {
		t.Fatal(err)
	}
	waitRootReading(t, service)
	service.Options.RootIssues.IssueState = func(context.Context, string, int) (string, error) {
		reads.Add(1)
		return "open", nil
	}
	service.mu.Lock()
	for root, reading := range service.rootStarts {
		reading.At = time.Now().Add(-rootPickupEvery)
		service.rootStarts[root] = reading
	}
	service.mu.Unlock()

	// Act
	if err := service.checkFleet(t.Context(), rootNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	waitRootReading(t, service)
	if err := service.checkFleet(t.Context(), rootNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if calls := awaitDispatch(t, service, spawner, 1); calls[0][1] != "root-widgets-7" || reads.Load() != 2 {
		t.Fatalf("spawns=%v reads=%d, want one ordinary open-root start after a fresh read", calls, reads.Load())
	}
}

func TestAQueuedRootNeverStartsBesideTheTaskThatOwnsItsIssue(t *testing.T) {
	for _, test := range []struct {
		name, want string
		reads      int32
		arrange    func(*testing.T, *Service, home.Home)
	}{
		{name: "filed by a live task given it as a follow-up", want: "belongs to widgets-follow-up, the live task that filed it", arrange: func(t *testing.T, _ *Service, h home.Home) {
			fileRootAsLiveTask(t, h, "widgets-follow-up", rootIssue(7).URL)
		}},
		{name: "closed by an open pull request", want: "closed by the open pull request https://github.com/acme/widgets/pull/8", reads: 1, arrange: func(t *testing.T, s *Service, _ home.Home) {
			s.Options.RootIssues.ClosingPullRequests = func(_ context.Context, repository string, number int) ([]string, error) {
				if repository != rootRepository || number != 7 {
					t.Errorf("read the pull requests that close %s#%d, want the persisted issue identity", repository, number)
				}
				return []string{"https://github.com/acme/widgets/pull/8"}, nil
			}
		}},
		{name: "pull requests unreadable", want: "cannot be read", reads: 1, arrange: func(t *testing.T, s *Service, _ home.Home) {
			s.Options.RootIssues.ClosingPullRequests = func(context.Context, string, int) ([]string, error) {
				return nil, errors.New("GitHub unavailable")
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the root was picked up and queued, and its issue has an
			// owner by the time it would start.
			service, h, spawner, reads := rootStartBoard(t, "open", nil)
			test.arrange(t, service, h)

			// Act
			for pass := range 3 {
				if err := service.checkFleet(t.Context(), rootNow.Add(time.Duration(pass)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				waitRootReading(t, service)
			}
			err := service.startQueued("root-widgets-7", false)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Start = %v, want %q", err, test.want)
			}
			if len(spawner.recorded()) != 0 || reads.Load() != test.reads {
				t.Fatalf("spawns=%v reads=%d, want no second owner spawned and %d reads", spawner.recorded(), reads.Load(), test.reads)
			}
			if records := fleetWakeRecords(t, h, "notify"); len(records) != 1 || !strings.Contains(records[0].Detail, test.want) {
				t.Fatalf("notices=%v, want one reconciliation notice", records)
			}
		})
	}
}

func TestAnOpenRootStillStartsAfterVerification(t *testing.T) {
	service, _, spawner, reads := rootStartBoard(t, "open", nil)
	if err := service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	waitRootReading(t, service)
	if err := service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls := awaitDispatch(t, service, spawner, 1); calls[0][1] != "root-widgets-7" || reads.Load() != 1 {
		t.Fatalf("spawns=%v reads=%d, want the verified open root", calls, reads.Load())
	}
}

func TestRootIdentityConflictsNeverBecomeCompletionOrRestartAuthority(t *testing.T) {
	for _, test := range []struct {
		name, want string
		change     func(*testing.T, *Service, home.Home)
	}{
		{name: "changed repository", want: "differs", change: func(t *testing.T, s *Service, h home.Home) {
			s.Options.RootIssues.Repository = func(context.Context, string) (string, error) { return "acme/recreated", nil }
		}},
		{name: "unreadable pickup", want: "pickup identity cannot be read", change: func(t *testing.T, s *Service, h home.Home) {
			writeFile(t, filepath.Join(h.State, rootPickupsFile), "broken json")
		}},
		{name: "conflicting ticket", want: "identities disagree", change: func(t *testing.T, s *Service, h home.Home) {
			if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "root-widgets-7", Repository: "acme/old", Number: 7}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "conflicting ticket URL", want: "ticket's issue identities disagree", change: func(t *testing.T, s *Service, h home.Home) {
			if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "root-widgets-7", Repository: rootRepository, Number: 7, URL: "https://github.com/acme/other/issues/7"}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unreadable ticket", want: "ticket identity cannot be read", change: func(t *testing.T, s *Service, h home.Home) {
			writeFile(t, filepath.Join(h.State, "tickets", "root-widgets-7.json"), "broken json")
		}},
		{name: "missing identity", want: "no recorded issue identity", change: func(t *testing.T, s *Service, h home.Home) {
			if err := writeRootPickups(h.State, nil); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), plainBrief)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, h, spawner, reads := rootStartBoard(t, "open", nil)
			test.change(t, service, h)
			if err := service.checkFleet(t.Context(), time.Now()); err != nil {
				t.Fatal(err)
			}
			waitRootReading(t, service)
			err := service.startQueued("root-widgets-7", false)
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "Already finished") {
				t.Fatalf("Start = %v, want reconciliation: %s", err, test.want)
			}
			if len(spawner.recorded()) != 0 || reads.Load() != 0 {
				t.Fatalf("spawns=%v reads=%d", spawner.recorded(), reads.Load())
			}
		})
	}
}
