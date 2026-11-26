package supervisor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// Claude engine catalog defaults, separate from automatic task routing.
const (
	defaultModel  = "claude-opus-5-5"
	defaultEffort = "xhigh"
)

var (
	spawnHarnesses = []string{"claude", "codex", "pi"}
	spawnModes     = []string{"no-mistakes", "direct-PR", "local-only"}
	spawnValue     = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]~-]{1,200}$`)
)

// Dispatch is what a queued task's Start reads and runs on this machine:
// its memory, and goblins spawn itself. Without it the board starts no goblin.
type Dispatch struct {
	Idle func(context.Context, state.TaskMeta) (bool, error)
	// Memory reads the machine's physical memory, commit and kernel pools.
	Memory func() (Memory, error)
	// PoolTags reads what each of the kernel's pool tags holds, for the
	// record of the pools (kernel_pools.go); nil keeps no record.
	PoolTags func() ([]PoolTag, error)
	// Disk reads the free space of the home's drive and the disk floor; nil
	// reads the machine's.
	Disk func() (Disk, error)
	// CommitHolders names the apps holding the most commit.
	CommitHolders func() ([]CommitHolder, error)
	// TopHandleHolder names the process holding the most handles open; nil
	// reads none, and no process past the handle line wakes the CEO.
	TopHandleHolder func() (HandleHolder, error)
	// Processors reads how free the machine's performance cores are; nil
	// reads none, and nothing waits on them.
	Processors func() (Processors, error)
	// GPU reads the machine's graphics adapters and how busy each is; nil
	// reads none.
	GPU func() (GPU, error)
	// Processes lists the machine's processes with their private working sets,
	// for what a goblin that just started still has to take of memory
	// (ReadRoom); nil reads none, and such a goblin counts its whole start.
	Processes func() ([]fleettree.Process, error)
	// HeavyLine reads the room the machine's heavy line keeps at the given
	// reading of memory, whose Hold says why no goblin starts or resumes by
	// itself, as SlotHold says it, or "" when one may; nil reads none, and
	// nothing waits on it.
	HeavyLine func(Memory) (LineRoom, error)
	// LineStall says how the machine's heavy line stands still at the given
	// reading of memory, a turn free, runs waiting and memory enough for the
	// one at its head, as MachineLineStall says it, or "" when it does not;
	// nil reads none.
	LineStall func(Memory) (string, error)
	// LongTurnWaits reads the goblins' own turns that have waited in the
	// machine's heavy line longTurnWait or longer at the given moment, as
	// MachineLongTurnWaits reads them; nil reads none.
	LongTurnWaits func(now time.Time) ([]verify.Standing, error)
	// TurnHolders reads the tasks whose goblins hold a turn of the machine's
	// heavy line, as MachineTurnHolders reads them, which the memory floor
	// pauses last; nil reads none, and the floor spares no goblin for a turn.
	TurnHolders func() ([]string, error)
	// Spawn runs goblins with args and returns what it printed.
	Spawn func(ctx context.Context, args []string) (string, error)
}

// Memory is the machine's available physical memory and available commit,
// in bytes, beside the fleet's floor, under which nothing starts, and the free
// memory at which the next start of the costliest harness is admitted: a
// start needs both to reach them.
// The kernel's pools ride along, since a pool that keeps growing is a driver
// leaking memory, with the line past which the PC is better restarted
// (pc_restart.go) and the tags that grew most since the sign-in began, and so
// do the apps holding the most commit once commit is the tighter of the two.
// SlotHold is why the supervisor starts and resumes no goblin by itself at
// this reading, keeping the test slots open, or empty when it may or the
// machine's heavy line is not read. HandleHolder is the process holding the
// most handles open, whose leak fills the kernel's paged pool. Guard is the
// reading the memory guard last acted on.
type Memory struct {
	Available       uint64         `json:"available"`
	Total           uint64         `json:"total"`
	CommitAvailable uint64         `json:"commit_available"`
	CommitLimit     uint64         `json:"commit_limit"`
	PagedPool       uint64         `json:"paged_pool"`
	NonpagedPool    uint64         `json:"nonpaged_pool"`
	PoolLine        uint64         `json:"pool_line"`
	PoolGrowth      []PoolGrowth   `json:"pool_growth,omitempty"`
	Floor           uint64         `json:"floor"`
	Next            uint64         `json:"next"`
	SlotHold        string         `json:"slot_hold"`
	Holders         []CommitHolder `json:"holders,omitempty"`
	HandleHolder    *HandleHolder  `json:"handle_holder,omitempty"`
	Guard           *GuardReading  `json:"guard,omitempty"`
}

// GuardReading is the reading under the floor that the memory guard last acted
// on: when it read, the free memory and commit it read, the floor, and what it
// did about it.
type GuardReading struct {
	At              time.Time `json:"at"`
	Available       uint64    `json:"available"`
	CommitAvailable uint64    `json:"commit_available"`
	Floor           uint64    `json:"floor"`
	Action          string    `json:"action"`
}

// startPlan is the goblins spawn a Start runs.
type startPlan struct {
	id, project, brief, harness, model, effort, mode string
	briefRevision                                    string
	missingBrief                                     *fleet.QueuedTask
	isOverlord                                       bool
	isProductionDefect                               bool
	// isRoot is a root issue the supervisor picked up, which starts behind
	// every other queued task.
	isRoot bool
	root   *rootStart
}

func (p startPlan) args() []string {
	args := []string{"spawn", p.id, "--project", p.project, "--brief", p.brief}
	for _, flag := range [][2]string{{"--harness", p.harness}, {"--model", p.model}, {"--effort", p.effort}, {"--mode", p.mode}} {
		if flag[1] != "" {
			args = append(args, flag[0], flag[1])
		}
	}
	if p.briefRevision != "" {
		args = append(args, "--brief-revision", p.briefRevision)
	}
	return args
}

// startTask serves POST /api/tasks/start: the Overlord's Start on a queued
// task, which dispatches it through goblins spawn as the CEO would.
func (h *HTTP) startTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Task string `json:"task"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if state.ValidTaskID(input.Task) != nil {
		apiError(w, http.StatusBadRequest, "Invalid task ID")
		return
	}
	if err := h.Service.startTask(input.Task); err != nil {
		var refusal StartRefusal
		if !errors.As(err, &refusal) {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respond(w, http.StatusConflict, struct {
			Error   string `json:"error"`
			Passing bool   `json:"passing"`
		}{bounded(refusal.Reason, 1500), refusal.Passing})
		return
	}
	h.Service.notify()
	// Every snapshot from this revision on shows this start, not a failure
	// of the last one.
	h.Service.mu.Lock()
	revision := h.Service.revision
	h.Service.mu.Unlock()
	respond(w, http.StatusAccepted, struct {
		Starting bool   `json:"starting"`
		Revision uint64 `json:"revision"`
	}{true, revision})
}

