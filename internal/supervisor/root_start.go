package supervisor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

var (
	rootBriefIdentity = regexp.MustCompile(`(?m)^Fix issue #(\d+) at its root: https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)\s*$`)
	rootTaskName      = regexp.MustCompile(`^root-.+-[0-9]+$`)
)

type rootStart struct {
	Task       string `json:"task"`
	Project    string `json:"project"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Problem    string `json:"problem,omitempty"`
	Number     int    `json:"number"`
}

type rootStartResult string

const (
	rootVerifiedOpen   rootStartResult = "open"
	rootVerifiedClosed rootStartResult = "closed"
	rootStateUnknown   rootStartResult = "unknown"
	rootStateOwned     rootStartResult = "owned"
	rootReadingsLimit                  = 128
)

type rootStartReading struct {
	Root   rootStart       `json:"identity"`
	At     time.Time       `json:"observed_at"`
	Result rootStartResult `json:"result"`
	Reason string          `json:"reason,omitempty"`
}

func (reading rootStartReading) current(root rootStart, now time.Time) bool {
	if reading.Root != root || root.Problem != "" || root.Task == "" || root.Project == "" || root.Repository == "" || root.Number <= 0 || root.Revision == "" || reading.At.IsZero() || now.Before(reading.At) || now.Sub(reading.At) >= rootPickupEvery {
		return false
	}
	switch reading.Result {
	case rootVerifiedOpen:
		return reading.Reason == ""
	case rootVerifiedClosed, rootStateUnknown, rootStateOwned:
		return reading.Reason != ""
	default:
		return false
	}
}

func (reading rootStartReading) refusal() StartRefusal {
	return StartRefusal{Reason: reading.Reason, Held: reading.Result == rootVerifiedClosed, Waits: reading.Reason != "" && reading.Result != rootVerifiedClosed}
}

// rootForStart recognizes persisted roots and the generated brief's own
// identity, never an issue number guessed from a task's name. A root-like
// name without identity needs reconciliation, not a guessed GitHub lookup.
func rootForStart(h home.Home, queued fleet.QueuedTask, plan startPlan) *rootStart {
	text, briefErr := fsx.ReadFile(plan.brief)
	match := rootBriefIdentity.FindStringSubmatch(string(text))
	isRoot := plan.isRoot || queued.IsBriefOnly && rootTaskName.MatchString(plan.id) || match != nil
	root := rootStart{Task: plan.id, Project: plan.project, Revision: fmt.Sprintf("%x", sha256.Sum256(text)) + queued.Revision}
	picked, pickedErr := readRootPickups(h.State)
	add := func(repository string, number int) {
		if repository == "" || number <= 0 {
			root.Problem = "its recorded identity is incomplete"
		} else if root.Repository != "" && (!strings.EqualFold(root.Repository, repository) || root.Number != number) {
			root.Problem = "its recorded identities disagree"
		} else {
			root.Repository, root.Number = repository, number
		}
	}
	for _, pickup := range picked {
		if pickup.Task == plan.id {
			isRoot = true
			root.Revision += fmt.Sprintf("/pickup:%s#%d@%s", pickup.Repository, pickup.Number, pickup.At.Format(time.RFC3339Nano))
			add(pickup.Repository, pickup.Number)
		}
	}
	var record tickets.Record
	recordErr := os.ErrNotExist
	if isRoot || rootTaskName.MatchString(plan.id) {
		record, recordErr = tickets.ReadRecord(h.State, plan.id)
		isRoot = isRoot || pickedErr != nil || !errors.Is(recordErr, os.ErrNotExist)
	}
	if !isRoot {
		return nil
	}
	if match != nil {
		number, numberErr := strconv.Atoi(match[1])
		urlNumber, urlErr := strconv.Atoi(match[3])
		if numberErr != nil || urlErr != nil || number != urlNumber {
			root.Problem = "its brief's issue identities disagree"
		} else {
			add(match[2], number)
		}
	}
	if recordErr == nil {
		root.Revision += fmt.Sprintf("/ticket:%s#%d/%s", record.Repository, record.Number, record.URL)
		add(record.Repository, record.Number)
		if record.URL != "" && !strings.EqualFold(record.URL, fmt.Sprintf("https://github.com/%s/issues/%d", record.Repository, record.Number)) {
			root.Problem = "its ticket's issue identities disagree"
		}
	} else if !errors.Is(recordErr, os.ErrNotExist) {
		root.Problem = "its ticket identity cannot be read: " + recordErr.Error()
	}
	if pickedErr != nil {
		root.Problem = "its pickup identity cannot be read: " + pickedErr.Error()
	}
	if briefErr != nil && !errors.Is(briefErr, os.ErrNotExist) {
		root.Problem = "its brief cannot be read: " + briefErr.Error()
	}
	if root.Repository == "" && root.Problem == "" {
		root.Problem = "it has no recorded issue identity"
	}
	return &root
}

func (root rootStart) refusal() string {
	return "Root issue needs reconciliation: " + root.Problem + "; nothing starts. Reconcile the root or queue new work under a new id"
}

// rootStartRefusal keeps GitHub off the scheduler and Start request. At most
// one bounded read runs at a time; all results, including failures, wait a
// pickup interval before another read. A changed brief or identity is new.
func (s *Service) rootStartRefusal(root rootStart) StartRefusal {
	if refusal, kind := rootLocalRefusal(s.Store.Home, root); refusal.Reason != "" {
		s.tellRootStart(root, kind, refusal.Reason)
		return refusal
	}
	now := time.Now().UTC()
	s.mu.Lock()
	read, isRead := s.rootStarts[root]
	if isRead && read.current(root, now) {
		s.mu.Unlock()
		if read.Result == rootVerifiedClosed {
			ctx, cancel := context.WithTimeout(context.Background(), ghCallTimeout)
			err := s.rootRepository(ctx, root)
			cancel()
			if err != nil {
				problem := root
				problem.Problem = bounded(err.Error(), 500)
				reason := problem.refusal()
				s.tellRootStart(root, "identity", reason)
				return StartRefusal{Reason: reason, Waits: true}
			}
		}
		return read.refusal()
	}
	if !s.isReadingRoot {
		s.isReadingRoot = true
		go s.readRootStart(root)
	}
	s.mu.Unlock()
	return StartRefusal{Reason: "Root issue awaits verification of its recorded identity; nothing starts until it is confirmed open", Waits: true}
}

func rootLocalRefusal(h home.Home, root rootStart) (StartRefusal, string) {
	if root.Problem != "" {
		return StartRefusal{Reason: root.refusal(), Waits: true}, "identity"
	}
	filers, err := liveRootFilers(h)
	if err != nil {
		root.Problem = "the live tasks' filed root issues cannot be read: " + err.Error()
		return StartRefusal{Reason: root.refusal(), Waits: true}, "identity"
	}
	if filer := filers[strings.ToLower(fmt.Sprintf("https://github.com/%s/issues/%d", root.Repository, root.Number))]; filer != "" {
		reason := fmt.Sprintf("Root issue %s#%d belongs to %s, the live task that filed it; nothing starts while it runs. Reconcile the root with %s or queue new work under a new id", root.Repository, root.Number, filer, filer)
		return StartRefusal{Reason: reason, Waits: true}, "owned"
	}
	return StartRefusal{}, ""
}

func (s *Service) readRootStart(root rootStart) {
	ctx, cancel := context.WithTimeout(context.Background(), ghCallTimeout)
	defer cancel()
	state, err := s.rootState(ctx, root)
	var closing []string
	if err == nil && state == "open" {
		closing, err = s.Options.RootIssues.ClosingPullRequests(ctx, root.Repository, root.Number)
	}
	reason, kind := "", state
	switch {
	case err != nil:
		kind = "unreadable"
		root.Problem = "its identity or GitHub state cannot be read: " + bounded(err.Error(), 500)
		reason = root.refusal()
	case state == "closed":
		reason = fmt.Sprintf("Root issue %s#%d is closed; nothing starts. Reconcile the old brief or queue new work under a new id", root.Repository, root.Number)
	case state != "open":
		root.Problem = fmt.Sprintf("%s#%d is %s, not a verified open issue", root.Repository, root.Number, state)
		reason = root.refusal()
	case len(closing) > 0:
		kind = "owned"
		reason = fmt.Sprintf("Root issue %s#%d is closed by the open pull request %s, whose task owns it; nothing starts while it is open. Reconcile the root with that task or queue new work under a new id", root.Repository, root.Number, strings.Join(closing, ", "))
	}
	// The cache key describes local evidence, not the result of the read.
	root.Problem = ""
	s.keepRootStart(root, kind, reason)
	s.mu.Lock()
	s.isReadingRoot = false
	s.mu.Unlock()
	s.notify()
}

func (s *Service) rootState(ctx context.Context, root rootStart) (string, error) {
	if err := s.rootRepository(ctx, root); err != nil {
		return "", err
	}
	return s.Options.RootIssues.IssueState(ctx, root.Repository, root.Number)
}

func (s *Service) rootRepository(ctx context.Context, root rootStart) error {
	reader := s.Options.RootIssues
	if reader == nil || reader.Checkout == nil || reader.Repository == nil || reader.IssueState == nil || reader.ClosingPullRequests == nil {
		return errors.New("the root issue reader is unavailable")
	}
	return recordedRootRepository(ctx, s.Store.Home, reader, root)
}

func recordedRootRepository(ctx context.Context, h home.Home, reader *RootIssues, root rootStart) error {
	checkout, err := reader.Checkout(root.Project)
	if err != nil {
		return err
	}
	manifest, _, err := project.LoadForCheckout(h.Data, checkout)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	repository := manifest.IssueRepository
	if repository == "" {
		repository, err = reader.Repository(ctx, checkout)
		if err != nil {
			return err
		}
	}
	if !strings.EqualFold(repository, root.Repository) {
		return fmt.Errorf("recorded issue repository %s differs from the project's issue repository %s", root.Repository, repository)
	}
	return nil
}

func (s *Service) keepRootStart(root rootStart, kind, reason string) {
	if reason != "" {
		s.tellRootStart(root, kind, reason)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rootStarts == nil {
		s.rootStarts = map[rootStart]rootStartReading{}
	}
	for previous := range s.rootStarts {
		if previous.Task == root.Task {
			delete(s.rootStarts, previous)
		}
	}
	result := rootStateUnknown
	switch kind {
	case "open":
		result = rootVerifiedOpen
	case "closed":
		result = rootVerifiedClosed
	case "owned":
		result = rootStateOwned
	}
	s.rootStarts[root] = rootStartReading{Root: root, At: time.Now().UTC(), Result: result, Reason: reason}
}

// Only checkFleet persists these readings. The independent turn reader may
// consume closure, but open evidence never gives it authority to start work.
func (s *Service) currentRootReadings() []rootStartReading {
	now := time.Now().UTC()
	var current []rootStartReading
	s.mu.Lock()
	for root, reading := range s.rootStarts {
		if reading.current(root, now) {
			current = append(current, reading)
		}
	}
	s.mu.Unlock()
	sort.Slice(current, func(i, j int) bool { return current[i].Root.Task < current[j].Root.Task })
	if len(current) > rootReadingsLimit {
		current = current[:rootReadingsLimit]
	}
	return current
}

func persistedRootStartReader(h home.Home) func(rootStart) StartRefusal {
	watched, readErr := readFleetWakes(h.State)
	reader := &RootIssues{
		Checkout:   func(name string) (string, error) { return project.Resolve(name, install.MachineProjectsRoot) },
		Repository: tickets.GitHub{Commands: execx.OSRunner{}}.RepositoryOf,
	}
	return func(root rootStart) StartRefusal {
		if refusal, _ := rootLocalRefusal(h, root); refusal.Reason != "" {
			return refusal
		}
		unknown := StartRefusal{Reason: "Root issue awaits verification of its recorded identity; nothing starts until it is confirmed open", Waits: true}
		if readErr != nil || len(watched.RootReadings) > rootReadingsLimit {
			return unknown
		}
		var found *rootStartReading
		for _, reading := range watched.RootReadings {
			if reading.Root.Task == root.Task {
				if found != nil || !reading.current(root, time.Now().UTC()) {
					return unknown
				}
				found = &reading
			}
		}
		if found == nil || found.Result == rootVerifiedOpen {
			return unknown
		}
		if found.Result == rootVerifiedClosed {
			ctx, cancel := context.WithTimeout(context.Background(), ghCallTimeout)
			err := recordedRootRepository(ctx, h, reader, root)
			cancel()
			if err != nil {
				root.Problem = bounded(err.Error(), 500)
				return StartRefusal{Reason: root.refusal(), Waits: true}
			}
		}
		return found.refusal()
	}
}

func (s *Service) tellRootStart(root rootStart, kind, reason string) {
	key := fmt.Sprintf("root-start/%x", sha256.Sum256([]byte(fmt.Sprintf("%+v/%s", root, kind))))
	_, isNew, err := wake.AppendFirst(s.Store.Home.State, key, "notify", root.Task, "root_reconciliation: "+root.Task+": "+reason)
	if err == nil && isNew {
		_, err = wake.PublishEpisode(s.Store.Home.State)
	}
	if err != nil {
		s.publish(err)
	}
}
