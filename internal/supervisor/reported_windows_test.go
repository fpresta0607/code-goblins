package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-02, in the desktop window: "it does seem like alert
// sent, complete delays can be improved to avoid lagging notifications". A
// goblin's report told the supervisor nothing, so the board learned of it at
// its next refresh, up to fifteen seconds later, and then waited for a
// snapshot that took 3 to 13 seconds to build. His budget: from a goblin's
// notify to its alert on the board, under one second at the live fleet's
// size, a dozen goblins and a few hundred state files.
func TestAGoblinsReportReachesAnOpenBoardWithinASecondAtTheLiveFleetsSize(t *testing.T) {
	// Arrange: a running supervisor over a fleet of the live size, and one
	// board open on it with its first snapshot.
	_, h := liveSizedFleet(t)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	defer s.Close()
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	snapshots := make(chan Snapshot, 16)
	go func() {
		reader := bufio.NewReaderSize(response.Body, 4<<20)
		name := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch line = strings.TrimRight(line, "\n"); {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && name == "snapshot":
				var snapshot Snapshot
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot) == nil {
					snapshots <- snapshot
				}
			}
		}
	}()
	select {
	case <-snapshots:
	case <-time.After(30 * time.Second):
		t.Fatal("the board got no first snapshot")
	}
	// A supervisor that just started publishes several times, and a report
	// made then would ride on one of those: the board is left to go quiet.
	for quiet := false; !quiet; {
		select {
		case <-snapshots:
		case <-time.After(3 * time.Second):
			quiet = true
		}
	}

	// Act and assert. The budget is the path's: the machine that runs this
	// is often busy with other tests, which only ever adds time, so the
	// fastest report must meet it. A report that waits for something the
	// supervisor does by the clock never does: each one is made five seconds
	// or more after a refresh and four or more before the next, and all of
	// them before the supervisor's first minute is up, when it reconciles and
	// rebuilds its history.
	const budget = time.Second
	var took []time.Duration
	for {
		since := time.Since(started)
		if phase := since % snapshotRefresh; phase < 5*budget {
			time.Sleep(5*budget - phase)
			continue
		} else if phase > snapshotRefresh-4*budget {
			time.Sleep(snapshotRefresh - phase)
			continue
		}
		if since > 3*snapshotRefresh-4*budget {
			break
		}
		activity := fmt.Sprintf("working: report %d", len(took))
		reported := time.Now()
		if err := state.AppendStatus(h.State, "goblin-03", activity); err != nil {
			t.Fatal(err)
		}
		Reported(h.State)
		for shown := false; !shown; {
			select {
			case snapshot := <-snapshots:
				shown = slices.ContainsFunc(snapshot.Tasks, func(task Task) bool { return task.ID == "goblin-03" && task.Activity == activity })
			case <-time.After(30 * time.Second):
				t.Fatalf("report %d never reached the board", len(took))
			}
		}
		took = append(took, time.Since(reported).Round(time.Millisecond))
		if took[len(took)-1] <= budget {
			t.Logf("a report reached the board after %s", took)
			return
		}
	}
	t.Errorf("the reports made in the supervisor's first %s reached the board after %s, want one within %s", 3*snapshotRefresh, took, budget)
}

// A report is news of a goblin, not of the supervisor: the error the board
// shows stays until the cycle that met it runs clean.
func TestAReportLeavesTheBoardsErrorAsItWas(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// settled waits until the supervisor has told its boards nothing for a
	// while, and returns the revision it stopped at.
	settled := func(quiet time.Duration) uint64 {
		revision := s.Revision()
		for since := time.Now(); time.Since(since) < quiet; time.Sleep(20 * time.Millisecond) {
			if now := s.Revision(); now != revision {
				revision, since = now, time.Now()
			}
		}
		return revision
	}
	settled(2 * time.Second)
	s.publish(errors.New("the disk is full"))
	before := s.Revision()

	// Act
	Reported(h.State)

	// Assert: once the supervisor has done all the report makes it do.
	for deadline := time.Now().Add(5 * time.Second); s.Revision() == before; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the report told the boards nothing")
		}
	}
	settled(500 * time.Millisecond)
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Error != "the disk is full" {
		t.Errorf("the board's error after a report = %q, want it kept", snapshot.Error)
	}
}
