package train

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// toldOfLabel returns what the CFO was told about a label that could not be
// put on.
func (s *scratch) toldOfLabel() []string {
	var said []string
	for _, text := range s.cfo {
		if strings.Contains(text, "could not put the label") {
			said = append(said, text)
		}
	}
	return said
}

// A train that lands merges its own pull request, and the notes GitHub writes
// for a release list every merged pull request: asked for the next release's
// notes on 2026-10-10, it listed train pull request 650 beside the one pull
// request it landed. So each pull request a train opens wears the train's
// label, which a repository's .github/release.yml leaves out of those notes.
// A repository's first train finds no such label and makes it, once.
func TestEveryPullRequestATrainOpensWearsItsLabel(t *testing.T) {
	t.Parallel()
	// Arrange: a red run nobody broke, so each half passes on a pull request
	// of its own.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(71, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(72, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	gh.breaks = []string{second.HeadRefOid}
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	wornAtTheStart := slices.Clone(gh.wears[gh.trainURL])
	s.step(engine, started.ID)
	s.step(engine, started.ID)
	gh.breaks = nil

	// Act
	s.step(engine, started.ID)
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || len(gh.opened) != 2 {
		t.Fatalf("train %s with pull requests %v, want it landed on one for each half", landed.State, gh.opened)
	}
	if !slices.Equal(wornAtTheStart, []string{OwnLabel}) {
		t.Errorf("its pull request wore %q once it was open, want %q from the start", wornAtTheStart, OwnLabel)
	}
	for _, url := range gh.opened {
		if got := gh.wears[url]; gh.reads(url) != "MERGED" || !slices.Equal(got, []string{OwnLabel}) {
			t.Errorf("%s reads %s and wears %q, want it merged wearing %q", url, gh.reads(url), got, OwnLabel)
		}
	}
	if len(gh.wears) != len(gh.opened) {
		t.Errorf("labelled %v, want the train's own pull requests alone", gh.wears)
	}
	if !gh.labels[OwnLabel] || gh.labelCalls() != 1 {
		t.Errorf("the repository has the label %v after %d makings, want it made once by its first pull request", gh.labels[OwnLabel], gh.labelCalls())
	}
	if said := s.toldOfLabel(); len(said) != 0 {
		t.Errorf("CFO told %q, want nothing said of a label that was put on", said)
	}
}

// A repository that already has the label keeps it as it is: its owner may
// have given it a colour, and a train writes to a repository's labels only
// when its own is missing.
func TestATrainLeavesALabelTheRepositoryAlreadyHasAsItIs(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	gh.labels[OwnLabel] = true
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || !slices.Equal(gh.wears[gh.trainURL], []string{OwnLabel}) {
		t.Fatalf("train %s, its pull request wears %q: want it landed wearing %q", landed.State, gh.wears[gh.trainURL], OwnLabel)
	}
	if made := gh.labelCalls(); made != 0 {
		t.Fatalf("the label was written %d times, want a label the repository has left alone", made)
	}
}

// The label matters on a pull request that merges, so a train makes sure of
// it again before it merges one: a train in flight when this build is
// installed has a pull request an older build opened without it, and a label
// can be taken off by hand.
func TestAPullRequestATrainMergesWearsItsLabelWhoeverOpenedIt(t *testing.T) {
	t.Parallel()
	for name, arrange := range map[string]func(t *testing.T, s *scratch, gh *fakeGitHub, id string){
		"an older build opened it": func(t *testing.T, s *scratch, gh *fakeGitHub, id string) {
			olderBuild(t, s, gh, id, nil)
			delete(gh.labels, OwnLabel)
		},
		"its label was taken off by hand": func(_ *testing.T, _ *scratch, gh *fakeGitHub, _ string) {
			delete(gh.wears, gh.trainURL)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s := newScratch(t)
			gh := newFakeGitHub(s)
			first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
			riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
			engine := s.engine(gh)
			started, err := engine.Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			arrange(t, s, gh, started.ID)

			// Act
			landed := s.step(engine, started.ID)

			// Assert
			if landed.State != StateLanded || gh.reads(gh.trainURL) != "MERGED" {
				t.Fatalf("train %s, its pull request reads %s: want it landed and merged", landed.State, gh.reads(gh.trainURL))
			}
			if got := gh.wears[gh.trainURL]; !slices.Equal(got, []string{OwnLabel}) {
				t.Fatalf("the pull request it merged wears %q, want %q", got, OwnLabel)
			}
			if said := s.toldOfLabel(); len(said) != 0 {
				t.Fatalf("CFO told %q, want nothing said of a label that was put on", said)
			}
		})
	}
}

// The label is housekeeping, and a train never stops for it: where GitHub
// refuses the label the train lands all the same, and the CFO is told which
// pull request merged without it and how to put it on, when it opened and
// again when it merged.
func TestATrainThatCannotLabelItsPullRequestLandsAndSaysSo(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	gh.noLabels = "HTTP 403: Resource not accessible by personal access token"
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || landed.Errors != 0 || gh.reads(first.URL) != "MERGED" || gh.reads(second.URL) != "MERGED" || s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatalf("train %s after %d failed steps, #11 reads %s and #12 %s: want it landed with main at the tree CI tested", landed.State, landed.Errors, gh.reads(first.URL), gh.reads(second.URL))
	}
	said := s.toldOfLabel()
	if len(said) != 2 || len(gh.wears) != 0 {
		t.Fatalf("CFO told %q, labelled %v: want it said at the opening and at the merge, with no label on", said, gh.wears)
	}
	for _, text := range said {
		for _, want := range []string{"merge_train: o/r train " + started.ID, OwnLabel, gh.trainURL, "HTTP 403", "gh pr edit " + gh.trainURL + " --add-label " + OwnLabel} {
			if !strings.Contains(text, want) {
				t.Errorf("CFO told %q, want %q", text, want)
			}
		}
		if strings.ContainsAny(text, ";—") {
			t.Errorf("CFO told %q, want no semicolon or long dash in what he reads", text)
		}
	}
}
