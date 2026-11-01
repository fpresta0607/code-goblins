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

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

func TestAnUndispatchedRootWithoutVerifiedIdentityNeverReachesSpawn(t *testing.T) {
	// Arrange: only the old root brief remains, as after the resolved task's
	// records were lost. No backlog row, live status or archive protects it.
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	id := "root-widgets-7"
	writeFile(t, filepath.Join(h.Data, id, "brief.md"), rootBrief("widgets", id, rootIssue(7), nil))

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
		reading.at = time.Now().Add(-rootPickupEvery)
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
	writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), rootBrief("widgets", "root-widgets-7", rootIssue(8), nil))
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
	writeFile(t, filepath.Join(h.Data, "root-widgets-7", "brief.md"), strings.ReplaceAll(plainBrief, "C:\\dev\\code-goblins", "widgets"))
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
	writeFile(t, filepath.Join(h.Data, id, "brief.md"), rootBrief("widgets", id, rootIssue(7), nil))
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
