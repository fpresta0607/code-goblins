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
	"strconv"
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
	// AnsweredOption, AnsweredBy and AnsweredAt record which choice closed
	// the question (empty for a written answer), who gave it (cfo or
	// overlord) and when, so a closed question shows its answer.
	AnsweredOption string     `json:"answered_option,omitempty"`
	AnsweredBy     string     `json:"answered_by,omitempty"`
	AnsweredAt     *time.Time `json:"answered_at,omitempty"`
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
	options, recommended := questionChoices(options)
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

// questionChoices is a notify's choices as the Command Center shows them: a
// goblin marks the one it recommends the way AskUserQuestion does, by ending
// it with "(Recommended)".
func questionChoices(options []string) ([]string, string) {
	choices := slices.Clone(options)
	recommended := ""
	for i, option := range choices {
		if choice, marked := strings.CutSuffix(option, "(Recommended)"); marked {
			choices[i] = strings.TrimSpace(choice)
			if recommended == "" {
				recommended = choices[i]
			}
		}
	}
	return choices, recommended
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
	unlock, err := answerLock(s.Store.Home.State, q.Seq)
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: the CFO is answering this question with cfo answer (%v); nothing was sent", ErrRejected, err)
	}
	defer unlock()
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
	if err := wake.MarkAnswered(s.Store.Home.State, q.Seq, wake.AnsweredByOverlord, a.Text); err != nil {
		result.Reason += " The CFO's notify still reads unanswered: " + err.Error()
	}
	return result, nil
}

// answersInbox holds the answers cfo answer spooled for the supervisor to
// record on their questions, one file per question named like its inbox file.
const answersInbox = "answers-inbox"

// cfoAnswer is one answer the CFO gave with cfo answer.
type cfoAnswer struct {
	QuestionID string    `json:"question_id"`
	Option     string    `json:"option"`
	Answer     string    `json:"answer"`
	At         time.Time `json:"at"`
}

