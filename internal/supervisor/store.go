// Package supervisor owns the persistent native control plane. Browsers only
// observe its state and enqueue bounded actions; they never drive progression.
package supervisor

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const maxSessions = 512
const maxActions = 256
const maxSeen = 2048
const maxStateBytes = 8 << 20

var ErrStorage = errors.New("supervisor persistence failed")
var ErrDeferred = errors.New("event awaits retryable evidence")
var ErrRejected = errors.New("invalid native event")

type Session struct {
	ID           string          `json:"id"`
	NativeID     string          `json:"native_id"`
	Harness      string          `json:"harness"`
	Role         string          `json:"role"`
	TaskID       string          `json:"task_id,omitempty"`
	Generation   string          `json:"generation,omitempty"`
	Parent       string          `json:"parent,omitempty"`
	ReportedRoot string          `json:"reported_root,omitempty"`
	Relation     string          `json:"relation,omitempty"`
	Model        string          `json:"model,omitempty"`
	AgentType    string          `json:"agent_type,omitempty"`
	Phase        string          `json:"phase"`
	TurnID       string          `json:"turn_id,omitempty"`
	LastEventID  string          `json:"last_event_id"`
	UpdatedAt    time.Time       `json:"updated_at"`
	PromptAt     time.Time       `json:"prompt_at,omitzero"` // when this generation last took a prompt for a turn
	HostID       string          `json:"host_id,omitempty"`  // the native terminal the session runs in
	Runtime      RuntimeEvidence `json:"runtime"`
}

type Evaluation struct {
	Phase      string    `json:"phase"`
	Reason     string    `json:"reason"`
	Head       string    `json:"head,omitempty"`
	Base       string    `json:"base,omitempty"`
	Generation string    `json:"generation,omitempty"`
	PR         string    `json:"pr,omitempty"`
	Verified   bool      `json:"verified"`
	At         time.Time `json:"at"`
	// GateStep is the no-mistakes step the task's gate is on, such as test.
	GateStep string `json:"gate_step,omitempty"`
	// WaitingOn is what a waiting task waits on: another task's ID,
	// overlord, ci or deploy.
	WaitingOn string `json:"waiting_on,omitempty"`
	// Awaiting is set by a delivery that was typed and submitted and now
	// waits for its harness to report taking it; its action keeps it.
	Awaiting *Awaiting `json:"-"`
	// CanSendNow is whether a queued message could be typed into its session
	// on a retry, and SendNowWhy says why not when it could not.
	CanSendNow bool   `json:"-"`
	SendNowWhy string `json:"-"`
}

type Action struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	TaskID     string `json:"task_id"`
	Generation string `json:"generation,omitempty"`
	Session    string `json:"session,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	Text       string `json:"text,omitempty"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Side       string `json:"side,omitempty"`
	Head       string `json:"head,omitempty"`
	Revision   string `json:"revision,omitempty"`
	DiffID     string `json:"diff_id,omitempty"`
	QuestionID string `json:"question_id,omitempty"`
	ReviewID   string `json:"review_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	AnswerKind string `json:"answer_kind,omitempty"`
	// Target is, on a delete of a queued message, that message's own action id,
	// so two messages with the same words are told apart.
	Target      string    `json:"target,omitempty"`
	CFOIdentity string    `json:"cfo_identity,omitempty"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Awaiting is set while the action's delivery, typed and submitted, waits
	// for its harness to report taking it.
	Awaiting *Awaiting `json:"awaiting,omitempty"`
	// CanSendNow is whether he may ask for a queued message to be typed now,
	// and SendNowWhy says why not, in plain words.
	CanSendNow bool   `json:"can_send_now,omitempty"`
	SendNowWhy string `json:"send_now_why,omitempty"`
	// Advice is what the Overlord should do about a delivery that never
	// arrived, in his words; the board shows it as written.
	Advice string `json:"advice,omitempty"`
	// Dismissed names the questions a clear dismissed while they still waited
	// on the Overlord, which the CFO hears of when the action runs.
	Dismissed []string `json:"dismissed,omitempty"`
	// Replaces is, on his change of an answer, the answer it replaces, as it
	// stood when he made the change; the supervisor sets it.
	Replaces string `json:"replaces,omitempty"`
	// With is, only on an answer to the CFO as it is handed to its run, the
	// answers that go to the CFO in the same message after it, in the order
	// he gave them. It is never stored.
	With []Action `json:"-"`
}

type Database struct {
	Schema        int                   `json:"schema"`
	Revision      uint64                `json:"revision"`
	Sessions      map[string]Session    `json:"sessions"`
	Retired       []string              `json:"retired"`
	TaskSessions  map[string]string     `json:"task_sessions"`
	Tasks         map[string]Evaluation `json:"tasks"`
	Actions       []Action              `json:"actions"`
	Seen          []string              `json:"seen"`
	Issues        []string              `json:"issues"`
	Questions     []Question            `json:"questions"`
	QuestionFloor time.Time             `json:"question_floor,omitempty"`
	Activity      []BoardActivity       `json:"activity"`
	Reviews       []Review              `json:"reviews,omitempty"`
	Runs          []Run                 `json:"runs,omitempty"`
	// CFOAnswers are answers the CFO gave over the pipe whose question could
	// not take them yet: still arriving, or held by the Overlord's own board
	// answer on its way. Only the pipe adds to them, so each is proven.
	CFOAnswers []cfoAnswer `json:"cfo_answers,omitempty"`
	// Credentials are requests for credential values, by name only.
	Credentials    []CredentialRequest `json:"credentials,omitempty"`
	BrowserSignIns []BrowserSignIn     `json:"browser_signins,omitempty"`
	// Announced is every Command Center key the board has announced, so no
	// item is announced twice.
	Announced []Announcement `json:"announced,omitempty"`
	// QueuedSends are the receipts of messages typed into a goblin in a
	// turn, held until its record shows the goblin took each; the board
	// shows a receipt only then.
	QueuedSends []BoardActivity `json:"queued_sends,omitempty"`
}