// startTask checks that id is queued work that can start and files its Start
// to run in its turn: one goblin starts or resumes at a time, and only with
// memory and disk to spare, and the board shows it starting meanwhile. A
// Start clicked again while it starts or waits changes nothing.
func (s *Service) startTask(id string) error {
	if s.Options.Dispatch == nil {
		return StartRefusal{Reason: "This board cannot start goblins"}
	}
	s.starts.Lock()
	isUnderWay := s.starting == id || s.isAsked(id)
	s.starts.Unlock()
	if isUnderWay {
		return nil
	}
	// The Start reads no memory itself: its turn does, as it starts.
	if _, err := planStart(s.Store.Home, id, s.finishedWork(), s.rowReading(time.Now().UTC(), nil)); err != nil {
		return err
	}
	s.ask(askedChange{task: id})
	return nil
}

func (s *Service) startQueued(id string, isOverlord bool) error {
	dispatch := s.Options.Dispatch
	if dispatch == nil {
		return StartRefusal{Reason: "This board cannot start goblins"}
	}
	s.starts.Lock()
	if s.startErrors == nil {
		s.startErrors = map[string]string{}
	}
	if s.starting != "" {
		starting := s.starting
		s.starts.Unlock()
		return StartRefusal{Reason: starting + " is starting; start another once it is up", Passing: true}
	}
	for task, action := range s.changing {
		if action == "resume" {
			s.starts.Unlock()
			return StartRefusal{Reason: task + " is resuming; start another once it is up", Passing: true}
		}
	}
	if s.changing[id] != "" {
		s.starts.Unlock()
		return StartRefusal{Reason: "This task is being changed", Passing: true}
	}
	s.starting = id
	s.overlordStart = ""
	if isOverlord {
		s.overlordStart = id
	}
	s.starts.Unlock()
	isStarting := false
	defer func() {
		if !isStarting {
			s.starts.Lock()
			s.starting = ""
			s.starts.Unlock()
			s.notify()
		}
	}()
	queueLock := ".queued-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(s.Store.Home.State, queueLock); err != nil {
		return StartRefusal{Reason: "This queued task is being changed; try again", Passing: true}
	}
	defer func() {
		if !isStarting {
			if err := lock.ReleaseExclusiveNamed(s.Store.Home.State, queueLock); err != nil {
				s.publish(err)
			}
		}
	}()
	memory, err := dispatch.Memory()
	if err != nil {
		return StartRefusal{Reason: "Free memory cannot be read, so nothing starts: " + err.Error()}
	}
	plan, err := planStart(s.Store.Home, id, s.finishedWork(), s.rowReading(time.Now().UTC(), &memory))
	if err != nil {
		return err
	}
	plan.isOverlord = isOverlord
	if plan.root != nil {
		// GitHub's answer may be cached, but the local project mapping must
		// still match at the boundary before any worktree or harness exists.
		ctx, cancel := context.WithTimeout(context.Background(), ghCallTimeout)
		err := s.rootRepository(ctx, *plan.root)
		cancel()
		if err != nil {
			problem := *plan.root
			problem.Problem = bounded(err.Error(), 500)
			reason := problem.refusal()
			s.keepRootStart(*plan.root, "identity", reason)
			return StartRefusal{Reason: reason, Waits: true}
		}
	}
	room, err := s.room(time.Now().UTC())
	if err != nil {
		return StartRefusal{Reason: "The room for a start cannot be read, so nothing starts: " + err.Error()}
	}
	disk, err := s.machineDisk()
	if err != nil {
		return StartRefusal{Reason: "Free disk cannot be read, so nothing starts: " + err.Error()}
	}
	if err := CheckLaunch(memory, room, plan.harness, disk); err != nil {
		return StartRefusal{Reason: err.Error(), Passing: true}
	}
	if plan.missingBrief != nil {
		if err := fleet.WriteQueuedBrief(s.Store.Home, *plan.missingBrief); err != nil {
			return err
		}
		content, err := fsx.ReadFile(plan.brief)
		if err != nil {
			return err
		}
		if err := fleet.ValidateBriefReadiness(content); err != nil {
			return err
		}
		plan.briefRevision = fmt.Sprintf("brief:%x", sha256.Sum256(content))
		if _, err := wake.Append(s.Store.Home.State, "notify", id, "brief created: Start wrote data/"+id+"/brief.md from the queued task; the CEO can amend it"); err != nil {
			return err
		}
	}
	isStarting = true
	s.starts.Lock()
	delete(s.startErrors, id)
	s.starts.Unlock()
	// The board shows the start at once, whoever made it: the scheduler's
	// reading tells it only when its line changes.
	s.notify()
	go s.runStart(dispatch, plan)
	return nil
}

