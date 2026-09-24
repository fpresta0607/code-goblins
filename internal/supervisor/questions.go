package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const maxQuestions = 128

type Question struct {
	ID          string    `json:"id"`
	Identity    string    `json:"identity"`
	Text        string    `json:"text"`
	Options     []string  `json:"options"`
	Recommended string    `json:"recommended,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	AnswerID    string    `json:"answer_id,omitempty"`
	Answer      string    `json:"answer,omitempty"`
	AnswerKind  string    `json:"answer_kind,omitempty"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
	// Task and Generation name the goblin that asked, and Seq the blocked
	// notify that told the CFO; all three are empty for the CFO's own
	// question.
	Task       string `json:"task,omitempty"`
	Generation string `json:"generation,omitempty"`
	Seq        int    `json:"seq,omitempty"`
	// Images are a goblin's review images as absolute paths, one for each
	// choice in order. cfo notify checks them before publishing and the board
	// checks them again whenever it serves one, and only ever sees ImageCount.
	Images     []string `json:"images,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
}

func validQuestion(q Question) error {
	if len(q.ID) < 8 || len(q.ID) > 128 || strings.ContainsAny(q.ID, "\x00\r\n") || len(q.Identity) != 64 || strings.TrimSpace(q.Text) == "" || len(q.Text) > 4000 || len(q.Options) > 8 || q.CreatedAt.IsZero() || q.CreatedAt.After(time.Now().Add(5*time.Minute)) {
		return errors.New("question requires an ID, registered CFO identity, timestamp, bounded text and at most eight choices")
	}
	seen := map[string]bool{}
	for _, option := range q.Options {
		if strings.TrimSpace(option) == "" || len(option) > 500 || seen[option] {
			return errors.New("question choices must be distinct, nonempty and bounded")
		}
		seen[option] = true
	}
	if q.Recommended != "" && !slices.Contains(q.Options, q.Recommended) {
		return errors.New("recommendation must name one of the supplied choices exactly")
	}
	if q.Task != "" && (state.ValidTaskID(q.Task) != nil || q.Generation == "" || q.Seq <= 0) {
		return errors.New("a goblin's question names its task, generation and blocked notify")
	}
	if len(q.Images) > 0 && (q.Task == "" || len(q.Images) != len(q.Options)) {
		return errors.New("only a goblin's question takes images, one for each choice it offers")
	}
	for _, image := range q.Images {
		if !filepath.IsAbs(image) || filepath.Clean(image) != image {
			return errors.New("a review image must be an absolute, clean path")
		}
	}
	return nil
}

func sameQuestion(a, b Question) bool {
	return a.Identity == b.Identity && a.Text == b.Text && slices.Equal(a.Options, b.Options) && a.Recommended == b.Recommended && a.Task == b.Task && slices.Equal(a.Images, b.Images)
}

// goblinIdentity binds a goblin's question to the task generation and pane
// that asked, so a restarted or moved task never receives an answer meant
// for its predecessor.
func goblinIdentity(meta state.TaskMeta) string {
	sum := sha256.Sum256([]byte("goblin\x00" + meta.ID + "\x00" + meta.SpawnGen + "\x00" + meta.HerdrSession + "\x00" + meta.HerdrPaneID))
	return hex.EncodeToString(sum[:])
}

// goblinAsker proves the calling process runs under the task's own pane, the
// same proof registration uses for the CFO, so no other process can ask in
// a goblin's name.
func goblinAsker(ctx context.Context, stateDir string, client *herdr.Client, taskID string) (state.TaskMeta, error) {
	meta, err := state.ReadTaskMeta(stateDir, taskID)
	if err != nil {
		return meta, fmt.Errorf("task %s has no live record: %w", taskID, err)
	}
	if meta.Backend != "herdr" || meta.HerdrSession == "" || meta.HerdrPaneID == "" {
		return meta, fmt.Errorf("task %s has no Herdr pane to answer", taskID)
	}
	if _, _, err := paneHarness(ctx, client, herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}); err != nil {
		return meta, err
	}
	return meta, nil
}

// SurfaceNotify shows a goblin's blocking notify in the Command Center when
// it offers choices, labelled with the goblin, and the Overlord's answer
// returns to the goblin's pane once. A notify without choices is prose for
// the CFO and never opens the modal, and neither does a failed notify.
// images, one for each choice in order, must already have passed
// ReviewImages: SurfaceNotify records them without checking the files.
func SurfaceNotify(ctx context.Context, stateDir string, client *herdr.Client, taskID string, record wake.Record, images []string) error {
	question, options, ok := wake.Question(record)
	if !ok || len(options) == 0 {
		return nil
	}
	// A goblin marks the choice it recommends the way AskUserQuestion does,
	// by ending it with "(Recommended)".
	recommended := ""
	for i, option := range options {
		if choice, marked := strings.CutSuffix(option, "(Recommended)"); marked {
			options[i] = strings.TrimSpace(choice)
			if recommended == "" {
				recommended = options[i]
			}
		}
	}
	meta, err := goblinAsker(ctx, stateDir, client, taskID)
	if err != nil {
		return err
	}
	q := Question{ID: fmt.Sprintf("notify-%s-%d", taskID, record.Seq), Identity: goblinIdentity(meta), Text: question, Options: options, Recommended: recommended, CreatedAt: time.Now().UTC(), Status: "pending", Task: taskID, Generation: meta.SpawnGen, Seq: record.Seq, Images: images}
	if err := validQuestion(q); err != nil {
		return err
	}
	return publish(stateDir, q)
}

// SendGoblin delivers text to the goblin a question named, through the same
// Herdr connection the board uses for the CFO. The delivery is pinned to the
// task generation and pane that asked, so a restarted or moved task never
// receives an answer meant for its predecessor.
func (c *CFOConnection) SendGoblin(ctx context.Context, taskID, identity, text string) (Evaluation, error) {
	current := func(target herdr.Target) error {
		meta, err := state.ReadTaskMeta(c.State, taskID)
		if err != nil || goblinIdentity(meta) != identity || target != (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}) {
			return errors.New("the goblin's task restarted or ended")
		}
		return nil
	}
	meta, err := state.ReadTaskMeta(c.State, taskID)
	if err != nil || current(herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}) != nil {
		return Evaluation{}, fmt.Errorf("%w: the goblin's task restarted or ended; nothing was sent", ErrRejected)
	}
	guard := func(_ context.Context, target herdr.Target, _ herdr.AgentDetail) error { return current(target) }
	sender := fleet.Sender{Herdr: c.Herdr, Resolve: fleet.Resolver{StateDir: c.State}, Guard: guard}
	if err := sender.Text(ctx, taskID, oneLine(text)); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Accepted by the goblin through Herdr."}, nil
}

// answerGoblin returns the Overlord's board answer to the goblin that asked.
// The goblin's blocked notify then reads answered, so neither the monitor
// nor the CFO asks it again; retiring it is still the CFO's ordinary ack.
func (s *Service) answerGoblin(ctx context.Context, a Action) (Evaluation, error) {
	if s.Options.CFO == nil {
		return Evaluation{}, fmt.Errorf("%w: Herdr message transport is unavailable", ErrRejected)
	}
	i := slices.IndexFunc(s.Store.Snapshot().Questions, func(q Question) bool {
		return q.ID == a.QuestionID && q.Identity == a.Generation && q.AnswerID == a.ID && q.Task != ""
	})
	if i < 0 {
		return Evaluation{}, fmt.Errorf("%w: user question context changed", ErrRejected)
	}
	q := s.Store.Snapshot().Questions[i]
	pending, err := wake.Pending(s.Store.Home.State)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: read the wake queue: %v; nothing was sent", ErrRejected, err)
	}
	if !slices.ContainsFunc(pending, func(r wake.Record) bool { return r.Seq == q.Seq && r.Answered == "" }) {
		return Evaluation{}, fmt.Errorf("%w: the CFO already handled this question; nothing was sent", ErrRejected)
	}
	label := "Answer"
	if a.AnswerKind == "other" {
		label = "Answer (Other)"
	}
	result, err := s.Options.CFO.SendGoblin(ctx, q.Task, q.Identity, fmt.Sprintf("The Overlord answered your question on the board. Question: %s %s: %s", q.Text, label, a.Text))
	if err != nil {
		return result, err
	}
	if err := wake.MarkAnswered(s.Store.Home.State, q.Seq, a.Text); err != nil {
		result.Reason += " The CFO's notify still reads unanswered: " + err.Error()
	}
	return result, nil
}

// callerIdentity proves this process descends from the registered primary
// CFO, including its creation time, and that its native identity is live, and
// returns that identity. The registration stays open, so it cannot change,
// until release is called.
func (c *CFOConnection) callerIdentity(ctx context.Context) (string, func(), error) {
	file, err := openPrimary(filepath.Join(c.State, "primary.json"))
	if err != nil {
		return "", nil, errNotRegistered
	}
	release := func() { _ = file.Close() }
	p, identity, err := decodePrimary(file)
	if err != nil {
		release()
		return "", nil, err
	}
	entries, err := proc.Ancestry(os.Getpid(), 32)
	if err != nil {
		release()
		return "", nil, err
	}
	if !slices.ContainsFunc(entries, func(entry proc.Entry) bool { return entry.PID == p.Process.PID && entry.Start.Equal(p.Process.Start) }) {
		release()
		return "", nil, errors.New("only the registered CFO process may report to the Overlord")
	}
	if err := c.verify(ctx, p); err != nil {
		release()
		return "", nil, err
	}
	return identity, release, nil
}

// PublishQuestion is deliberately a local CFO operation, not a browser or
// worker-alert endpoint. The caller must descend from the registered primary
// process, including its creation time, and its native identity must be live.
func (c *CFOConnection) PublishQuestion(ctx context.Context, id, text string, options []string, recommended string) error {
	identity, release, err := c.callerIdentity(ctx)
	if err != nil {
		return err
	}
	defer release()
	q := Question{ID: id, Identity: identity, Text: text, Options: options, Recommended: recommended, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := validQuestion(q); err != nil {
		return err
	}
	return publish(c.State, q)
}

// publish writes a validated question into the inbox the supervisor
// ingests. A retry with the same ID and content changes nothing and the same
// ID with other content is refused, whether the question still waits in the
// inbox or was already ingested.
func publish(stateDir string, q Question) error {
	if _, err := lock.AcquireExclusiveNamed(stateDir, ".question-publish.lock"); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(stateDir, ".question-publish.lock")
	id := q.ID
	// Read only. Opening a Store here would perform crash recovery while the
	// supervisor is live and overwrite its single-writer transaction state.
	if info, err := os.Stat(filepath.Join(stateDir, ".supervisor.json")); err == nil {
		if info.Size() > maxStateBytes {
			return errors.New("supervisor state exceeds its bound")
		}
		data, err := os.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
		if err != nil {
			return err
		}
		var db Database
		if err := json.Unmarshal(data, &db); err != nil {
			return errors.New("supervisor question history is unreadable")
		}
		for _, prior := range db.Questions {
			if prior.ID == id {
				if !sameQuestion(prior, q) {
					return errors.New("question ID already used")
				}
				return nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Join(stateDir, "questions-inbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entriesOnDisk, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(id))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	if data, err := os.ReadFile(path); err == nil {
		var prior Question
		if json.Unmarshal(data, &prior) != nil || !sameQuestion(prior, q) {
			return errors.New("question ID already used")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entriesOnDisk) >= maxQuestions {
		return errors.New("user question inbox is full")
	}
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

func (s *Store) acceptQuestion(q Question) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validQuestion(q); err != nil {
		return err
	}
	for _, prior := range s.db.Questions {
		if prior.ID == q.ID {
			if !sameQuestion(prior, q) {
				return errors.New("question ID already used")
			}
			return nil
		}
	}
	if !s.db.QuestionFloor.IsZero() && !q.CreatedAt.After(s.db.QuestionFloor) {
		return nil
	}
	if len(s.db.Questions) >= maxQuestions {
		// A question that closed without an answer stays listed until the
		// Overlord clears it, or until it is the oldest superseded one while
		// 128 are held: a new question is never refused because closed ones
		// were not cleared. A pending question is never dropped.
		index := slices.IndexFunc(s.db.Questions, func(old Question) bool {
			return old.Status == "cleared" || old.Status == "succeeded" || old.Status == "failed"
		})
		if index < 0 {
			index = slices.IndexFunc(s.db.Questions, func(old Question) bool { return old.Status == "superseded" })
		}
		if index < 0 {
			return ErrDeferred
		}
		if old := s.db.Questions[index]; old.CreatedAt.After(s.db.QuestionFloor) {
			// An older pending publication may outlive a newer answer. Never
			// move the replay cutoff past this admission or its timestamp peers.
			floor := old.CreatedAt
			if !floor.Before(q.CreatedAt) {
				floor = q.CreatedAt.Add(-time.Nanosecond)
			}
			if floor.After(s.db.QuestionFloor) {
				s.db.QuestionFloor = floor
			}
		}
		s.db.Questions = slices.Delete(s.db.Questions, index, index+1)
	}
	q.Status, q.AnswerID, q.Message = "pending", "", ""
	q.Answer, q.AnswerKind = "", ""
	q.Options = slices.Clone(q.Options)
	s.db.Questions = append(s.db.Questions, q)
	return s.save()
}

func (s *Store) ingestQuestions() error {
	dir := filepath.Join(s.Home.State, "questions-inbox")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type record struct {
		path     string
		question Question
		invalid  error
	}
	var records []record
	for _, entry := range entries[:min(len(entries), maxQuestions)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var q Question
		var invalid error
		if info.Size() > 12<<10 {
			invalid = errors.New("question exceeds its size limit")
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &q) != nil {
				invalid = errors.New("invalid question JSON")
			}
		}
		records = append(records, record{path, q, invalid})
	}
	// Publication caps the inbox at maxQuestions. Hash filenames carry no
	// chronology: admit its oldest records first so capacity deferral cannot
	// strand unseen questions behind a newer retired timestamp.
	slices.SortStableFunc(records, func(a, b record) int {
		return a.question.CreatedAt.Compare(b.question.CreatedAt)
	})
	for _, record := range records {
		q, invalid, path := record.question, record.invalid, record.path
		if invalid == nil {
			invalid = s.acceptQuestion(q)
		}
		if errors.Is(invalid, ErrStorage) {
			return invalid
		}
		if errors.Is(invalid, ErrDeferred) {
			continue
		}
		if invalid != nil {
			// Keep a bounded diagnostic, never raw question data or a poison
			// file that can stop subsequent records from being accepted.
			s.mu.Lock()
			s.db.Issues = append(s.db.Issues, "User question rejected: "+bounded(invalid.Error(), 300))
			if len(s.db.Issues) > 20 {
				s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
			}
			err := s.save()
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) supersedeQuestions() error {
	// Missing evidence cannot establish replacement: an unreadable
	// registration leaves the CFO's questions alone, and an unreadable queue
	// leaves the goblins' questions alone.
	identity := ""
	if file, err := openPrimary(filepath.Join(s.Home.State, "primary.json")); err == nil {
		_, current, err := decodePrimary(file)
		_ = file.Close()
		if err == nil {
			identity = current
		}
	}
	pending, pendingErr := wake.Pending(s.Home.State)
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Questions {
		q := &s.db.Questions[i]
		if q.Status != "pending" {
			continue
		}
		if q.Task == "" {
			if identity != "" && q.Identity != identity {
				q.Status, q.Message = "superseded", "The CFO session changed. Ask the current CFO to reissue this question."
				changed = true
			}
			continue
		}
		meta, err := state.ReadTaskMeta(s.Home.State, q.Task)
		switch {
		case errors.Is(err, os.ErrNotExist) || err == nil && goblinIdentity(meta) != q.Identity:
			q.Status, q.Message = "superseded", "The goblin's task restarted or ended, so its question no longer applies."
			changed = true
		case pendingErr == nil && !slices.ContainsFunc(pending, func(r wake.Record) bool { return r.Seq == q.Seq }):
			q.Status, q.Message = "superseded", "The CFO already handled this question."
			changed = true
		}
	}
	if changed {
		return s.save()
	}
	return nil
}

func (s *Store) questionAnswer(a Action) error {
	for _, q := range s.db.Questions {
		if q.ID != a.QuestionID {
			continue
		}
		if (a.Kind == "goblin_answer") != (q.Task != "") {
			return errors.New("answer this question through the recipient it names")
		}
		if q.Task != "" {
			if meta, err := state.ReadTaskMeta(s.Home.State, q.Task); err != nil || goblinIdentity(meta) != q.Identity {
				return errors.New("the goblin's task restarted or ended, so this answer has no recipient")
			}
		}
		if q.Identity != a.Generation {
			return errors.New("this question belongs to a previous CFO session")
		}
		if q.AnswerID != "" || q.Status != "pending" {
			return errors.New("an answer is already recorded for this question; inspect its outcome")
		}
		if a.AnswerKind != "" && a.AnswerKind != "option" && a.AnswerKind != "other" {
			return errors.New("answer kind must be option or other")
		}
		if a.AnswerKind != "other" && (len(q.Options) > 0 || a.AnswerKind == "option") && !slices.Contains(q.Options, a.Text) {
			return errors.New("select one of the CFO's supplied choices")
		}
		return nil
	}
	return errors.New("this user question is unavailable")
}

// clearQuestion closes a question the Overlord cleared after it closed without
// an answer. Clearing one already cleared changes nothing.
func (s *Store) clearQuestion(id, identity string) (Evaluation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Questions, func(q Question) bool { return q.ID == id && q.Identity == identity })
	if i < 0 {
		return Evaluation{}, fmt.Errorf("%w: the question is gone; nothing was cleared", ErrRejected)
	}
	if s.db.Questions[i].Status == "cleared" {
		return Evaluation{Reason: "The question was already cleared."}, nil
	}
	if s.db.Questions[i].Status != "superseded" && s.db.Questions[i].Status != "failed" {
		return Evaluation{}, fmt.Errorf("%w: the question is %s; only one that closed without an answer can be cleared", ErrRejected, s.db.Questions[i].Status)
	}
	s.db.Questions[i].Status = "cleared"
	if err := s.save(); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Cleared from the Command Center."}, nil
}
