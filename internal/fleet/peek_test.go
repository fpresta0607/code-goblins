package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

func TestPeekerTailDefaultsToFortyAndUsesHerdrCaptureFloor(t *testing.T) {
	output := numberedLines(50)
	runner := &fakeRunner{replies: []runnerReply{rawReply(output)}}
	var clientSleeps []time.Duration
	peeker := Peeker{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}},
		Herdr:   newHerdrClient(runner, &clientSleeps),
	}

	got, err := peeker.Tail(context.Background(), "task-7", 0)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := numberedLinesFrom(11, 50); got != want {
		t.Errorf("Tail default output = %q, want %q", got, want)
	}
	assertRequests(t, runner.requests, [][]string{{"pane", "read", "pane-7", "--source", "recent", "--lines", "200", "--session", "fleet"}})
}

func TestPeekerTailFallsBackToTwoHundredForInvalidLineCount(t *testing.T) {
	output := numberedLines(205)
	runner := &fakeRunner{replies: []runnerReply{rawReply(output)}}
	var clientSleeps []time.Duration
	peeker := Peeker{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}},
		Herdr:   newHerdrClient(runner, &clientSleeps),
	}

	got, err := peeker.Tail(context.Background(), "task-7", -1)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := numberedLinesFrom(6, 205); got != want {
		t.Errorf("Tail invalid fallback output = %q, want %q", got, want)
	}
	assertRequests(t, runner.requests, [][]string{{"pane", "read", "pane-7", "--source", "recent", "--lines", "200", "--session", "fleet"}})
}

func TestPeekerTailPassesExactLineCountAndReturnsOnlyLocalTail(t *testing.T) {
	output := numberedLines(255)
	runner := &fakeRunner{replies: []runnerReply{rawReply(output)}}
	var clientSleeps []time.Duration
	peeker := Peeker{
		Resolve: &fakeResolver{target: herdr.Target{Session: "fleet", Pane: "pane-7"}},
		Herdr:   newHerdrClient(runner, &clientSleeps),
	}

	got, err := peeker.Tail(context.Background(), "task-7", 250)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := numberedLinesFrom(6, 255); got != want {
		t.Errorf("Tail exact output = %q, want %q", got, want)
	}
	assertRequests(t, runner.requests, [][]string{{"pane", "read", "pane-7", "--source", "recent", "--lines", "250", "--session", "fleet"}})
}

func TestPeekerRequiresCollaborators(t *testing.T) {
	_, err := (Peeker{}).Tail(context.Background(), "task-7", 40)
	assertErrorContains(t, err, "resolver")
	_, err = (Peeker{Resolve: &fakeResolver{}}).Tail(context.Background(), "task-7", 40)
	assertErrorContains(t, err, "Herdr")
}

func numberedLines(count int) string {
	return numberedLinesFrom(1, count)
}

func numberedLinesFrom(start, end int) string {
	var out strings.Builder
	for line := start; line <= end; line++ {
		fmt.Fprintf(&out, "line-%03d\n", line)
	}
	return out.String()
}