type Store struct {
	Home home.Home
	mu   sync.Mutex
	// readers protects the committed view without waiting for file writes.
	readers       sync.RWMutex
	db            Database
	committed     Database
	pageReceipts  pageReceiptState
	changed       chan struct{}
	inboxCursor   string
	deferredUntil map[string]time.Time
	// inboxRead is each inbox event as the last intake read it, by file
	// name, so an unchanged event deferred again is not read and parsed
	// again; isInboxFilling says the CFO was woken for an inbox near its
	// bound, until it drains to half.
	inboxRead      map[string]inboxEvent
	isInboxFilling bool
	// failingSince is when saves started failing, in Unix nanoseconds; zero
	// while they succeed.
	failingSince atomic.Int64
}

func Open(h home.Home) (*Store, error) {
	if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
		return nil, err
	}
	s := &Store{Home: h, changed: make(chan struct{}, 1), db: Database{Schema: 1, Sessions: map[string]Session{}, TaskSessions: map[string]string{}, Tasks: map[string]Evaluation{}, Actions: []Action{}, Seen: []string{}, Issues: []string{}}}
	if info, err := os.Stat(s.path()); err == nil && info.Size() > maxStateBytes {
		return nil, errors.New("supervisor state exceeds its bound")
	}
	data, err := fsx.ReadFile(s.path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 0 {
		if len(data) > maxStateBytes {
			return nil, errors.New("supervisor state exceeds its bound")
		}
		if err := json.Unmarshal(data, &s.db); err != nil {
			return nil, fmt.Errorf("supervisor state is corrupt: %w", err)
		}
		if s.db.Schema != 1 || s.db.Sessions == nil || s.db.Tasks == nil || s.db.TaskSessions == nil || len(s.db.Sessions) > maxSessions || len(s.db.Actions) > maxActions {
			return nil, errors.New("invalid supervisor state")
		}
		s.committed = cloneDatabase(s.db)
		for i := range s.db.Actions {
			a := &s.db.Actions[i]
			// A delivery already typed and submitted still awaits its reader.
			if a.Status == "running" && a.Awaiting == nil {
				if a.Kind == "evaluate" {
					a.Status = "queued"
					a.Message = "Recovered evaluation"
				} else {
					a.Status = "uncertain"
					a.Message = "Supervisor stopped during delivery. Inspect the task before sending again."
				}
			}
		}
		s.updateQuestionOutcomes()
		for i := range s.db.BrowserSignIns {
			card := &s.db.BrowserSignIns[i]
			if card.VerifiedAt == nil && (card.State == "opening" || card.State == "open") {
				card.State, card.Reason = "ready", "The board restarted. Open the sign-in window again."
			}
		}
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	s.committed = cloneDatabase(s.db)
	if err := s.readPageReceipts(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) path() string { return filepath.Join(s.Home.State, ".supervisor.json") }

func (s *Store) save() error {
	for len(s.db.Tasks) > maxSessions {
		oldest := ""
		for id, value := range s.db.Tasks {
			if s.db.TaskSessions[id] == "" && (oldest == "" || value.At.Before(s.db.Tasks[oldest].At)) {
				oldest = id
			}
		}
		if oldest == "" {
			s.db = cloneDatabase(s.committed)
			return fmt.Errorf("%w: task history is at capacity", ErrDeferred)
		}
		delete(s.db.Tasks, oldest)
	}
	s.db.Revision++
	data, err := json.Marshal(s.db)
	if err == nil && len(data)+1 > maxStateBytes {
		err = errors.New("supervisor state exceeds its bound")
	}
	if err == nil {
		err = fsx.AtomicWriteFile(s.path(), append(data, '\n'))
	}
	if err != nil {
		s.db = cloneDatabase(s.committed)
		s.failingSince.CompareAndSwap(0, time.Now().UnixNano())
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	s.failingSince.Store(0)
	committed := cloneDatabase(s.db)
	s.readers.Lock()
	s.committed = committed
	s.readers.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return nil
}

func (s *Store) Snapshot() Database {
	s.readers.RLock()
	defer s.readers.RUnlock()
	return cloneDatabase(s.committed)
}

func cloneDatabase(d Database) Database {
	d.Sessions = maps.Clone(d.Sessions)
	d.Retired = slices.Clone(d.Retired)
	d.TaskSessions = maps.Clone(d.TaskSessions)
	d.Tasks = maps.Clone(d.Tasks)
	d.Actions = slices.Clone(d.Actions)
	d.Seen = slices.Clone(d.Seen)
	d.Issues = slices.Clone(d.Issues)
	d.Questions = slices.Clone(d.Questions)
	d.Activity = slices.Clone(d.Activity)
	d.QueuedSends = slices.Clone(d.QueuedSends)
	d.Reviews = slices.Clone(d.Reviews)
	d.Runs = slices.Clone(d.Runs)
	d.CFOAnswers = slices.Clone(d.CFOAnswers)
	d.Credentials = slices.Clone(d.Credentials)
	d.BrowserSignIns = slices.Clone(d.BrowserSignIns)
	for i := range d.BrowserSignIns {
		if d.BrowserSignIns[i].ClosedAt != nil {
			closed := *d.BrowserSignIns[i].ClosedAt
			d.BrowserSignIns[i].ClosedAt = &closed
		}
		if d.BrowserSignIns[i].VerifiedAt != nil {
			verified := *d.BrowserSignIns[i].VerifiedAt
			d.BrowserSignIns[i].VerifiedAt = &verified
		}
	}
	for i := range d.Credentials {
		d.Credentials[i] = d.Credentials[i].clone()
	}
	d.Announced = slices.Clone(d.Announced)
	for i := range d.Questions {
		d.Questions[i].Options = slices.Clone(d.Questions[i].Options)
	}
	for i := range d.Reviews {
		d.Reviews[i].ImageSums = slices.Clone(d.Reviews[i].ImageSums)
	}
	for i := range d.Runs {
		d.Runs[i].CredentialNames = slices.Clone(d.Runs[i].CredentialNames)
	}
	return d
}

func (s *Store) Accept(e nativehook.Event) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A refusal puts back what the event changed, a copy of every session,
	// task and action, so only one that came after a change does: on
	// 2026-10-27 the inbox held 4,096 events it refused before any change,
	// and copying the database for each took 11 seconds of every cycle.
	isChanging := false
	defer func() {
		if err != nil && isChanging {
			s.db = cloneDatabase(s.committed)
		}
		if err != nil && !errors.Is(err, ErrStorage) && !errors.Is(err, ErrDeferred) {
			err = fmt.Errorf("%w: %v", ErrRejected, err)
		}
	}()
	if err := e.Validate(); err != nil {
		return err
	}
	if slices.Contains(s.db.Seen, e.ID) {
		return nil
	}
	if e.OccurredAt.After(time.Now().Add(time.Minute)) || e.OccurredAt.Before(time.Now().Add(-7*24*time.Hour)) {
		return errors.New("event timestamp is outside retention window")
	}
	if e.TaskID != "" {
		meta, err := state.ReadTaskMeta(s.Home.State, e.TaskID)
		if err != nil {
			_, isTracked := s.db.TaskSessions[e.TaskID]
			_, isEvaluated := s.db.Tasks[e.TaskID]
			if errors.Is(err, os.ErrNotExist) && !isTracked && !isEvaluated && time.Since(e.OccurredAt) > unknownTaskGrace {
				return fmt.Errorf("%w: %w: task %s has no record", ErrDeferred, errUnknownTask, bounded(e.TaskID, 80))
			}
			return fmt.Errorf("%w: task metadata unavailable: %v", ErrDeferred, err)
		}
		if e.Generation != meta.SpawnGen {
			return errors.New("event belongs to another spawn generation")
		}
		if e.Role == "goblin" && e.Harness != meta.Harness {
			return errors.New("event harness does not match task")
		}
		cwd, err := fsx.Canonical(e.CWD)
		if err != nil {
			return fmt.Errorf("%w: cwd unavailable: %v", ErrDeferred, err)
		}
		wt, err := fsx.Canonical(meta.Worktree)
		if err != nil {
			return fmt.Errorf("%w: worktree unavailable: %v", ErrDeferred, err)
		}
		rel, err := filepath.Rel(wt, cwd)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("event cwd does not belong to task worktree")
		}
	}
	key := e.Harness + "/" + e.SessionID
	prior, known := s.db.Sessions[key]
	if known {
		if prior.TaskID != e.TaskID || prior.Role != e.Role || (prior.Generation != e.Generation && e.Kind != "started") {
			return errors.New("session attribution changed")
		}
		if e.OccurredAt.Before(prior.UpdatedAt) {
			return errors.New("stale session event")
		}
		if prior.Phase == "ended" && e.Kind != "ended" && e.Kind != "started" {
			return errors.New("session is already ended")
		}
	} else if e.Kind != "started" {
		return fmt.Errorf("%w: session has no start evidence", ErrDeferred)
	}
	parent := prior.Parent
	if e.ParentSessionID != "" {
		incoming := e.ParentHarness + "/" + e.ParentSessionID
		if parent != "" && parent != incoming {
			return errors.New("conflicting parent evidence; reparenting refused")
		}
		parent = incoming
		for at, depth := parent, 0; at != ""; depth++ {
			if at == key || depth >= maxSessions {
				return errors.New("cyclic session lineage")
			}
			at = s.db.Sessions[at].Parent
		}
	}
	isChanging = true
	if e.Role == "goblin" {
		current := s.db.TaskSessions[e.TaskID]
		if current == "" && len(s.db.TaskSessions) >= maxSessions {
			oldest := ""
			for task, evaluation := range s.db.Tasks {
				if s.db.TaskSessions[task] != "" && evaluation.Phase == "done" && (oldest == "" || evaluation.At.Before(s.db.Tasks[oldest].At)) {
					oldest = task
				}
			}
			if oldest == "" {
				return fmt.Errorf("%w: unresolved task tracking is at capacity", ErrDeferred)
			}
			delete(s.db.TaskSessions, oldest)
			delete(s.db.Tasks, oldest)
		}
		if current != "" && current != key {
			if e.Kind != "started" || !e.OccurredAt.After(s.db.Sessions[current].UpdatedAt) {
				return errors.New("event is from an obsolete task session")
			}
		}
	}
	if !known && len(s.db.Sessions) >= maxSessions {
		oldest := ""
		for id, node := range s.db.Sessions {
			if node.Phase != "ended" && time.Since(node.UpdatedAt) < 7*24*time.Hour {
				continue
			}
			if slices.ContainsFunc(s.db.Actions, func(a Action) bool { return a.Session == id && (a.Status == "queued" || a.Status == "running") }) {
				continue
			}
			if oldest == "" || node.UpdatedAt.Before(s.db.Sessions[oldest].UpdatedAt) {
				oldest = id
			}
		}
		if oldest == "" {
			return fmt.Errorf("%w: all retained sessions have recent activity or pending actions", ErrDeferred)
		}
		retired := s.db.Sessions[oldest]
		delete(s.db.Sessions, oldest)
		for task, id := range s.db.TaskSessions {
			if id == oldest {
				if _, exists := s.db.Tasks[task]; !exists {
					s.db.Tasks[task] = Evaluation{Phase: "unknown", Reason: "Native session retired; independent task evidence is pending", Generation: retired.Generation, At: retired.UpdatedAt}
				}
			}
		}
		s.db.Retired = append(s.db.Retired, oldest)
		if len(s.db.Retired) > maxSessions {
			s.db.Retired = s.db.Retired[len(s.db.Retired)-maxSessions:]
		}
	}
	// Reserve the action slot before mutating session state.
	if e.Role == "goblin" && (e.Kind == "settled" || e.Kind == "ended" || e.Kind == "interrupted") {
		a := Action{ID: "eval-" + e.ID, Kind: "evaluate", TaskID: e.TaskID, Generation: e.Generation, Session: key, EventID: e.ID}
		if _, err := s.queue(a); err != nil {
			return fmt.Errorf("%w: %v", ErrDeferred, err)
		}
	}
	node := Session{ID: key, NativeID: e.SessionID, Harness: e.Harness, Role: e.Role, TaskID: e.TaskID, Generation: e.Generation, Parent: parent, ReportedRoot: e.RootSessionID, Relation: e.Relation, Model: e.Model, AgentType: e.AgentType, Phase: e.Kind, TurnID: e.TurnID, HostID: e.HostID, LastEventID: e.ID, UpdatedAt: e.OccurredAt}
	if node.Model == "" {
		node.Model = prior.Model
	}
	if node.Relation == "" {
		node.Relation = prior.Relation
	}
	if node.ReportedRoot == "" {
		node.ReportedRoot = prior.ReportedRoot
	}
	if known && prior.Generation == e.Generation {
		node.PromptAt = prior.PromptAt
	}
	if e.Prompt {
		node.PromptAt = e.OccurredAt
	}
	// A compaction starts and ends no turn, so the session's record stays as
	// its last turn left it.
	if e.Kind != "compacting" && e.Kind != "compacted" {
		s.db.Sessions[key] = node
	}
	if e.Role == "goblin" {
		s.db.TaskSessions[e.TaskID] = key
	}
	if !known || prior.Generation != e.Generation {
		source := parent
		if s.db.Sessions[source].ID == "" {
			source = ""
		}
		if err := s.retainActivity(BoardActivity{ID: "start-" + e.ID, Kind: "created", TaskID: e.TaskID, Generation: e.Generation, Source: source, Target: key, State: "accepted", At: e.OccurredAt}); err != nil {
			return err
		}
	}
	s.db.Seen = append(s.db.Seen, e.ID)
	if len(s.db.Seen) > maxSeen {
		s.db.Seen = s.db.Seen[len(s.db.Seen)-maxSeen:]
	}
	return s.save()
}

func (s *Store) Queue(a Action) (Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued, err := s.queue(a)
	if err != nil {
		return Action{}, err
	}
	return queued, s.save()
}

// queuedMessage is the queued message action id names for task.
func (s *Store) queuedMessage(id, task string) (*Action, error) {
	if id == "" {
		return nil, errors.New("that message was not named")
	}
	for i := range s.db.Actions {
		a := &s.db.Actions[i]
		if a.ID == id && a.TaskID == task && a.Kind == "message" {
			if a.Status != "queued" {
				return nil, errors.New("that message is no longer waiting")
			}
			return a, nil
		}
	}
	return nil, errors.New("that message is no longer waiting")
}

// EditQueuedMessage changes a queued message's words before it is delivered,
// so the change is what its session receives.
func (s *Store) EditQueuedMessage(id, task, text string) (Action, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Action{}, errors.New("a message cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.queuedMessage(id, task)
	if err != nil {
		return Action{}, err
	}
	a.Text, a.UpdatedAt = text, time.Now().UTC()
	if err := s.save(); err != nil {
		return Action{}, err
	}
	return *a, nil
}

// SendQueuedMessageNow clears a queued message's wait so the worker types it
// at its next safe moment, and refuses when its own reason says typing now
// would break a gate or has nowhere to go.
func (s *Store) SendQueuedMessageNow(id, task string) (Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.queuedMessage(id, task)
	if err != nil {
		return Action{}, err
	}
	if !a.CanSendNow {
		why := a.SendNowWhy
		if why == "" {
			why = "It cannot be typed into its session now."
		}
		return Action{}, errors.New(why)
	}
	delete(s.deferredUntil, a.ID)
	a.UpdatedAt = time.Now().UTC()
	if err := s.save(); err != nil {
		return Action{}, err
	}
	return *a, nil
}

// queueUnlessEvaluating queues a reconcile's evaluation only while no
// evaluation of its task is queued or running. It is the second of two
// checks: the reconcile's snapshot catches an evaluation that finishes while
// the reconcile reads, and this one, under the store lock, catches one queued
// since the snapshot. Its request ID names the minute and everything the
// evaluation carries: two reconciles can fall in one minute, after a restart
// or a slow cycle, and the second asks for the same evaluation only when it
// saw the same session event and generation, and otherwise for a new one,
// never for another under the first one's ID.
func (s *Store) queueUnlessEvaluating(a Action, now time.Time) error {
	sum := sha256.Sum256([]byte(a.Session + "\n" + a.EventID + "\n" + a.Generation))
	a.ID = fmt.Sprintf("reconcile-%s-%d-%x", a.TaskID, now.Unix()/60, sum[:8])
	s.mu.Lock()
	defer s.mu.Unlock()
	if evaluationPending(s.db.Actions, a.TaskID) {
		return nil
	}
	if _, err := s.queue(a); err != nil {
		return err
	}
	return s.save()
}

// QueueReview admits a review only for a CFO the verifier proves live, and
// pins that registration as its recipient. An identical retry answers from
// its durable record before any probe, so a replacement registration can
// never adopt it. The registration stays open, which denies its replacement,
// until the pinned review is durable. The probe runs outside the store lock
// and never sends.
func (s *Store) QueueReview(a Action, cfo *CFOConnection) (Action, error) {
	s.mu.Lock()
	existing, found, err := s.lookup(a)
	s.mu.Unlock()
	if err != nil || found {
		return existing, err
	}
	if cfo == nil {
		return Action{}, errors.New("CFO transport is unavailable. No review was queued.")
	}
	file, err := openPrimary(filepath.Join(s.Home.State, "primary.json"))
	if err != nil {
		return Action{}, fmt.Errorf("%v. No review was queued.", errNotRegistered)
	}
	defer file.Close()
	primary, identity, err := decodePrimary(file)
	if err != nil {
		return Action{}, err
	}
	if err := cfo.verify(primary); err != nil {
		return Action{}, fmt.Errorf("%v. No review was queued.", err)
	}
	a.CFOIdentity = identity
	return s.Queue(a)
}

// lookup returns the durable action an identical retry names. A changed
// payload under the same ID is refused rather than treated as a new request.
func (s *Store) lookup(a Action) (Action, bool, error) {
	if a.ID == "" || len(a.ID) > 128 || strings.ContainsAny(a.ID, "\x00\r\n") {
		return Action{}, false, errors.New("action requires a bounded request ID")
	}
	for _, existing := range s.db.Actions {
		if existing.ID != a.ID {
			continue
		}
		generation := a.Generation
		if generation == "" && (existing.Kind == "evaluate" || existing.Kind == "feedback" || existing.Kind == "message") {
			generation = existing.Generation
		}
		if existing.Kind != a.Kind || existing.TaskID != a.TaskID || existing.Generation != generation || existing.Session != a.Session || existing.EventID != a.EventID || existing.Text != a.Text || existing.File != a.File || existing.Head != a.Head || existing.Line != a.Line || existing.EndLine != a.EndLine || existing.Side != a.Side || existing.Revision != a.Revision || existing.DiffID != a.DiffID || existing.QuestionID != a.QuestionID || existing.ReviewID != a.ReviewID || existing.RunID != a.RunID || existing.AnswerKind != a.AnswerKind {
			return Action{}, false, errors.New("request ID was already used for another action")
		}
		return existing, true, nil
	}
	return Action{}, false, nil
}

// isClosedHistory reports whether a can roll out of a full action bound to
// make room for a new action: it succeeded, failed, or was never confirmed
// (uncertain) and is no longer watched for a late take. Nothing resolves an
// unconfirmed delivery, so held for good it would fill the bound and refuse
// every new action. Queued and running work stays, and so does a delivery
// still watched, which a late take may yet confirm.
func isClosedHistory(a Action) bool {
	return a.Status == "succeeded" || a.Status == "failed" || a.Status == "uncertain" && a.Awaiting == nil
}

func (s *Store) queue(a Action) (Action, error) {
	if existing, found, err := s.lookup(a); err != nil || found {
		return existing, err
	}
	answer := a.Kind == "cfo_answer" || a.Kind == "goblin_answer"
	change := a.Kind == "answer_change"
	item := a.Kind == "review_answer" || a.Kind == "review_clear" || a.Kind == "question_clear" || a.Kind == "run" || a.Kind == "run_stop" || a.Kind == "run_skip" || a.Kind == "run_answer"
	message := a.Kind == "message"
	if !item && a.RunID != "" {
		return Action{}, errors.New("only a run action names a run item")
	}
	if a.Kind == "message_withdraw" {
		return s.withdrawMessage(a)
	}
	if !answer && !change && a.AnswerKind != "" {
		return Action{}, errors.New("answer kind is only valid for a question")
	}
	if a.Kind != "evaluate" && a.Kind != "review" && !answer && !change && !item && !message {
		return Action{}, errors.New("unsupported action; task lifecycle cannot be dragged or assigned")
	}
	if len(a.Text) > 16000 || len(a.File) > 4096 {
		return Action{}, errors.New("action exceeds size limit")
	}
	if item {
		return s.queueItemAction(a)
	}
	if a.Kind != "evaluate" && strings.TrimSpace(a.Text) == "" {
		return Action{}, errors.New("comment is empty")
	}
	if a.Kind == "review" && a.Generation == "" {
		return Action{}, errors.New("task generation is required; refresh the board")
	}
	if answer || change {
		if a.Generation == "" || a.TaskID != "" || a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" {
			return Action{}, errors.New("an answer requires only its recipient identity and text")
		}
		if a.QuestionID == "" {
			return Action{}, errors.New("invalid user question context")
		}
	} else if message && a.TaskID == "" {
		// A message to the CFO goes to whichever CFO runs when it is
		// delivered, so it names none.
		if a.Generation != "" || a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" || a.QuestionID != "" || a.ReviewID != "" {
			return Action{}, errors.New("a message to the CFO carries only its text")
		}
	} else if message {
		// A message goes to its task's goblin whichever session runs when it
		// is delivered, and to a queued task's once its Start brings one up.
		if a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" || a.QuestionID != "" || a.ReviewID != "" {
			return Action{}, errors.New("a message to a goblin carries only its task and text")
		}
		meta, err := state.ReadTaskMeta(s.Home.State, a.TaskID)
		if errors.Is(err, os.ErrNotExist) {
			if _, queuedErr := fleet.ReadQueuedTask(s.Home, a.TaskID); queuedErr != nil {
				return Action{}, errors.Join(err, queuedErr)
			}
		} else if err != nil {
			return Action{}, err
		}
		a.Generation = meta.SpawnGen
	} else {
		meta, err := state.ReadTaskMeta(s.Home.State, a.TaskID)
		if err != nil {
			return Action{}, err
		}
		if a.Generation != "" && a.Generation != meta.SpawnGen {
			return Action{}, errors.New("task restarted or was replaced; refresh the board")
		}
		a.Generation = meta.SpawnGen
	}
	if answer {
		if err := s.questionAnswer(a); err != nil {
			return Action{}, err
		}
	}
	isSame := false
	if change {
		replaces, isChosen, err := s.questionChange(a)
		if err != nil {
			return Action{}, err
		}
		a.Replaces, isSame = replaces, isChosen
	}
	if a.Kind == "review" && a.CFOIdentity == "" {
		return Action{}, errors.New("A review needs a verified CFO recipient. No review was queued.")
	}
	if a.Kind == "cfo_answer" {
		file, err := openPrimary(filepath.Join(s.Home.State, "primary.json"))
		if err != nil {
			return Action{}, errNotRegistered
		}
		_, identity, err := decodePrimary(file)
		_ = file.Close()
		if err != nil || identity != a.Generation {
			return Action{}, errors.New("the primary CFO changed; refresh before sending")
		}
	}
	if len(s.db.Actions) >= maxActions {
		remove := slices.IndexFunc(s.db.Actions, isClosedHistory)
		if remove < 0 {
			return Action{}, errors.New("action queue is full; resolve pending actions")
		}
		delete(s.deferredUntil, s.db.Actions[remove].ID)
		s.db.Actions = slices.Delete(s.db.Actions, remove, remove+1)
	}
	a.Status = "queued"
	a.CreatedAt = time.Now().UTC()
	a.UpdatedAt = a.CreatedAt
	// The answer already chosen sends nothing, but is kept, so a retry of the
	// same request finds it done.
	if isSame {
		a.Status, a.Message = "succeeded", "That is already the answer, so nothing was sent."
	}
	s.db.Actions = append(s.db.Actions, a)
	if answer {
		for i := range s.db.Questions {
			if s.db.Questions[i].ID == a.QuestionID {
				s.db.Questions[i].AnswerID, s.db.Questions[i].Status = a.ID, "queued"
				s.db.Questions[i].Answer, s.db.Questions[i].AnswerKind = a.Text, a.AnswerKind
				s.closeFollowUps(s.db.Questions[i], "overlord", a.CreatedAt)
			}
		}
	}
	// His change is the answer from the moment he makes it, on every board,
	// while it goes on its way.
	if change && !isSame {
		if i := slices.IndexFunc(s.db.Questions, func(q Question) bool { return q.ID == a.QuestionID }); i >= 0 {
			q, at := &s.db.Questions[i], a.CreatedAt
			q.ChangeID, q.ReplacedAnswer = a.ID, a.Replaces
			q.Answer, q.AnswerKind, q.AnsweredOption = a.Text, a.AnswerKind, ""
			if a.AnswerKind == "option" {
				q.AnsweredOption = a.Text
			}
			q.AnsweredBy, q.AnsweredAt, q.AnsweredAway, q.AnsweredIn, q.Message = "overlord", &at, false, "", "You changed the answer."
		}
	}
	return a, nil
}