// runStart runs goblins spawn to its end, which confirms the goblin works before
// it returns, so it is never cut short: a killed spawn strands its task.
// Either way the CEO is told through the wake queue, as a notify tells it,
// before the card stops showing the start.
func (s *Service) runStart(dispatch *Dispatch, plan startPlan) {
	defer func() {
		if err := lock.ReleaseExclusiveNamed(s.Store.Home.State, ".queued-"+plan.id+".lock"); err != nil {
			s.publish(err)
		}
	}()
	launched := make(chan struct{})
	go s.watchLaunch(plan.id, launched)
	output, err := s.runPastTheSpawnLock(dispatch, plan.args())
	close(launched)
	failure := ""
	if err != nil {
		failure = spawnFailure(output, err)
	}
	isBriefWaiting := err != nil && strings.Contains(failure, fleet.ErrBriefNotReady.Error())
	// A start the network failed is tried again by the scheduler after a wait
	// that doubles with each try, since DNS and GitHub came and went for
	// minutes at a time on 2026-11-25, until networkStartTries tries have
	// failed (#2905). Any other outcome ends the retries.
	s.starts.Lock()
	retry := s.startRetries[plan.id]
	delete(s.startRetries, plan.id)
	if failure != "" && !isBriefWaiting && tickets.IsLostConnection(failure) && retry.tries+1 < networkStartTries {
		retry.tries++
		retry.at = time.Now().UTC().Add(min(time.Minute<<(retry.tries-1), 30*time.Minute))
		if s.startRetries == nil {
			s.startRetries = map[string]startRetry{}
		}
		s.startRetries[plan.id] = retry
		failure += "; the network could not be reached, so it tries again by itself at " + retry.at.Format("15:04Z")
	}
	s.starts.Unlock()
	h := s.Store.Home
	detail := "start failed: " + failure
	if isBriefWaiting {
		detail = "start deferred: " + failure
		failure = ""
	}
	if err == nil {
		// Started now from the board, it goes to the top of In progress. One
		// the supervisor started by itself joins the bottom, under every
		// goblin the board lists, so his order above it stands. Either way it
		// is under the lock an order the Overlord saves meanwhile takes.
		s.ordering.Lock()
		var order []string
		var readErr error
		if plan.isOverlord {
			order, readErr = fleet.ReadAttention(h)
			order = append([]string{plan.id}, slices.DeleteFunc(order, func(id string) bool { return id == plan.id })...)
		} else {
			var tasks []state.TaskMeta
			tasks, readErr = inProgressOrder(h)
			for _, meta := range tasks {
				if meta.ID != plan.id {
					order = append(order, meta.ID)
				}
			}
			order = append(order, plan.id)
		}
		if readErr == nil {
			readErr = fleet.WriteAttention(h, order)
		}
		s.ordering.Unlock()
		if readErr != nil {
			s.publish(readErr)
		}
		detail = "started: the Overlord started this from the board, at the top of In progress; its row is still in backlog.md's Queued section"
		if !plan.isOverlord {
			detail = "started: the supervisor started the next queued task after checking cleared pauses, at the bottom of In progress; its row is still in backlog.md's Queued section"
		}
		if plan.isProductionDefect {
			detail += "; production defect jumped the order"
		}
	}
	if _, err := wake.Append(h.State, "notify", plan.id, detail); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(h.State); err != nil {
		s.publish(err)
	}
	s.starts.Lock()
	s.starting = ""
	if failure != "" {
		s.startErrors[plan.id] = failure
	}
	s.starts.Unlock()
	s.notify()
	s.runAsked()
}

