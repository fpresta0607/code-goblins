// Package supervisor owns the persistent native control plane. Browsers only
// observe its state and enqueue bounded actions; they never drive progression.
package supervisor

import (
	"context"
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
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
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
}

type Action struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	TaskID      string    `json:"task_id"`
	Generation  string    `json:"generation,omitempty"`
	Session     string    `json:"session,omitempty"`
	EventID     string    `json:"event_id,omitempty"`
	Text        string    `json:"text,omitempty"`
	File        string    `json:"file,omitempty"`
	Line        int       `json:"line,omitempty"`
	EndLine     int       `json:"end_line,omitempty"`
	Side        string    `json:"side,omitempty"`
	Head        string    `json:"head,omitempty"`
	Revision    string    `json:"revision,omitempty"`
	DiffID      string    `json:"diff_id,omitempty"`
	QuestionID  string    `json:"question_id,omitempty"`
	ReviewID    string    `json:"review_id,omitempty"`
	AnswerKind  string    `json:"answer_kind,omitempty"`
	CFOIdentity string    `json:"cfo_identity,omitempty"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
}

type Store struct {
	Home        home.Home
	mu          sync.Mutex
	db          Database
	committed   Database
	changed     chan struct{}
	inboxCursor string
}

func Open(h home.Home) (*Store, error) {
	if err := os.MkdirAll(nativehook.SpoolDir(h.State), 0700); err != nil {
		return nil, err
	}
	s := &Store{Home: h, changed: make(chan struct{}, 1), db: Database{Schema: 1, Sessions: map[string]Session{}, TaskSessions: map[string]string{}, Tasks: map[string]Evaluation{}, Actions: []Action{}, Seen: []string{}, Issues: []string{}}}
	if info, err := os.Stat(s.path()); err == nil && info.Size() > maxStateBytes {
		return nil, errors.New("supervisor state exceeds its bound")
	}
	data, err := os.ReadFile(s.path())
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
			if a.Status == "running" {
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
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	s.committed = cloneDatabase(s.db)
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
	if err != nil {
		s.db = cloneDatabase(s.committed)
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	if err := fsx.AtomicWriteFile(s.path(), append(data, '\n')); err != nil {
		s.db = cloneDatabase(s.committed)
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	s.committed = cloneDatabase(s.db)
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return nil
}

func (s *Store) Snapshot() Database {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneDatabase(s.db)
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
	d.Reviews = slices.Clone(d.Reviews)
	for i := range d.Questions {
		d.Questions[i].Options = slices.Clone(d.Questions[i].Options)
	}
	for i := range d.Reviews {
		d.Reviews[i].ImageSums = slices.Clone(d.Reviews[i].ImageSums)
	}
	return d
}

func (s *Store) Accept(e nativehook.Event) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
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
	node := Session{ID: key, NativeID: e.SessionID, Harness: e.Harness, Role: e.Role, TaskID: e.TaskID, Generation: e.Generation, Parent: parent, ReportedRoot: e.RootSessionID, Relation: e.Relation, Model: e.Model, AgentType: e.AgentType, Phase: e.Kind, TurnID: e.TurnID, LastEventID: e.ID, UpdatedAt: e.OccurredAt}
	if node.Model == "" {
		node.Model = prior.Model
	}
	if node.Relation == "" {
		node.Relation = prior.Relation
	}
	if node.ReportedRoot == "" {
		node.ReportedRoot = prior.ReportedRoot
	}
	s.db.Sessions[key] = node
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

// QueueReview admits a review only for a CFO the verifier proves live, and
// pins that registration as its recipient. An identical retry answers from
// its durable record before any probe, so a replacement registration can
// never adopt it. The registration stays open, which denies its replacement,
// until the pinned review is durable. The probe runs outside the store lock
// and never sends.
func (s *Store) QueueReview(ctx context.Context, a Action, cfo *CFOConnection) (Action, error) {
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
	probe, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := cfo.verify(probe, primary); err != nil {
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
		if generation == "" && (existing.Kind == "evaluate" || existing.Kind == "feedback") {
			generation = existing.Generation
		}
		if existing.Kind != a.Kind || existing.TaskID != a.TaskID || existing.Generation != generation || existing.Session != a.Session || existing.EventID != a.EventID || existing.Text != a.Text || existing.File != a.File || existing.Head != a.Head || existing.Line != a.Line || existing.EndLine != a.EndLine || existing.Side != a.Side || existing.Revision != a.Revision || existing.DiffID != a.DiffID || existing.QuestionID != a.QuestionID || existing.ReviewID != a.ReviewID || existing.AnswerKind != a.AnswerKind {
			return Action{}, false, errors.New("request ID was already used for another action")
		}
		return existing, true, nil
	}
	return Action{}, false, nil
}

func (s *Store) queue(a Action) (Action, error) {
	if existing, found, err := s.lookup(a); err != nil || found {
		return existing, err
	}
	answer := a.Kind == "cfo_answer" || a.Kind == "goblin_answer"
	item := a.Kind == "review_answer" || a.Kind == "review_clear" || a.Kind == "question_clear"
	if !answer && a.AnswerKind != "" {
		return Action{}, errors.New("answer kind is only valid for a question")
	}
	if a.Kind != "evaluate" && a.Kind != "review" && !answer && !item {
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
	if answer {
		if a.Generation == "" || a.TaskID != "" || a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" {
			return Action{}, errors.New("an answer requires only its recipient identity and text")
		}
		if a.QuestionID == "" {
			return Action{}, errors.New("invalid user question context")
		}
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
		// Terminal actions can roll out; pending and uncertain intent stays.
		remove := -1
		for i, old := range s.db.Actions {
			if old.Status == "succeeded" || old.Status == "failed" {
				remove = i
				break
			}
		}
		if remove < 0 {
			return Action{}, errors.New("action queue is full; resolve pending actions")
		}
		s.db.Actions = slices.Delete(s.db.Actions, remove, remove+1)
	}
	a.Status = "queued"
	a.CreatedAt = time.Now().UTC()
	a.UpdatedAt = a.CreatedAt
	s.db.Actions = append(s.db.Actions, a)
	if answer {
		for i := range s.db.Questions {
			if s.db.Questions[i].ID == a.QuestionID {
				s.db.Questions[i].AnswerID, s.db.Questions[i].Status = a.ID, "queued"
				s.db.Questions[i].Answer, s.db.Questions[i].AnswerKind = a.Text, a.AnswerKind
				at := a.CreatedAt
				s.db.Questions[i].AnsweredBy, s.db.Questions[i].AnsweredAt = "overlord", &at
				if a.AnswerKind != "other" && slices.Contains(s.db.Questions[i].Options, a.Text) {
					s.db.Questions[i].AnsweredOption = a.Text
				}
			}
		}
	}
	return a, nil
}

// queueItemAction admits the Overlord answering or clearing one Command
// Center item: an open review, or a question that closed without an answer.
// A pending question cannot be cleared, so an unanswered decision is never
// hidden.
func (s *Store) queueItemAction(a Action) (Action, error) {
	answer := a.Kind == "review_answer"
	if a.Generation == "" || answer && strings.TrimSpace(a.Text) == "" || !answer && a.Text != "" || a.TaskID != "" || a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" || (a.Kind == "question_clear") != (a.QuestionID != "") || (a.Kind != "question_clear") != (a.ReviewID != "") {
		return Action{}, errors.New("an item action names only its item, that item's identity and, for an answer, its text")
	}
	review := -1
	if a.ReviewID != "" {
		review = slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == a.ReviewID && r.Identity == a.Generation && r.State == "open" })
		if review < 0 {
			return Action{}, errors.New("that review is not open; refresh the board")
		}
	}
	if a.Kind == "question_clear" && !slices.ContainsFunc(s.db.Questions, func(q Question) bool {
		return q.ID == a.QuestionID && q.Identity == a.Generation && (q.Status == "superseded" || q.Status == "failed")
	}) {
		return Action{}, errors.New("only a question that closed without an answer can be cleared; refresh the board")
	}
	if len(s.db.Actions) >= maxActions {
		remove := slices.IndexFunc(s.db.Actions, func(old Action) bool { return old.Status == "succeeded" || old.Status == "failed" })
		if remove < 0 {
			return Action{}, errors.New("action queue is full; resolve pending actions")
		}
		s.db.Actions = slices.Delete(s.db.Actions, remove, remove+1)
	}
	a.Status = "queued"
	a.CreatedAt = time.Now().UTC()
	a.UpdatedAt = a.CreatedAt
	s.db.Actions = append(s.db.Actions, a)
	if answer {
		r := &s.db.Reviews[review]
		r.State, r.Answer, r.AnswerID, r.Delivered, r.UpdatedAt = "answered", a.Text, a.ID, false, a.CreatedAt
	}
	return a, nil
}

// ProcessOne commits intent before executing it. Only read-only evaluation is
// safe to replay after a crash; external deliveries have uncertain outcomes.
func (s *Store) ProcessOne(ctx context.Context, execute func(context.Context, Action) (Evaluation, error)) error {
	s.mu.Lock()
	i := -1
	for index, a := range s.db.Actions {
		if a.Status == "queued" {
			i = index
			break
		}
	}
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
	s.db.Actions[i].Status = "running"
	s.db.Actions[i].UpdatedAt = time.Now().UTC()
	if err := s.save(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	result, runErr := execute(ctx, a)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Queue append/retention can move indices while the action is running.
	i = slices.IndexFunc(s.db.Actions, func(current Action) bool { return current.ID == a.ID })
	if i < 0 {
		return errors.New("running action disappeared")
	}
	completed := &s.db.Actions[i]
	completed.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		completed.Status = "failed"
		if a.Kind != "evaluate" && !errors.Is(runErr, ErrRejected) {
			completed.Status = "uncertain"
		}
		completed.Message = bounded(runErr.Error(), 1200)
		if a.Kind == "evaluate" && (a.EventID == "" || s.db.Sessions[a.Session].LastEventID == a.EventID) {
			prior := s.db.Tasks[a.TaskID]
			s.db.Tasks[a.TaskID] = Evaluation{Phase: "unavailable", Reason: completed.Message, Base: prior.Base, Generation: a.Generation, At: completed.UpdatedAt}
		}
	} else {
		completed.Status = "succeeded"
		completed.Message = result.Reason
		if a.Kind == "evaluate" && (a.EventID == "" || s.db.Sessions[a.Session].LastEventID == a.EventID) {
			result.At = time.Now().UTC()
			result.Generation = a.Generation
			s.db.Tasks[a.TaskID] = result
		}
	}
	s.updateQuestionOutcomes()
	return s.save()
}

func (s *Store) updateQuestionOutcomes() {
	for i := range s.db.Questions {
		if s.db.Questions[i].Status == "cleared" {
			continue
		}
		for _, a := range s.db.Actions {
			if (a.Kind == "cfo_answer" || a.Kind == "goblin_answer") && a.ID == s.db.Questions[i].AnswerID {
				s.db.Questions[i].Status, s.db.Questions[i].Message = a.Status, a.Message
			}
		}
	}
}

func (s *Store) Ingest() error {
	entries, err := os.ReadDir(nativehook.SpoolDir(s.Home.State))
	if err != nil {
		return err
	}
	type record struct {
		path  string
		event nativehook.Event
		err   error
	}
	records := []record{}
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
			return statErr
		}
		if info.Size() > nativehook.MaxInputBytes {
			r.err = errors.New("oversized event")
		} else {
			data, readErr := os.ReadFile(r.path)
			if readErr != nil {
				return readErr
			}
			r.err = json.Unmarshal(data, &r.event)
		}
		records = append(records, r)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].event.OccurredAt.Equal(records[j].event.OccurredAt) {
			return records[i].event.Kind == "started" && records[j].event.Kind != "started"
		}
		return records[i].event.OccurredAt.Before(records[j].event.OccurredAt)
	})
	consumed := 0
	for _, r := range records {
		if consumed >= 256 {
			break
		}
		err := r.err
		if err == nil {
			err = s.Accept(r.event)
		}
		if errors.Is(err, ErrStorage) {
			return err
		}
		if errors.Is(err, ErrDeferred) {
			continue
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
				return saveErr
			}
		}
		if err := os.Remove(r.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		consumed++
	}
	return nil
}

func bounded(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