// queueItemAction admits the Overlord answering or clearing one Command
// Center item: an open review, or a question that closed without an answer
// or still waits on him, which the clear dismisses. A pending question whose
// answer is on its way cannot be cleared, and a clear carries no text. An
// answer and a clear close their item here, under the
// store lock, so every board shows it closed at once; the action that runs
// later only delivers the answer or tells the asker of the clear.
func (s *Store) queueItemAction(a Action) (Action, error) {
	answer := a.Kind == "review_answer" || a.Kind == "run_answer"
	runAction := a.Kind == "run" || a.Kind == "run_stop" || a.Kind == "run_skip" || a.Kind == "run_answer"
	if a.Generation == "" || answer && strings.TrimSpace(a.Text) == "" || !answer && a.Text != "" || a.TaskID != "" || a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" || (a.Kind == "question_clear") != (a.QuestionID != "") || (a.Kind == "review_answer" || a.Kind == "review_clear") != (a.ReviewID != "") || runAction != (a.RunID != "") {
		return Action{}, errors.New("an item action names only its item, that item's identity and, for an answer, its text")
	}
	review := -1
	if a.ReviewID != "" {
		review = slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == a.ReviewID && r.Identity == a.Generation && r.State == "open" })
		if review < 0 {
			return Action{}, errors.New("that review is not open; refresh the board")
		}
	}
	question := -1
	if a.Kind == "question_clear" {
		question = slices.IndexFunc(s.db.Questions, func(q Question) bool {
			return q.ID == a.QuestionID && q.Identity == a.Generation && (q.Status == "superseded" || q.Status == "failed" || q.Status == "pending" && q.AnswerID == "")
		})
		if question < 0 {
			return Action{}, errors.New("only a question waiting on you or closed without an answer can be cleared; refresh the board")
		}
	}
	// Stop names a command running in its terminal; the action that runs
	// later ends it.
	if a.Kind == "run_stop" && !slices.ContainsFunc(s.db.Runs, func(r Run) bool {
		return r.ID == a.RunID && r.Identity == a.Generation && r.State == "running" && r.Terminal
	}) {
		return Action{}, errors.New("that command is not running on its card; refresh the board")
	}
	// A run item runs once: the action claims it here, under the store lock.
	run := -1
	if a.Kind == "run" {
		run = slices.IndexFunc(s.db.Runs, func(r Run) bool { return r.ID == a.RunID && r.Identity == a.Generation })
		switch {
		case run < 0:
			return Action{}, errors.New("that run item is not on the board; refresh the board")
		case s.db.Runs[run].State == "expired" || s.db.Runs[run].State == "ready" && s.db.Runs[run].Update == nil && !time.Now().Before(s.db.Runs[run].ExpiresAt):
			return Action{}, errors.New("that run item expired; running it again needs a new item from the CFO")
		case s.db.Runs[run].CFOUnwoken != 0:
			return Action{}, errors.New("that item restarts the CFO with Restart the CFO; it runs no command")
		case s.db.Runs[run].CFOStopped:
			return Action{}, errors.New("that item reopens the CFO with Reopen the CFO; it runs no command")
		case s.db.Runs[run].State == "withdrawn":
			return Action{}, errors.New("the CFO withdrew that run item: " + s.db.Runs[run].Reason)
		case s.db.Runs[run].State != "ready":
			return Action{}, errors.New("that run item already ran; running it again needs a new item from the CFO")
		}
	}
	// Skip and Other close a ready command nobody ran, here under the store
	// lock, in place of running it; the action that runs later tells its
	// asker.
	declined := -1
	if a.Kind == "run_skip" || a.Kind == "run_answer" {
		declined = slices.IndexFunc(s.db.Runs, func(r Run) bool { return r.ID == a.RunID && r.Identity == a.Generation })
		switch {
		case declined < 0:
			return Action{}, errors.New("that run item is not on the board; refresh the board")
		case s.db.Runs[declined].Update != nil || s.db.Runs[declined].CFOUnwoken != 0 || s.db.Runs[declined].CFOStopped || s.db.Runs[declined].CredentialRequest != "" || s.db.Runs[declined].ConnectionTask != "" || s.db.Runs[declined].RemoteFix != "":
			return Action{}, errors.New("that item is the board's own, not a command someone asked you to run")
		case s.db.Runs[declined].State != "ready" || !time.Now().Before(s.db.Runs[declined].ExpiresAt):
			return Action{}, errors.New("that run item is no longer waiting on you; refresh the board")
		}
	}
	if len(s.db.Actions) >= maxActions {
		remove := slices.IndexFunc(s.db.Actions, isClosedHistory)
		if remove < 0 {
			return Action{}, errors.New("action queue is full; resolve pending actions")
		}
		s.db.Actions = slices.Delete(s.db.Actions, remove, remove+1)
	}
	a.Status = "queued"
	a.CreatedAt = time.Now().UTC()
	a.UpdatedAt = a.CreatedAt
	switch {
	case question >= 0:
		a.Dismissed = s.clearQuestion(question)
	case a.Kind == "review_clear":
		s.clearReview(review, clearedFromTheCommandCenter, a.CreatedAt)
	}
	s.db.Actions = append(s.db.Actions, a)
	if a.Kind == "review_answer" {
		r := &s.db.Reviews[review]
		r.State, r.Answer, r.AnswerID, r.Delivered, r.UpdatedAt = "answered", a.Text, a.ID, false, a.CreatedAt
	}
	if declined >= 0 {
		closed := a.CreatedAt
		r := &s.db.Runs[declined]
		r.State, r.Reason, r.RunAction, r.FinishedAt = "withdrawn", "The Overlord skipped it from the Command Center.", a.ID, &closed
		if a.Kind == "run_answer" {
			r.Reason = "The Overlord answered instead: " + bounded(a.Text, 1800)
		}
	}
	if run >= 0 {
		ran := a.CreatedAt
		r := &s.db.Runs[run]
		r.State, r.RunAction, r.RanAt = "running", a.ID, &ran
	}
	return a, nil
}