// networkStartTries is how many times a start the network keeps failing is
// tried before it waits for a Start: about two and a half hours of waits.
const networkStartTries = 10

// startRetry is when the scheduler tries again a start the network failed,
// and how many tries it has had again so far.
type startRetry struct {
	at    time.Time
	tries int
}

// spawnFailure is why goblins spawn failed: the last line it printed, which is
// its refusal, or the error when it printed nothing.
func spawnFailure(output string, err error) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return bounded(last, 500)
	}
	return bounded(err.Error(), 500)
}

// planStart is the goblins spawn a Start of id runs, or why it cannot: id must be
// queued work with a brief and a project, not already running and not
// already finished. Harness, model and effort come from the backlog row, then
// the brief's Delivery section, and mode from that section alone, since it is
// the one readiness validated. Unspecified engine settings are routed by
// spawn.
func planStart(h home.Home, id string, finished *finishedWork, reading rowReading) (startPlan, error) {
	if _, err := os.Stat(filepath.Join(h.State, id+".meta")); err == nil {
		return startPlan{}, StartRefusal{Reason: id + " already runs; open it from In progress", Held: true}
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		return startPlan{}, err
	}
	queued, err := backlog.ReadQueuedTask(h, id)
	if errors.Is(err, fleet.ErrNotQueued) {
		return startPlan{}, StartRefusal{Reason: id + " is not queued", Held: true}
	}
	if err != nil {
		return startPlan{}, err
	}
	if refusal := finished.refusal(id); refusal != "" {
		return startPlan{}, StartRefusal{Reason: refusal, Held: true}
	}
	row := queued.Row
	brief := filepath.Join(h.Data, id, "brief.md")
	_, briefErr := os.Stat(brief)
	if briefErr != nil && !errors.Is(briefErr, os.ErrNotExist) {
		return startPlan{}, briefErr
	}
	if waits := rowWaits(h, backlog, row, reading); len(waits) > 0 {
		return startPlan{}, waitRefusal(id, row, waits)
	}
	var briefLines []string
	var named map[string]string
	var content []byte
	briefRevision := ""
	if briefErr == nil {
		content, err = fsx.ReadFile(brief)
		if err != nil {
			return startPlan{}, err
		}
		if err := fleet.ValidateBriefReadiness(content); err != nil {
			return startPlan{}, StartRefusal{Reason: err.Error(), Passing: true, Held: true}
		}
		briefRevision = fmt.Sprintf("brief:%x", sha256.Sum256(content))
		if queued.IsBriefOnly && queued.Revision != briefRevision {
			return startPlan{}, StartRefusal{Reason: fleet.ErrBriefNotReady.Error() + ": the brief changed while planning its start", Passing: true}
		}
		briefLines = strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
		named = fleet.BriefDelivery(content)
	}
	plan := startPlan{id: id, brief: brief, briefRevision: briefRevision, project: fleet.BriefProject(briefLines), isProductionDefect: row.Priority == "production-defect", isRoot: row.Priority == RootPriority}
	if plan.project == "" {
		plan.project = row.Repo
	}
	if plan.project == "" {
		return startPlan{}, StartRefusal{Reason: "The brief for " + id + " names no project"}
	}
	if root := rootForStart(h, queued, plan); root != nil {
		plan.isRoot = true
		plan.root = root
		refusal := StartRefusal{Reason: "Root issue awaits verification of its recorded identity", Waits: true}
		if root.Problem != "" {
			refusal.Reason = root.refusal()
		}
		if reading.root != nil {
			refusal = reading.root(*root)
		}
		if refusal.Reason != "" {
			return startPlan{}, refusal
		}
	}
	settings := queuedEngine(row, named)
	plan.harness, plan.model, plan.effort, plan.mode = settings.harness, settings.model, settings.effort, settings.mode
	if err := plan.validateEngine(); err != nil {
		return startPlan{}, StartRefusal{Reason: err.Error()}
	}
	if errors.Is(briefErr, os.ErrNotExist) {
		queued.Row.Repo, queued.Row.Mode, queued.Row.Harness, queued.Row.Model, queued.Row.Effort = plan.project, plan.mode, plan.harness, plan.model, plan.effort
		plan.missingBrief = &queued
		content = []byte(fleet.QueuedBriefText(queued, nil))
	}
	if refusal := deliveryWait(h, plan, content); refusal != "" {
		if plan.missingBrief != nil {
			if err := fleet.WriteQueuedBrief(h, *plan.missingBrief); err != nil && !errors.Is(err, os.ErrExist) {
				return startPlan{}, err
			}
		}
		return startPlan{}, StartRefusal{Reason: refusal}
	}
	return plan, nil
}