// AnswerGoblin answers a goblin's blocked question as the CFO, the structured
// counterpart of cfo send: ref is the question's ID or its notify's wake
// sequence, and option names one of its choices in full or by its first word
// (the a | b labels goblins give their options). The goblin receives the
// choice the way cfo send types, its notify reads answered so cfo drain
// retires it without --ack-blocking, and the supervisor records which choice
// closed the question, that the CFO gave it, and when. Only the registered
// primary CFO may answer. It returns the choice it delivered.
func (c *CFOConnection) AnswerGoblin(ctx context.Context, ref, option, note string) (string, error) {
	_, release, err := c.CallerIdentity(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	seq, err := strconv.Atoi(ref)
	if i := strings.LastIndexByte(ref, '-'); err != nil && strings.HasPrefix(ref, "notify-") && i > 0 {
		seq, err = strconv.Atoi(ref[i+1:])
	}
	if err != nil || seq <= 0 {
		return "", fmt.Errorf("%s names neither a goblin question nor its notify's wake sequence", ref)
	}
	unlock, err := answerLock(c.State, seq)
	if err != nil {
		return "", err
	}
	defer unlock()
	pending, err := wake.Pending(c.State)
	if err != nil {
		return "", fmt.Errorf("read the wake queue: %w", err)
	}
	i := slices.IndexFunc(pending, func(r wake.Record) bool { return r.Seq == seq })
	if i < 0 {
		return "", fmt.Errorf("notify %d is not waiting: it was never raised or the CFO already handled it", seq)
	}
	record := pending[i]
	if record.Answered != "" {
		return "", fmt.Errorf("notify %d was already answered: %s", seq, record.Answered)
	}
	_, options, ok := wake.Question(record)
	if !ok || len(options) == 0 {
		return "", fmt.Errorf("notify %d asks no multiple-choice question; answer it with cfo send", seq)
	}
	choices, _ := questionChoices(options)
	chosen, err := pickChoice(choices, option)
	if err != nil {
		return "", err
	}
	id := fmt.Sprintf("notify-%s-%d", record.Key, seq)
	if ref != strconv.Itoa(seq) && ref != id {
		return "", fmt.Errorf("%s is not the question of notify %d, which is %s", ref, seq, id)
	}
	q, err := readQuestion(c.State, id)
	if err != nil {
		return "", fmt.Errorf("question %s has not reached the board yet (%v); try again in a moment", id, err)
	}
	if q.AnswerID != "" && q.Status != "failed" {
		return "", fmt.Errorf("the Overlord is answering %s on the board (its answer is %s); nothing was sent", id, q.Status)
	}
	answer := chosen
	if note = strings.TrimSpace(note); note != "" {
		answer += ". " + note
	}
	if _, err := c.SendGoblin(ctx, q.Task, q.Identity, fmt.Sprintf("decision %d: %s", seq, answer)); err != nil {
		return "", err
	}
	var unrecorded []error
	if err := spoolAnswer(c.State, cfoAnswer{QuestionID: id, Option: chosen, Answer: answer, At: time.Now().UTC()}); err != nil {
		unrecorded = append(unrecorded, fmt.Errorf("the board could not record it: %w", err))
	}
	if err := wake.MarkAnswered(c.State, seq, wake.AnsweredByCFO, answer); err != nil {
		unrecorded = append(unrecorded, fmt.Errorf("notify %d still reads unanswered: %w", seq, err))
	}
	if err := errors.Join(unrecorded...); err != nil {
		return chosen, fmt.Errorf("delivered to %s; do not send it again, but %w", q.Task, err)
	}
	return chosen, nil
}

// answerLock serializes the two ways one goblin question is answered, cfo
// answer and the board, so whichever comes second sees the other's answer;
// it waits briefly for the other one. The lock is the question's own, named
// by its notify's wake sequence, so answers to different goblins never wait
// on each other.
func answerLock(stateDir string, seq int) (func(), error) {
	name := fmt.Sprintf(".answer-%d.lock", seq)
	_, err := lock.AcquireExclusiveNamed(stateDir, name)
	for deadline := time.Now().Add(5 * time.Second); err != nil && time.Now().Before(deadline); {
		time.Sleep(25 * time.Millisecond)
		_, err = lock.AcquireExclusiveNamed(stateDir, name)
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.ReleaseExclusiveNamed(stateDir, name) }, nil
}

// pickChoice matches an answer to one choice: exactly, or by its first word
// when that names a single choice.
func pickChoice(choices []string, option string) (string, error) {
	option = strings.TrimSpace(option)
	if slices.Contains(choices, option) {
		return option, nil
	}
	chosen := ""
	for _, choice := range choices {
		if first, _, _ := strings.Cut(choice, " "); strings.EqualFold(first, option) {
			if chosen != "" {
				return "", fmt.Errorf("%q names more than one choice; give the choice in full: %s", option, strings.Join(choices, " | "))
			}
			chosen = choice
		}
	}
	if chosen == "" {
		return "", fmt.Errorf("%q is not one of the choices: %s", option, strings.Join(choices, " | "))
	}
	return chosen, nil
}

// readQuestion finds a question the supervisor holds or has yet to ingest,
// reading its files without opening a Store, as publish does.
func readQuestion(stateDir, id string) (Question, error) {
	if info, err := os.Stat(filepath.Join(stateDir, ".supervisor.json")); err == nil {
		if info.Size() > maxStateBytes {
			return Question{}, errors.New("supervisor state exceeds its bound")
		}
		data, err := os.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
		if err != nil {
			return Question{}, err
		}
		var db Database
		if err := json.Unmarshal(data, &db); err != nil {
			return Question{}, errors.New("supervisor question history is unreadable")
		}
		if i := slices.IndexFunc(db.Questions, func(q Question) bool { return q.ID == id }); i >= 0 {
			return db.Questions[i], nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Question{}, err
	}
	sum := sha256.Sum256([]byte(id))
	data, err := os.ReadFile(filepath.Join(stateDir, "questions-inbox", hex.EncodeToString(sum[:])+".json"))
	if err != nil {
		return Question{}, err
	}
	var q Question
	if err := json.Unmarshal(data, &q); err != nil {
		return Question{}, errors.New("the question waiting in the inbox is unreadable")
	}
	return q, nil
}

// spoolAnswer leaves a delivered CFO answer for the supervisor to record.
func spoolAnswer(stateDir string, a cfoAnswer) error {
	dir := filepath.Join(stateDir, answersInbox)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) >= maxQuestions {
		return errors.New("the answer inbox is full")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(a.QuestionID))
	return fsx.AtomicWriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), data)
}

