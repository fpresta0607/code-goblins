package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// filedRoot is one filing a stub FileRoot was asked for.
type filedRoot struct {
	project string
	root    tickets.Root
}

// filingBoard is a service whose FileRoot sends every filing it is asked
// for to the channel it returns, and answers with refused.
func filingBoard(t *testing.T, refused error) (*Service, chan filedRoot) {
	t.Helper()
	handler, _ := orderBoard(t)
	filings := make(chan filedRoot, 10)
	handler.Service.Options.FileRoot = func(_ context.Context, project string, root tickets.Root) (tickets.FiledRoot, error) {
		filings <- filedRoot{project, root}
		return tickets.FiledRoot{Number: 1}, refused
	}
	return handler.Service, filings
}

// nextFiling is the next filing, or fails the test when none comes.
func nextFiling(t *testing.T, filings chan filedRoot) filedRoot {
	t.Helper()
	select {
	case filing := <-filings:
		return filing
	case <-time.After(3 * time.Second):
		t.Fatal("no root was filed")
	}
	return filedRoot{}
}

func TestARepeatingSupervisorErrorIsFiledOnceOnTheHomesRepository(t *testing.T) {
	// Arrange
	service, filings := filingBoard(t, nil)
	first := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	met := func(pid string, at time.Time) {
		service.fileRepeatedErrors(errors.New("reap: pid "+pid+" exited before it was read\nsomething else at once"), at)
	}

	// Act: met, met again within the hour, then past it, then again.
	met("4012", first)
	met("977", first.Add(30*time.Minute))
	met("31", first.Add(time.Hour))
	met("8", first.Add(2*time.Hour))

	// Assert: each of the two classes once, on the home's repository.
	keys := map[string]filedRoot{}
	for range 2 {
		filing := nextFiling(t, filings)
		keys[filing.root.RootKey()] = filing
	}
	filing, isFiled := keys["error:reap: pid n exited before it was read"]
	if _, isOther := keys["error:something else at once"]; !isFiled || !isOther || filing.project != service.Store.Home.Root {
		t.Fatalf("filings = %+v, want each class filed on the home's repository", keys)
	}
	if !strings.Contains(filing.root.Evidence, "First met 2026-10-14 18:00Z, met again 2026-10-14 19:00Z.") {
		t.Fatalf("evidence = %q", filing.root.Evidence)
	}
	select {
	case again := <-filings:
		t.Fatalf("filed again: %+v, want one filing a class in a run", again)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAFilingThatFailsTellsTheCFOAndIsNoErrorOfTheSupervisors(t *testing.T) {
	// Arrange
	service, filings := filingBoard(t, errors.New("refused: the issue would go on acme/widgets, which is public, and it names a path on this machine"))

	// Act
	service.fileFoundRoot(service.Store.Home.Root, tickets.RedMainRoot("go", "job go (rest)", "run 9"))
	nextFiling(t, filings)

	// Assert
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, record := range fleetWakeRecords(t, service.Store.Home, "check") {
			if strings.HasPrefix(record.Detail, "root_unfiled: go is red on the default branch: job go (rest) was not filed: refused:") {
				service.mu.Lock()
				lastError := service.lastError
				service.mu.Unlock()
				if lastError != "" {
					t.Fatalf("the failed filing became a supervisor error: %s", lastError)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the CFO was not told the root was not filed")
}

func TestNoRootIsFiledWithoutAFilingPath(t *testing.T) {
	handler, _ := orderBoard(t)
	handler.Service.fileRepeatedErrors(errors.New("x"), time.Now())
	handler.Service.fileFoundRoot("p", tickets.Root{Title: "x"})
	if handler.Service.errorClassesMet != nil {
		t.Fatal("classes were kept for a board that files nothing")
	}
}

func TestTheSupervisorsErrorWakeFilesALineMetAgainAnHourLater(t *testing.T) {
	// Arrange
	service, filings := filingBoard(t, nil)
	first := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)

	// Act
	service.wakeForNewErrors(errors.New("page sweep: read 3 pages failed"), first)
	service.wakeForNewErrors(errors.New("page sweep: read 5 pages failed"), first.Add(time.Hour))

	// Assert
	if filing := nextFiling(t, filings); filing.root.RootKey() != "error:page sweep: read n pages failed" {
		t.Fatalf("filing = %+v", filing)
	}
}
