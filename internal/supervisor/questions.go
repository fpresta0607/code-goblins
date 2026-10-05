package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/spawn"
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
	// AnsweredIn says where an answer given outside the question's own card
	// came from, such as page for the Overlord's answer on the review page
	// that carries it.
	AnsweredIn string `json:"answered_in,omitempty"`
	// Page is, on the board only, the open review item whose page carries
	// this question, so the Command Center shows the two as one.
	Page string `json:"page,omitempty"`
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

// goblinAsker proves the calling process runs under the task's own terminal,
// the same proof registration uses for the CFO: the program in its native
// terminal. No other process can ask in a goblin's name.
func goblinAsker(stateDir, taskID string) (state.TaskMeta, error) {
	meta, err := state.ReadTaskMeta(stateDir, taskID)
	if err != nil {
		return meta, fmt.Errorf("task %s has no live record: %w", taskID, err)
	}
	if meta.Backend != "native" {
		return meta, fmt.Errorf("task %s runs in no native terminal, so nothing proves who speaks in its name", taskID)
	}
	_, _, err = nativeProgram(stateDir, meta.ID)
	return meta, err
}

// SurfaceNotify records a goblin's blocking notify with choices for the CFO
// to answer, labelled with the goblin. A notify without choices and a failed
// notify stay prose in the CFO's wake queue.
// images, one for each choice in order, must already have passed
// ReviewImages: SurfaceNotify records them without checking the files.
// detail is the notify as the goblin wrote it: the queue holds record's
// one-line form, and the question keeps its own line breaks.
func SurfaceNotify(stateDir, taskID string, record wake.Record, detail string, images []string) error {
	asked := record
	asked.Detail = detail
	question, options, ok := wake.Question(asked)
	if !ok || len(options) == 0 {
		return nil
	}
	options, recommended := questionChoices(options)
	meta, err := goblinAsker(stateDir, taskID)
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

// SendGoblin delivers text to the goblin a question named, through its own
// native terminal. The delivery is pinned to the task generation that asked,
// so a restarted task never receives an answer meant for its predecessor.
func (c *CFOConnection) SendGoblin(ctx context.Context, taskID, identity, text string) (Evaluation, error) {
	meta, err := state.ReadTaskMeta(c.State, taskID)
	if err != nil || goblinIdentity(meta) != identity {
		return Evaluation{}, fmt.Errorf("%w: the goblin's task restarted or ended; nothing was sent", ErrRejected)
	}
	if meta.Backend != "native" {
		return Evaluation{}, fmt.Errorf("%w: the goblin's task was recorded in Herdr by an older build, which this build cannot reach; nothing was sent", ErrRejected)
	}
	if err := typeIntoGoblin(ctx, c.State, meta, fleet.Stamp(oneLine(text))); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Accepted by the goblin in its native terminal."}, nil
}

// typeIntoGoblin delivers text into a goblin's native terminal. It is a
// variable so a test can act while a delivery is in flight.
var typeIntoGoblin = func(ctx context.Context, stateDir string, meta state.TaskMeta, text string) error {
	sender := spawn.Service{StateDir: stateDir, PromptSince: func(taskID, generation string, since time.Time) (bool, error) {
		return NativePromptSince(stateDir, taskID, generation, since)
	}}
	return sender.SendNative(ctx, meta, text)
}

// answerGoblin returns the Overlord's board answer to the goblin that asked.
// The goblin's blocked notify then reads answered, so neither the monitor
// nor the CFO asks it again; retiring it is still the CFO's ordinary ack.
func (s *Service) answerGoblin(ctx context.Context, a Action) (Evaluation, error) {
	if s.Options.CFO == nil {
		return Evaluation{}, fmt.Errorf("%w: message transport is unavailable", ErrRejected)
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
	sent := time.Now().UTC()
	text := fmt.Sprintf("The Overlord answered your question on the board. Question: %s %s: %s", q.Text, label, a.Text)
	isSaved, err := savePausedAnswer(s.Store.Home.State, q.Task, q.Identity, text)
	if err != nil {
		return Evaluation{}, err
	}
	result := Evaluation{Reason: "Answer saved for the goblin's Resume."}
	if !isSaved {
		result, err = s.Options.CFO.SendGoblin(ctx, q.Task, q.Identity, text)
	}
	if errors.Is(err, fleet.ErrQueuedBehindTurn) {
		result, err = s.behindGoblinsTurn(q.Task, sent, "Submitted to the goblin while it was working; it takes the answer when its current turn ends."), nil
	}
	if err != nil {
		return result, err
	}
	if err := wake.MarkAnswered(s.Store.Home.State, q.Seq, wake.AnsweredByOverlord, a.Text); err != nil {
		result.Reason += " The CFO's notify still reads unanswered: " + err.Error()
	}
	return result, nil
}

// answersInbox is where cfo answer once spooled its answers. They now reach
// the supervisor only over its pipe, so ingest refuses any file found here.
const answersInbox = "answers-inbox"

// questionArrival is how long a goblin's question may take to reach the board
// after its notify is recorded, which comes first; one still absent after it
// never reached the board.
var questionArrival = 30 * time.Second

// cfoAnswer is one answer the CFO gave with cfo answer.
type cfoAnswer struct {
	QuestionID string `json:"question_id"`
	Option     string `json:"option"`
	Answer     string `json:"answer"`
	// In names where the Overlord gave the answer the CFO records, such as
	// chat; it is empty for the CFO's own answer.
	In string    `json:"in,omitempty"`
	At time.Time `json:"at"`
}

// AnswerGoblin answers a goblin's blocked question as the CFO, the structured
// counterpart of cfo send: ref is the question's ID or its notify's wake
// sequence, and option names one of its choices in full or by its first word
// (the a | b labels goblins give their options). The goblin receives the
// choice the way cfo send types, its notify reads answered so cfo drain
// retires it without --ack-blocking, and the supervisor records which choice
// closed the question, that the CFO gave it, and when. Only the registered
// primary CFO may answer. It returns the choice it delivered, and whether the
// goblin was working, so the answer waits in its input until its current turn
// ends: that is a delivery, submitted once, and it is recorded like one, so
// neither the CFO nor the board sends a second decision.
func (c *CFOConnection) AnswerGoblin(ctx context.Context, ref, option, note string) (chosen string, queued bool, err error) {
	identity, release, err := c.CallerIdentity()
	if err != nil {
		return "", false, err
	}
	defer release()
	seq, err := questionSeq(ref)
	if err != nil {
		return "", false, err
	}
	unlock, err := answerLock(c.State, seq)
	if err != nil {
		return "", false, err
	}
	defer unlock()
	pending, err := wake.Pending(c.State)
	if err != nil {
		return "", false, fmt.Errorf("read the wake queue: %w", err)
	}
	i := slices.IndexFunc(pending, func(r wake.Record) bool { return r.Seq == seq })
	if i < 0 {
		return "", false, fmt.Errorf("notify %d is not waiting: it was never raised or the CFO already handled it", seq)
	}
	record := pending[i]
	if record.Answered != "" {
		return "", false, fmt.Errorf("notify %d was already answered: %s", seq, record.Answered)
	}
	asked, options, ok := wake.Question(record)
	if !ok || len(options) == 0 {
		return "", false, fmt.Errorf("notify %d asks no multiple-choice question; answer it with cfo send", seq)
	}
	choices, _ := questionChoices(options)
	if chosen, err = pickChoice(choices, option); err != nil {
		return "", false, err
	}
	id := fmt.Sprintf("notify-%s-%d", record.Key, seq)
	if ref != strconv.Itoa(seq) && ref != id {
		return "", false, fmt.Errorf("%s is not the question of notify %d, which is %s", ref, seq, id)
	}
	q, err := readQuestion(c.State, id)
	onBoard := err == nil
	if errors.Is(err, fs.ErrNotExist) && time.Since(record.Time) >= questionArrival {
		// Its notify could not show it on the board, such as one the board's
		// proof refused, so it never will: the choice goes straight to the
		// goblin that asked.
		if q, err = askerOf(c.State, record); err != nil {
			return "", false, err
		}
	} else if err != nil {
		return "", false, fmt.Errorf("question %s has not reached the board yet (%v); try again in a moment", id, err)
	}
	if q.AnswerID != "" && q.Status != "failed" {
		return "", false, fmt.Errorf("the Overlord is answering %s on the board (its answer is %s); nothing was sent", id, q.Status)
	}
	answer := withNote(chosen, note)
	_, err = c.SendGoblin(ctx, q.Task, q.Identity, fmt.Sprintf("decision %d: %s", seq, answer))
	queued = errors.Is(err, fleet.ErrQueuedBehindTurn)
	if err != nil && !queued {
		return "", false, err
	}
	var unrecorded []error
	if err := wake.MarkAnswered(c.State, seq, wake.AnsweredByCFO, answer); err != nil {
		unrecorded = append(unrecorded, fmt.Errorf("notify %d still reads unanswered: %w", seq, err))
	}
	if onBoard {
		if err := c.recordAnswer(identity, q, id, chosen, answer, ""); err != nil {
			unrecorded = append(unrecorded, err)
		}
	}
	if err := logAFKAnswer(c.State, id, q.Task, asked, answer); err != nil {
		unrecorded = append(unrecorded, err)
	}
	if err := errors.Join(unrecorded...); err != nil {
		return chosen, queued, fmt.Errorf("delivered to %s; do not send it again, but %w", q.Task, err)
	}
	return chosen, queued, nil
}

// askerOf is the goblin that raised notify record, for a question that never
// reached the board: its task's current generation, when that generation
// started before the notify was recorded. A goblin that restarted since does
// not get an answer to a question it never asked.
func askerOf(stateDir string, record wake.Record) (Question, error) {
	meta, err := state.ReadTaskMeta(stateDir, record.Key)
	if err != nil {
		return Question{}, fmt.Errorf("%s has no live record, so notify %d reaches nobody: %w", record.Key, record.Seq, err)
	}
	started, err := strconv.ParseInt(strings.TrimPrefix(meta.SpawnGen, "s"), 10, 64)
	if err != nil || !strings.HasPrefix(meta.SpawnGen, "s") || time.Unix(0, started).After(record.Time) {
		return Question{}, fmt.Errorf("%s restarted or ended since it asked notify %d; nothing was sent", record.Key, record.Seq)
	}
	return Question{Task: meta.ID, Identity: goblinIdentity(meta)}, nil
}

// answerPlace is where the Overlord gave an answer the CFO records, in a few
// plain words, such as chat.
var answerPlace = regexp.MustCompile(`^[A-Za-z][A-Za-z ]{0,39}$`)

// RecordAnswer closes a question on the board with a choice already given
// some other way, and sends nobody anything. Only the registered primary CFO
// may do it. in names where the Overlord gave the choice, such as chat, and
// the card then reads as his answer there, recorded by the CFO; without it,
// the choice is the CFO's own, which only a goblin's question takes, such as
// a cfo answer whose delivery was left unconfirmed. The CFO's own question
// only the Overlord answers, so recording its answer names where.
// A goblin's question is recorded only once the CFO has handled its notify:
// it was acknowledged, or it reads answered. A notify still waiting
// unanswered is refused, since cfo answer is how that one is answered, and so
// is a question the Overlord is answering on the board. id is the question's
// ID, notify-<task>-<sequence> for a goblin's. It returns the choice it
// recorded.
func (c *CFOConnection) RecordAnswer(id, option, note, in string) (string, error) {
	identity, release, err := c.CallerIdentity()
	if err != nil {
		return "", err
	}
	defer release()
	if in != "" && !answerPlace.MatchString(in) {
		return "", errors.New("--in names where the Overlord answered in a few plain words, such as chat")
	}
	if in != "" {
		if err := overlordAway(c.State); err != nil {
			return "", err
		}
	}
	if _, err := strconv.Atoi(id); err == nil {
		return "", fmt.Errorf("--record-only takes the question's ID, such as notify-<task>-<sequence>, not the wake sequence %s", id)
	}
	if strings.HasPrefix(id, "notify-") {
		seq, err := questionSeq(id)
		if err != nil {
			return "", err
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
		if i := slices.IndexFunc(pending, func(r wake.Record) bool { return r.Seq == seq }); i >= 0 && pending[i].Answered == "" {
			return "", fmt.Errorf("notify %d is still waiting unanswered; answer it with cfo answer, which delivers it, or acknowledge it first if it was answered another way", seq)
		} else if i < 0 {
			acked, err := wake.Acked(c.State, seq)
			if err != nil {
				return "", fmt.Errorf("read the wake queue's ack floor: %w", err)
			}
			if !acked {
				return "", fmt.Errorf("notify %d was never raised", seq)
			}
		}
	} else if in == "" {
		return "", fmt.Errorf("%s is the CFO's own question, which only the Overlord answers: name where he answered it with --in, such as --in chat", id)
	}
	q, err := readQuestion(c.State, id)
	if err != nil {
		return "", fmt.Errorf("question %s is not on the board: %w", id, err)
	}
	if q.AnswerID != "" && q.Status != "failed" {
		return "", fmt.Errorf("the Overlord is answering %s on the board (its answer is %s); nothing was recorded", q.ID, q.Status)
	}
	if q.Status == "succeeded" && !answeredByAck(q) {
		return "", fmt.Errorf("%s is already answered: %s", q.ID, q.Answer)
	}
	choices, _ := questionChoices(q.Options)
	chosen, err := pickChoice(choices, option)
	if err != nil {
		return "", err
	}
	if err := c.recordAnswer(identity, q, q.ID, chosen, withNote(chosen, note), in); err != nil {
		return "", err
	}
	if in == "" {
		if err := logAFKAnswer(c.State, q.ID, q.Task, q.Text, withNote(chosen, note)); err != nil {
			return chosen, fmt.Errorf("recorded on the board; do not record it again, but %w", err)
		}
	}
	return chosen, nil
}

// errOverlordAway refuses an answer recorded as the Overlord's while AFK mode
// is on: he is away, so nothing he is said to have answered is taken as his.
var errOverlordAway = errors.New("AFK mode is on: the Overlord is away, so no answer is recorded as his. The question stays held for him; record his answer once he is back and has turned AFK mode off")

// overlordAway is errOverlordAway while AFK mode is on, and the reason its
// switch cannot be read when it cannot.
func overlordAway(stateDir string) error {
	switched, err := afk.Read(stateDir)
	if err != nil {
		return err
	}
	if switched.On {
		return errOverlordAway
	}
	return nil
}

// logAFKAnswer logs an answer the CFO gave a goblin while AFK mode is on as a
// decision made under its authority, with the question and the answer as its
// evidence. It logs nothing while AFK mode is off.
func logAFKAnswer(stateDir, id, task, question, answer string) error {
	switched, err := afk.Read(stateDir)
	if err != nil || !switched.On {
		return err
	}
	evidence := bounded("asked: "+question+" answered: "+answer, 6000)
	if err := sendPipeRequest(stateDir, runPipeRequest{Kind: "afk-log", AFK: &afk.Entry{Kind: afk.KindAnswer, What: id, Task: task, Evidence: evidence}}); err != nil {
		return fmt.Errorf("AFK mode's log did not take the decision: %w", err)
	}
	return nil
}

// recordAnswer tells the board which choice closed a goblin's question and
// closes the goblin's waits on the Overlord up to it, since the goblin has
// what it waited for; a later wait is its own request.
func (c *CFOConnection) recordAnswer(identity string, q Question, id, chosen, answer, in string) error {
	var unrecorded []error
	if err := sendPipeRequest(c.State, runPipeRequest{Kind: "answer", Answer: &cfoAnswer{QuestionID: id, Option: chosen, Answer: answer, In: in, At: time.Now().UTC()}}); err != nil {
		unrecorded = append(unrecorded, fmt.Errorf("the board could not record it: %w", err))
	}
	if q.Task == "" {
		return errors.Join(unrecorded...)
	}
	waiting := func(r Review) bool {
		n, err := strconv.Atoi(strings.TrimPrefix(r.ID, "waiting-"+q.Task+"-"))
		return r.Task == q.Task && strings.HasPrefix(r.ID, "waiting-"+q.Task+"-") && err == nil && n <= q.Seq
	}
	if err := clearReviews(c.State, identity, "The CFO answered "+q.Task+"'s question.", waiting, false); err != nil {
		unrecorded = append(unrecorded, fmt.Errorf("its waits on the Overlord stay open: %w", err))
	}
	return errors.Join(unrecorded...)
}

// questionSeq reads the wake sequence a goblin question reference names: the
// sequence itself, or the question's ID, notify-<task>-<sequence>.
func questionSeq(ref string) (int, error) {
	seq, err := strconv.Atoi(ref)
	if i := strings.LastIndexByte(ref, '-'); err != nil && strings.HasPrefix(ref, "notify-") && i > 0 {
		seq, err = strconv.Atoi(ref[i+1:])
	}
	if err != nil || seq <= 0 {
		return 0, fmt.Errorf("%s names neither a goblin question nor its notify's wake sequence", ref)
	}
	return seq, nil
}

// withNote is the answer a goblin receives: the choice, then the note.
func withNote(chosen, note string) string {
	if note = strings.TrimSpace(note); note != "" {
		return chosen + ". " + note
	}
	return chosen
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
		data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
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
	data, err := fsx.ReadFile(filepath.Join(stateDir, "questions-inbox", hex.EncodeToString(sum[:])+".json"))
	if err != nil {
		return Question{}, err
	}
	var q Question
	if err := json.Unmarshal(data, &q); err != nil {
		return Question{}, errors.New("the question waiting in the inbox is unreadable")
	}
	return q, nil
}

// ingestAnswers refuses every answer file in the old answer inbox, since the
// CFO's answers reach the board only over the supervisor's pipe and a file
// there was written by some other process of this user, and records each
// answer the pipe took whose question can take it now.
func (s *Store) ingestAnswers() error {
	dir := filepath.Join(s.Home.State, answersInbox)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries[:min(len(entries), maxQuestions)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		s.mu.Lock()
		s.issue("CFO answer rejected: " + errFromInbox.Error())
		err := s.save()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.db.CFOAnswers) == 0 {
		return nil
	}
	var waiting []cfoAnswer
	for _, a := range s.db.CFOAnswers {
		switch err := s.applyCFOAnswer(a); {
		case errors.Is(err, errAnswerWaits):
			waiting = append(waiting, a)
		case err != nil:
			s.issue("CFO answer rejected: " + err.Error())
		}
	}
	if len(waiting) == len(s.db.CFOAnswers) {
		return nil
	}
	s.db.CFOAnswers = waiting
	return s.save()
}

// recordCFOAnswer records an answer the CFO gave with cfo answer, or keeps it
// for a later pass when its question cannot take it yet.
func (s *Store) recordCFOAnswer(a cfoAnswer) error {
	// An answer that reads as the Overlord's is refused while he is away,
	// whatever command or process sent it.
	if a.In != "" {
		if err := overlordAway(s.Home.State); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyCFOAnswer(a); errors.Is(err, errAnswerWaits) {
		if len(s.db.CFOAnswers) >= maxQuestions {
			return errors.New("the board already holds the most CFO answers it keeps waiting")
		}
		s.db.CFOAnswers = append(s.db.CFOAnswers, a)
	} else if err != nil {
		return err
	}
	return s.save()
}

// errAnswerWaits is an answer whose question cannot take it yet: the question
// still waits in its inbox, or the Overlord's board answer is on its way.
var errAnswerWaits = errors.New("its question cannot take an answer yet")

// answeredByAck reports whether q closed because the CFO acked its notify,
// which the CFO does once it answered the goblin, such as with cfo send: the
// board knows the CFO answered it but not which choice, which the CFO may
// still record.
func answeredByAck(q Question) bool {
	return q.Status == "succeeded" && q.AnsweredBy == "cfo" && q.Answer == ""
}

// applyCFOAnswer closes a question with the CFO's answer: which choice closed
// it, that the CFO gave it, and when. A question still pending takes it, and
// so does one closed by the CFO's ack before its choice was recorded, one
// superseded, or one whose board answer was refused because the CFO had just
// answered.
func (s *Store) applyCFOAnswer(a cfoAnswer) error {
	i := slices.IndexFunc(s.db.Questions, func(q Question) bool { return q.ID == a.QuestionID })
	if i < 0 {
		sum := sha256.Sum256([]byte(a.QuestionID))
		if _, err := os.Stat(filepath.Join(s.Home.State, "questions-inbox", hex.EncodeToString(sum[:])+".json")); err == nil {
			return errAnswerWaits
		}
		return errors.New("its question is gone")
	}
	switch q := s.db.Questions[i]; {
	case q.Status == "queued":
		return errAnswerWaits
	case !slices.Contains([]string{"pending", "superseded", "failed"}, q.Status) && !answeredByAck(q):
		return errors.New("its question already closed as " + q.Status)
	}
	q, at := &s.db.Questions[i], a.At
	q.Status, q.Message, q.AnswerID = "succeeded", "Answered by the CFO.", ""
	q.Answer, q.AnswerKind = a.Answer, "option"
	q.AnsweredOption, q.AnsweredBy, q.AnsweredAt = a.Option, "cfo", &at
	if a.In != "" {
		q.Message, q.AnsweredBy, q.AnsweredIn = "You answered in "+a.In+"; the CFO recorded it.", "overlord", a.In
		s.closePagesOfQuestion(*q, "overlord", "You answered its question in "+a.In+": "+a.Answer)
		return nil
	}
	s.closePagesOfQuestion(*q, "cfo", "The CFO answered its question: "+a.Answer)
	return nil
}

// issue records a bounded diagnostic for the board, keeping the last 20.
func (s *Store) issue(text string) {
	s.db.Issues = append(s.db.Issues, text)
	if len(s.db.Issues) > 20 {
		s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
	}
}

// CallerIdentity proves this process descends from the registered primary
// CFO, including its creation time, and that its native identity is live, and
// returns that identity. The registration stays open, so it cannot change,
// until release is called.
func (c *CFOConnection) CallerIdentity() (string, func(), error) {
	return c.identityOf(os.Getpid(), time.Now())
}

// identityOf is CallerIdentity for any process that was running at connected,
// such as a client of the supervisor's run request pipe: a process that
// started later took the PID of the one that connected, and proves nothing.
func (c *CFOConnection) identityOf(pid int, connected time.Time) (string, func(), error) {
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
	proven := descendsFrom(entries, p.Process)
	if !proven && p.Host != "" && len(entries) > 0 {
		// A chain of parents that stops short, as a Cygwin or MSYS exec leaves
		// it, is proven by the proof value of the CFO's native terminal.
		if record, err := host.ReadRecord(c.State, p.Host); err == nil {
			if env, err := proc.Environment(pid); err == nil {
				program, err := terminalProgram(record, env)
				proven = err == nil && program.PID == p.Process.PID && program.Start.Equal(p.Process.Start)
			}
		}
	}
	if !proven || entries[0].Start.After(connected) {
		release()
		return "", nil, errors.New("this process does not run under the registered CFO")
	}
	if err := c.verify(p); err != nil {
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
func (c *CFOConnection) PublishQuestion(id, text string, options []string, recommended string) error {
	identity, release, err := c.CallerIdentity()
	if err != nil {
		return err
	}
	defer release()
	q := Question{ID: id, Identity: identity, Text: text, Options: options, Recommended: recommended, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := validQuestion(q); err != nil {
		return err
	}
	return sendPipeRequest(c.State, runPipeRequest{Kind: "question", Question: &q})
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
		data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
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
	if data, err := fsx.ReadFile(path); err == nil {
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
	// A goblin that asks again has moved past its earlier question, so the
	// newer one replaces it and the Command Center shows one card.
	for i := range s.db.Questions {
		if old := &s.db.Questions[i]; q.Task != "" && old.Task == q.Task && old.Identity == q.Identity && old.Status == "pending" && old.AnswerID == "" && old.Seq < q.Seq {
			old.Status, old.Message = "superseded", "The goblin asked again, so its newer question replaces this one."
		}
	}
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
			data, err := fsx.ReadFile(path)
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &q) != nil {
				invalid = errors.New("invalid question JSON")
			}
		}
		if invalid == nil && q.Task == "" {
			invalid = errFromInbox
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

// supersedeQuestions closes the goblins' questions that no longer apply. The
// CFO's own questions are never superseded: they follow the home's CFO across
// a restart (followCFO).
func (s *Store) supersedeQuestions() error {
	// Missing evidence cannot establish replacement: an unreadable queue
	// leaves the goblins' questions alone.
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
			continue
		}
		meta, err := state.ReadTaskMeta(s.Home.State, q.Task)
		switch {
		case errors.Is(err, os.ErrNotExist) || err == nil && goblinIdentity(meta) != q.Identity:
			q.Status, q.Message = "superseded", "The goblin's task restarted or ended, so its question no longer applies."
			changed = true
		case pendingErr == nil && !slices.ContainsFunc(pending, func(r wake.Record) bool { return r.Seq == q.Seq }):
			// The CFO acks a goblin's question once it answered it, so it
			// closes as answered by the CFO, with the check of any answer.
			at := time.Now().UTC()
			q.Status, q.Message, q.AnsweredBy, q.AnsweredAt = "succeeded", "Answered by the CFO.", "cfo", &at
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

// clearQuestion closes question i, which the Overlord cleared from the
// Command Center: one that closed without an answer, or one still waiting on
// him that he answered elsewhere or no longer needs, which he dismissed. It
// returns the dismissed question's ID, which the CFO must hear of, and nothing
// otherwise. The caller holds the store lock and has checked that the
// question may be cleared.
func (s *Store) clearQuestion(i int) []string {
	q := &s.db.Questions[i]
	dismissed := q.Status == "pending"
	q.Status = "cleared"
	if !dismissed {
		return nil
	}
	q.Message = "You dismissed it: answered elsewhere or no longer needed."
	return []string{q.ID}
}