// ingestAnswers records the answers cfo answer spooled: which choice closed
// the question, that the CFO gave it, and when. A question still pending
// takes its answer, and so does one superseded because the CFO drained its
// notify before this pass or one whose board answer was refused because the
// CFO had just answered; one still waiting in the question inbox, or whose
// board answer is still on its way, keeps its answer for a later pass.
func (s *Store) ingestAnswers() error {
	dir := filepath.Join(s.Home.State, answersInbox)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries[:min(len(entries), maxQuestions)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var a cfoAnswer
		reject := ""
		if len(data) > 12<<10 || json.Unmarshal(data, &a) != nil {
			reject = "the answer is unreadable"
		}
		if _, err := os.Stat(filepath.Join(s.Home.State, "questions-inbox", entry.Name())); reject == "" && err == nil {
			continue
		}
		s.mu.Lock()
		i := slices.IndexFunc(s.db.Questions, func(q Question) bool { return q.ID == a.QuestionID })
		if reject == "" && i >= 0 && s.db.Questions[i].Status == "queued" {
			s.mu.Unlock()
			continue
		}
		switch {
		case reject != "":
		case i < 0:
			reject = "its question is gone"
		case !slices.Contains([]string{"pending", "superseded", "failed"}, s.db.Questions[i].Status):
			reject = "its question already closed as " + s.db.Questions[i].Status
		default:
			q, at := &s.db.Questions[i], a.At
			q.Status, q.Message, q.AnswerID = "succeeded", "Answered by the CFO.", ""
			q.Answer, q.AnswerKind = a.Answer, "option"
			q.AnsweredOption, q.AnsweredBy, q.AnsweredAt = a.Option, "cfo", &at
		}
		if reject != "" {
			s.db.Issues = append(s.db.Issues, "CFO answer rejected: "+reject)
			if len(s.db.Issues) > 20 {
				s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
			}
		}
		err = s.save()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

// CallerIdentity proves this process descends from the registered primary
// CFO, including its creation time, and that its native identity is live, and
// returns that identity. The registration stays open, so it cannot change,
// until release is called.
func (c *CFOConnection) CallerIdentity(ctx context.Context) (string, func(), error) {
	return c.identityOf(ctx, os.Getpid())
}

// identityOf is CallerIdentity for any process, such as a client of the
// supervisor's run request pipe.
func (c *CFOConnection) identityOf(ctx context.Context, pid int) (string, func(), error) {
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
	entries, err := proc.Ancestry(pid, 32)
	if err != nil {
		release()
		return "", nil, err
	}
	if !descendsFrom(entries, p.Process) {
		release()
		return "", nil, errors.New("this process does not run under the registered CFO")
	}
	if err := c.verify(ctx, p); err != nil {
		release()
		return "", nil, err
	}
	return identity, release, nil
}

// descendsFrom reports whether an ancestry, the process itself first, reaches
// the registered CFO process, each ancestor created no later than its child:
// Windows reuses PIDs, so a parent created after its child is another process
// that took a dead parent's PID, and the chain ends there.
func descendsFrom(entries []proc.Entry, cfo lock.Info) bool {
	for i, entry := range entries {
		if i > 0 && entry.Start.After(entries[i-1].Start) {
			return false
		}
		if entry.PID == cfo.PID && entry.Start.Equal(cfo.Start) {
			return true
		}
	}
	return false
}

// PublishQuestion is deliberately a local CFO operation, not a browser or
// worker-alert endpoint. The caller must descend from the registered primary
// process, including its creation time, and its native identity must be live.
func (c *CFOConnection) PublishQuestion(ctx context.Context, id, text string, options []string, recommended string) error {
	identity, release, err := c.CallerIdentity(ctx)
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
