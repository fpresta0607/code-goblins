package supervisor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

var (
	rootBriefIdentity = regexp.MustCompile(`(?m)^Fix issue #(\d+) at its root: https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)\s*$`)
	rootTaskName      = regexp.MustCompile(`^root-.+-[0-9]+$`)
)

type rootStart struct {
	task, project, repository, revision, problem string
	number                                       int
}

type rootStartReading struct {
	at     time.Time
	reason string
}

// rootForStart recognizes persisted roots and the generated brief's own
// identity, never an issue number guessed from a task's name. A root-like
// name without identity needs reconciliation, not a guessed GitHub lookup.
func rootForStart(h home.Home, queued fleet.QueuedTask, plan startPlan) *rootStart {
	text, briefErr := fsx.ReadFile(plan.brief)
	match := rootBriefIdentity.FindStringSubmatch(string(text))
	isRoot := plan.isRoot || queued.IsBriefOnly && rootTaskName.MatchString(plan.id) || match != nil
	root := rootStart{task: plan.id, project: plan.project, revision: fmt.Sprintf("%x", sha256.Sum256(text)) + queued.Revision}
	picked, pickedErr := readRootPickups(h.State)
	add := func(repository string, number int) {
		if repository == "" || number <= 0 {
			root.problem = "its recorded identity is incomplete"
		} else if root.repository != "" && (!strings.EqualFold(root.repository, repository) || root.number != number) {
			root.problem = "its recorded identities disagree"
		} else {
			root.repository, root.number = repository, number
		}
	}
	for _, pickup := range picked {
		if pickup.Task == plan.id {
			isRoot = true
			add(pickup.Repository, pickup.Number)
		}
	}
	var record tickets.Record
	recordErr := os.ErrNotExist
	if isRoot || rootTaskName.MatchString(plan.id) {
		record, recordErr = tickets.ReadRecord(h.State, plan.id)
		isRoot = isRoot || !errors.Is(recordErr, os.ErrNotExist)
	}
	if !isRoot {
		return nil
	}
	if match != nil {
		number, numberErr := strconv.Atoi(match[1])
		urlNumber, urlErr := strconv.Atoi(match[3])
		if numberErr != nil || urlErr != nil || number != urlNumber {
			root.problem = "its brief's issue identities disagree"
		} else {
			add(match[2], number)
		}
	}
	if recordErr == nil {
		add(record.Repository, record.Number)
	} else if !errors.Is(recordErr, os.ErrNotExist) {
		root.problem = "its ticket identity cannot be read: " + recordErr.Error()
	}
	if pickedErr != nil {
		root.problem = "its pickup identity cannot be read: " + pickedErr.Error()
	}
	if briefErr != nil && !errors.Is(briefErr, os.ErrNotExist) {
		root.problem = "its brief cannot be read: " + briefErr.Error()
	}
	if root.repository == "" && root.problem == "" {
		root.problem = "it has no recorded issue identity"
	}
	return &root
}

func (root rootStart) refusal() string {
	return "Root issue needs reconciliation: " + root.problem + "; nothing starts. Reconcile the root or queue new work under a new id"
}

// rootStartRefusal keeps GitHub off the scheduler and Start request. At most
// one bounded read runs at a time; all results, including failures, wait a
// pickup interval before another read. A changed brief or identity is new.
func (s *Service) rootStartRefusal(root rootStart) string {
	if root.problem != "" {
		reason := root.refusal()
		s.tellRootStart(root, "identity", reason)
		return reason
	}
	now := time.Now().UTC()
	s.mu.Lock()
	read, isRead := s.rootStarts[root]
	if isRead && now.Sub(read.at) < rootPickupEvery {
		s.mu.Unlock()
		return read.reason
	}
	if !s.isReadingRoot {
		s.isReadingRoot = true
		go s.readRootStart(root)
	}
	s.mu.Unlock()
	return "Root issue awaits verification of its recorded identity; nothing starts until it is confirmed open"
}

func (s *Service) readRootStart(root rootStart) {
	ctx, cancel := context.WithTimeout(context.Background(), ghCallTimeout)
	defer cancel()
	state, err := s.rootState(ctx, root)
	reason, kind := "", state
	switch {
	case err != nil:
		kind = "unreadable"
		root.problem = "its identity or GitHub state cannot be read: " + bounded(err.Error(), 500)
		reason = root.refusal()
	case state == "closed":
		reason = fmt.Sprintf("Root issue %s#%d is closed; nothing starts. Reconcile the old brief or queue new work under a new id", root.repository, root.number)
	case state != "open":
		root.problem = fmt.Sprintf("%s#%d is %s, not a verified open issue", root.repository, root.number, state)
		reason = root.refusal()
	}
	// The cache key describes local evidence, not the result of the read.
	root.problem = ""
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
	return s.Options.RootIssues.IssueState(ctx, root.repository, root.number)
}

func (s *Service) rootRepository(ctx context.Context, root rootStart) error {
	reader := s.Options.RootIssues
	if reader == nil || reader.Checkout == nil || reader.Repository == nil || reader.IssueState == nil {
		return errors.New("the root issue reader is unavailable")
	}
	checkout, err := reader.Checkout(root.project)
	if err != nil {
		return err
	}
	manifest, _, err := project.LoadForCheckout(s.Store.Home.Data, checkout)
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
	if !strings.EqualFold(repository, root.repository) {
		return fmt.Errorf("recorded issue repository %s differs from the project's issue repository %s", root.repository, repository)
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
		if previous.task == root.task {
			delete(s.rootStarts, previous)
		}
	}
	s.rootStarts[root] = rootStartReading{at: time.Now().UTC(), reason: reason}
}

func (s *Service) tellRootStart(root rootStart, kind, reason string) {
	key := fmt.Sprintf("root-start/%x", sha256.Sum256([]byte(fmt.Sprintf("%+v/%s", root, kind))))
	_, isNew, err := wake.AppendFirst(s.Store.Home.State, key, "notify", root.task, "root_reconciliation: "+root.task+": "+reason)
	if err == nil && isNew {
		_, err = wake.PublishEpisode(s.Store.Home.State)
	}
	if err != nil {
		s.publish(err)
	}
}