// ProcessOne commits intent before executing it. Only read-only evaluation is
// safe to replay after a crash; external deliveries have uncertain outcomes.
func (s *Store) ProcessOne(ctx context.Context, execute func(context.Context, Action) (Evaluation, error)) error {
	// The follow and the pick share one read of the CFO, so a delivery that
	// waited for it is never sent addressed to the registration before.
	identity, live := s.liveCFO()
	s.mu.Lock()
	if live {
		if err := s.follow(identity); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	i := s.nextQueued(live)
	if i < 0 {
		s.mu.Unlock()
		return nil
	}
	a := s.db.Actions[i]
	if a.EventID != "" && s.db.Sessions[a.Session].LastEventID != a.EventID {
		s.db.Actions[i].Status = "succeeded"
		s.db.Actions[i].Message = "Superseded by newer session activity"
		err := s.save()
		s.mu.Unlock()
		return err
	}
	// The answers to the CFO's questions that wait with this one go with it,
	// as one message in the order he gave them, whatever kept each queued.
	// They are typed once, so they share one outcome.
	together := []int{i}
	if answers, _ := s.answersTogether(); slices.Contains(answers, i) {
		together = answers
		a = s.db.Actions[together[0]]
		for _, j := range together[1:] {
			a.With = append(a.With, s.db.Actions[j])
		}
	}
	started := time.Now().UTC()
	ran := make([]string, len(together))
	for n, j := range together {
		s.db.Actions[j].Status, s.db.Actions[j].UpdatedAt = "running", started
		ran[n] = s.db.Actions[j].ID
	}
	if err := s.save(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	result, runErr := execute(ctx, a)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ran {
		// Queue append/retention can move indices while the action is running.
		i = slices.IndexFunc(s.db.Actions, func(current Action) bool { return current.ID == id })
		if i < 0 {
			return errors.New("running action disappeared")
		}
		completed := &s.db.Actions[i]
		a := *completed
		completed.UpdatedAt = time.Now().UTC()
		if errors.Is(runErr, ErrDeferred) {
			completed.Status = "queued"
			completed.Message = bounded(runErr.Error(), 1200)
			if result.Reason != "" {
				completed.Message = bounded(result.Reason, 1200)
			}
			completed.CanSendNow, completed.SendNowWhy = result.CanSendNow, result.SendNowWhy
			if s.deferredUntil == nil {
				s.deferredUntil = make(map[string]time.Time)
			}
			s.deferredUntil[a.ID] = completed.UpdatedAt.Add(time.Second)
			continue
		}
		delete(s.deferredUntil, a.ID)
		completed.CanSendNow, completed.SendNowWhy = false, ""
		if runErr != nil {
			completed.Status = "failed"
			if a.Kind != "evaluate" && !errors.Is(runErr, ErrRejected) {
				completed.Status = "uncertain"
			}
			completed.Message = bounded(runErr.Error(), 1200)
			if a.Kind == "evaluate" && (a.EventID == "" || s.db.Sessions[a.Session].LastEventID == a.EventID) {
				prior := s.db.Tasks[a.TaskID]
				base := ""
				if prior.Generation == a.Generation {
					base = prior.Base
				}
				s.db.Tasks[a.TaskID] = Evaluation{Phase: "unavailable", Reason: completed.Message, Base: base, Generation: a.Generation, At: completed.UpdatedAt}
			}
		} else if result.Awaiting != nil {
			// Typed and submitted, and its harness has yet to report taking it:
			// it stays on its way until settleDeliveries hears.
			awaiting := *result.Awaiting
			if awaiting.QuietSince.IsZero() {
				awaiting.QuietSince = awaiting.Since
			}
			completed.Awaiting, completed.Message = &awaiting, result.Reason
		} else {
			completed.Status = "succeeded"
			completed.Message = result.Reason
			if a.Kind == "evaluate" && (a.EventID == "" || s.db.Sessions[a.Session].LastEventID == a.EventID) {
				result.At = time.Now().UTC()
				result.Generation = a.Generation
				s.db.Tasks[a.TaskID] = result
			}
		}
	}
	s.updateQuestionOutcomes()
	if err := s.save(); err != nil {
		return err
	}
	if errors.Is(runErr, ErrDeferred) {
		return runErr
	}
	return nil
}

func (s *Store) updateQuestionOutcomes() {
	for i := range s.db.Questions {
		if s.db.Questions[i].Status == "cleared" {
			continue
		}
		q := &s.db.Questions[i]
		for _, a := range s.db.Actions {
			if (a.Kind == "cfo_answer" || a.Kind == "goblin_answer") && a.ID == q.AnswerID {
				q.Status = a.Status
				// A change he made since is the answer, whenever the first one
				// arrives.
				if q.ChangeID != "" {
					continue
				}
				q.Message = a.Message
				if a.Status == "succeeded" {
					// A board answer is the Overlord's; an answer the CFO gave over
					// the pipe records its own attribution, which stays.
					if q.AnsweredBy == "" {
						at := a.UpdatedAt
						q.AnsweredBy, q.AnsweredAt = "overlord", &at
					}
					if a.AnswerKind != "other" && slices.Contains(q.Options, a.Text) {
						q.AnsweredOption = a.Text
					}
				}
			}
		}
		// Once his change has reached whoever acts on it, the question reads
		// answered, whatever became of the answer before it.
		if change := slices.IndexFunc(s.db.Actions, func(a Action) bool { return a.Kind == "answer_change" && a.ID == q.ChangeID }); change >= 0 && (s.db.Actions[change].Status == "succeeded" || s.db.Actions[change].Awaiting != nil) {
			q.Status = "succeeded"
		}
	}
}

// deferralLimit is how long an event waits in the native inbox for the
// evidence it was deferred for. One that waits longer waits for what does
// not come, such as the record of a task that was retired or never existed,
// or the start of a session whose start was lost, and leaves the inbox: on
// 2026-10-27 such events had filled it to its 4,096 bound, so the hooks of
// every goblin found it full and their events were lost.
const deferralLimit = time.Hour

// unknownTaskGrace is how long an event of a task with no record, which the
// store never tracked, waits for that record before it leaves the inbox: a
// task's record is written before its terminal starts, so only a process
// that carries a goblin's variables without being one, as a gate daemon's
// review sessions did, spools such events, 3,510 of them on 2026-10-27.
const unknownTaskGrace = 2 * time.Minute

// errUnknownTask marks a deferral that waits for a task the fleet never
// knew, which leaves the inbox once unknownTaskGrace has passed.
var errUnknownTask = errors.New("the fleet never knew its task")

// inboxFilling is how many events in the native inbox wake the CFO, a
// quarter short of the bound at which every goblin's hook fails.
const inboxFilling = nativehook.MaxQueuedEvents * 3 / 4

// inboxEvent is an inbox event as an intake read it, with the size and
// write time its file had then.
type inboxEvent struct {
	size    int64
	written time.Time
	event   nativehook.Event
	err     error
}

// Ingest accepts the events waiting in the native inbox and returns each it
// accepted for the first time, in the order it accepted them, also when it
// stops short with an error: they have left the inbox. Events deferred past
// deferralLimit, or waiting for a task the fleet never knew, leave it too,
// and one issue line says how many.
func (s *Store) Ingest() ([]nativehook.Event, error) {
	entries, err := os.ReadDir(nativehook.SpoolDir(s.Home.State))
	if err != nil {
		return nil, recoverNativeInbox(s.Home.State, err)
	}
	type record struct {
		path  string
		event nativehook.Event
		err   error
	}
	records := []record{}
	read, byTask := map[string]inboxEvent{}, map[string]int{}
	// Concurrent hook writers can briefly exceed the admission bound. Rotate
	// a bounded read window rather than wedging or discarding deferred events.
	start := sort.Search(len(entries), func(i int) bool { return entries[i].Name() > s.inboxCursor })
	for offset := 0; offset < len(entries); offset++ {
		entry := entries[(start+offset)%len(entries)]
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".event.json") {
			continue
		}
		if len(records) >= nativehook.MaxQueuedEvents {
			break
		}
		s.inboxCursor = entry.Name()
		r := record{path: filepath.Join(nativehook.SpoolDir(s.Home.State), entry.Name())}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil, statErr
		}
		if known, ok := s.inboxRead[entry.Name()]; ok && known.size == info.Size() && known.written.Equal(info.ModTime()) {
			r.event, r.err = known.event, known.err
		} else if info.Size() > nativehook.MaxEventBytes {
			r.err = errors.New("oversized event")
		} else {
			data, readErr := fsx.ReadFile(r.path)
			if readErr != nil {
				return nil, readErr
			}
			r.err = json.Unmarshal(data, &r.event)
		}
		read[entry.Name()] = inboxEvent{size: info.Size(), written: info.ModTime(), event: r.event, err: r.err}
		records = append(records, r)
		byTask[cmp.Or(r.event.TaskID, "the CFO's sessions")]++
	}
	// Only events still in the inbox are kept, so the record of what was
	// read never outgrows the inbox.
	s.inboxRead = read
	if err := s.wakeForAFillingInbox(len(records), byTask); err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].event.OccurredAt.Equal(records[j].event.OccurredAt) {
			return records[i].event.Kind == "started" && records[j].event.Kind != "started"
		}
		return records[i].event.OccurredAt.Before(records[j].event.OccurredAt)
	})
	consumed, dropped, dropReason := 0, 0, ""
	var accepted []nativehook.Event
	for _, r := range records {
		if consumed >= 256 {
			break
		}
		err := r.err
		isReplay := false
		if err == nil {
			s.mu.Lock()
			isReplay = slices.Contains(s.db.Seen, r.event.ID)
			s.mu.Unlock()
			err = s.Accept(r.event)
		}
		if errors.Is(err, ErrStorage) {
			return accepted, err
		}
		if errors.Is(err, ErrDeferred) {
			if !errors.Is(err, errUnknownTask) && time.Since(r.event.OccurredAt) < deferralLimit {
				continue
			}
			if dropped++; dropReason == "" {
				dropReason = err.Error()
			}
			if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return accepted, err
			}
			consumed++
			continue
		}
		if err == nil && !isReplay {
			accepted = append(accepted, r.event)
		}
		if err != nil {
			// Quarantine stores bounded diagnostics only, not raw potentially
			// secret input. A malformed event cannot wedge the entire fleet.
			s.mu.Lock()
			s.db.Issues = append(s.db.Issues, time.Now().UTC().Format(time.RFC3339)+" "+bounded(err.Error(), 300))
			if len(s.db.Issues) > 20 {
				s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
			}
			saveErr := s.save()
			s.mu.Unlock()
			if saveErr != nil {
				return accepted, saveErr
			}
		}
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return accepted, err
		}
		consumed++
	}
	if dropped > 0 {
		s.mu.Lock()
		s.db.Issues = append(s.db.Issues, fmt.Sprintf("%s set aside %d native events that waited for evidence that does not come, the first because %s", time.Now().UTC().Format(time.RFC3339), dropped, bounded(dropReason, 200)))
		if len(s.db.Issues) > 20 {
			s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
		}
		err := s.save()
		s.mu.Unlock()
		if err != nil {
			return accepted, err
		}
	}
	return accepted, nil
}