// deliveryWait is why the task's start waits on a delivery exception, or ""
// when it may start: a mode narrower than the project's posture and the
// brief's assessment select starts only under the exception the registered
// primary CEO approved for it. Started without one, goblins spawn would
// freeze no-mistakes, and an explicit spawn bringing the exception would find
// the task already started under it (issue 2146).
func deliveryWait(h home.Home, plan startPlan, brief []byte) string {
	if plan.mode == "" || plan.mode == "no-mistakes" {
		return ""
	}
	manifest, _, err := project.LoadForCheckout(h.Data, plan.project)
	if err != nil {
		manifest = project.Manifest{}
	}
	authority, exceptionErr := project.ApprovedDeliveryException(h.Data, plan.id, plan.mode, manifest)
	selected := project.SelectDelivery(manifest, string(brief), plan.mode, authority)
	if selected.Mode == plan.mode {
		return ""
	}
	reason := selected.Reason
	if exceptionErr != nil {
		reason += "; " + exceptionErr.Error()
	}
	approval := plan
	approval.briefRevision = ""
	briefPath := plan.brief
	if plan.missingBrief != nil {
		briefPath += ", written from its queued row,"
	}
	return fmt.Sprintf("%s asks for %s delivery and waits for its approval, since %s. The registered primary CEO approves it with goblins %s --delivery-exception \"<authority and reason>\", or the mode: line of its brief %s changes to no-mistakes", plan.id, plan.mode, reason, commandLine(approval.args()), briefPath)
}

func (p startPlan) validateEngine() error {
	switch {
	case p.harness != "" && !slices.Contains(spawnHarnesses, p.harness):
		return fmt.Errorf("the brief or backlog row names harness %s, which goblins spawn does not run", p.harness)
	case p.mode != "" && !slices.Contains(spawnModes, p.mode):
		return fmt.Errorf("the brief or backlog row names mode %s, which goblins spawn does not run", p.mode)
	case p.model != "" && !spawnValue.MatchString(p.model), p.effort != "" && p.effort != "default" && !slices.Contains(harness.Efforts(harness.Claude), p.effort):
		return errors.New("the brief or backlog row names a model or effort goblins spawn cannot take")
	}
	return nil
}

// queuedEngine is the engine and mode a queued task starts with, given what
// its brief names: the row's harness, model and effort before the brief's,
// and the brief's mode before the row's, since readiness validated it and the
// row's mode is only what a brief not yet written is given.
func queuedEngine(row fleet.BacklogRow, named map[string]string) startPlan {
	pick := func(fromRow, key string) string {
		if fromRow != "" || row.HasSetting(key) {
			return fromRow
		}
		return named[key]
	}
	mode := named["mode"]
	if mode == "" {
		mode = row.Mode
	}
	return startPlan{
		harness: pick(row.Harness, "harness"),
		model:   pick(row.Model, "model"),
		effort:  pick(row.Effort, "effort"),
		mode:    mode,
	}
}

// briefSettings are the harness, model, effort and mode a queued task's brief
// names under its Delivery heading, on lines of their own such as
// "mode: direct-PR".
func briefSettings(path string) map[string]string {
	content, err := fsx.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return fleet.BriefDelivery(content)
}