// wakeForAFillingInbox wakes the CFO once as the native inbox passes
// inboxFilling, naming the tasks whose events fill it most, before every
// goblin's hook finds it full; it wakes again only after the inbox has
// drained to half.
func (s *Store) wakeForAFillingInbox(count int, byTask map[string]int) error {
	if count < inboxFilling {
		if count <= nativehook.MaxQueuedEvents/2 {
			s.isInboxFilling = false
		}
		return nil
	}
	if s.isInboxFilling {
		return nil
	}
	tasks := slices.SortedFunc(maps.Keys(byTask), func(a, b string) int { return cmp.Or(byTask[b]-byTask[a], strings.Compare(a, b)) })
	var most []string
	for _, task := range tasks[:min(3, len(tasks))] {
		most = append(most, fmt.Sprintf("%s (%d)", bounded(task, 80), byTask[task]))
	}
	now := time.Now().UTC()
	detail := fmt.Sprintf("native_inbox_filling: the native event inbox holds %d events, and at %d every goblin's hook fails and its events are lost. Most come from %s. Find what spools them and stop it; they wait in %s.", count, nativehook.MaxQueuedEvents, strings.Join(most, ", "), nativehook.SpoolDir(s.Home.State))
	if err := wake.AppendFirstAndPublish(s.Home.State, "native-inbox/filling/"+now.Format(time.RFC3339Nano), "check", "native-inbox", detail); err != nil {
		return err
	}
	s.isInboxFilling = true
	return nil
}

func bounded(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
