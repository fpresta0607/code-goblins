package supervisor

import (
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A session has one record of its model and effort: a goblin's task record
// and the CEO's engine file. A spawn, a switch and a saved selection write
// it, the next start or resume reads it, and every view of the session shows
// it. What the harness itself is told, a /model or /effort typed in its
// terminal or sent by the board's own control, reaches that record from the
// harness's own session record (fleettree.Engine), read on the board's
// existing read of every live session, so the record follows what the
// harness runs rather than what it was started with (the Overlord,
// 2026-11-10: "It all should be one standard setting, centralized control,
// just different views").

// runningEngine is the model and effort engine names since its session
// started, as a record holds them, each empty when the session's own record
// has named none since. What a conversation held before the start is an
// earlier run's, which a switch or a resume has replaced in the record. A
// model typed after /model since the start is the model as typed, and
// isTyped says so; else it is the id an entry named since, a model its picker
// only named being the catalog's id for that name. An effort no start of the
// harness takes, such as Claude Code's auto, is the harness's own default,
// and a model that could not be handed to a start, such as Claude Code's
// default, which names whichever model it picks, is none.
func (s *Service) runningEngine(kind string, engine fleettree.Engine, started time.Time) (model string, isTyped bool, effort string) {
	if started.IsZero() {
		return "", false, ""
	}
	switch {
	case engine.ModelTypedAt.After(started):
		model, isTyped = engine.ModelTyped, true
	case engine.ModelAt.After(started):
		model = engine.Model
		if model == "" && engine.ModelName != "" {
			models, _ := readEngineModels(s.Options.Tree.Home, harness.Kind(kind))
			for _, listed := range models {
				if listed.ID == engine.ModelName || strings.EqualFold(listed.Name, engine.ModelName) {
					model = listed.ID
					break
				}
			}
		}
	}
	if !engineValue(model) {
		model = ""
	}
	if engine.EffortAt.After(started) && engine.Effort != "" {
		effort = "default"
		if slices.Contains(harness.Efforts(harness.Kind(kind)), engine.Effort) {
			effort = engine.Effort
		}
	}
	return model, isTyped, effort
}

// isRecordedModel reports whether recorded, the model a session's record
// names, is the model running, which its harness says it runs. A model typed
// after /model is one a start takes, so the record is that model only where
// it names it as typed. An id of Claude Code's answers names no alias and no
// context window, so a record that names that id by an alias or asks for a
// window keeps both while the session answers on it.
func isRecordedModel(kind, recorded, running string, isTyped bool) bool {
	return recorded == running || !isTyped && kind == "claude" && fleettree.NamesClaudeModel(recorded, running)
}

// keepEngine writes the model and effort goblin meta's own conversation
// names into its task record, under the record's own lock, and reports
// whether the record changed. A record another command holds, or one of a
// generation that has ended since, is left alone: the next read finds the
// engine again.
func (s *Service) keepEngine(meta state.TaskMeta, engine fleettree.Engine) (isChanged bool, err error) {
	model, isTyped, effort := s.runningEngine(meta.Harness, engine, spawnTime(meta.SpawnGen))
	if (model == "" || isRecordedModel(meta.Harness, meta.Model, model, isTyped)) && (effort == "" || effort == meta.Effort) {
		return false, nil
	}
	stateDir := s.Store.Home.State
	metadataLock := state.MetadataLockName(meta.ID)
	if _, err := lock.AcquireExclusiveNamed(stateDir, metadataLock); err != nil {
		if errors.Is(err, lock.ErrHeld) {
			return false, nil
		}
		return false, err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, metadataLock)) }()
	path := state.TaskMetaPath(stateDir, meta.ID)
	record, err := state.ReadMeta(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && (record["spawn_gen"] != meta.SpawnGen || record["harness"] != meta.Harness) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if model != "" && !isRecordedModel(meta.Harness, record["model"], model, isTyped) {
		record["model"], isChanged = model, true
	}
	if effort != "" && effort != record["effort"] {
		record["effort"], isChanged = effort, true
	}
	if !isChanged {
		return false, nil
	}
	return true, state.WriteMeta(path, record)
}

// keepCEOEngine writes the model and effort the CEO's own conversation names
// into its engine file, and reports whether the file changed. It does so for
// a CEO that runs in the board's native terminal, whose conversation the
// fleet home's folder holds. A selection that waits to be applied, or one a
// problem holds for the CEO's next start, is the Overlord's choice and stays
// as saved; one that names another harness than the CEO was started as gives
// way once the running CEO names its own engine, since the file is the record
// of the session that runs.
func (s *Service) keepCEOEngine() (isChanged bool, err error) {
	stateDir := s.Store.Home.State
	ceo := readCEOState(stateDir)
	_, session, _ := strings.Cut(ceo.session, "/")
	if !ceo.registered || ceo.terminal != NativeCEOTerminal || session == "" || !fleettree.HoldsTranscript(s.Options.Tree.Home, ceo.harness, s.Store.Home.Root, session) {
		return false, nil
	}
	path := fleettree.HeldTranscript(s.Options.Tree.Home, ceo.harness, s.Store.Home.Root, session)
	if path == "" {
		return false, nil
	}
	engine, err := s.Options.Tree.SessionEngine(fleettree.CEOOwner, ceo.harness, path)
	if err != nil {
		return false, nil
	}
	model, isTyped, effort := s.runningEngine(ceo.harness, engine, ceo.since)
	if model == "" && effort == "" {
		return false, nil
	}
	// A start or a selection that holds the board's starts may run long, and
	// the next read finds the engine again.
	if !s.starts.TryLock() {
		return false, nil
	}
	defer s.starts.Unlock()
	if current := readCEOState(stateDir); !current.registered || current.session != ceo.session || !current.since.Equal(ceo.since) {
		return false, nil
	}
	recorded, err := ReadCEOEngine(stateDir)
	isSaved := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// A selection the fleet cannot read is the board's to report.
		return false, nil
	}
	switch {
	case isSaved && recorded.Pending, isSaved && recorded.Harness == ceo.harness && recorded.Problem != "":
		return false, nil
	case !isSaved, recorded.Harness != ceo.harness:
		if isSaved && recorded.Overridden != ceo.harness || model == "" {
			return false, nil
		}
		if effort == "" {
			effort = "default"
		}
		recorded = CEOEngine{Harness: ceo.harness, Model: model, Effort: effort}
	default:
		if model != "" && !isRecordedModel(ceo.harness, recorded.Model, model, isTyped) {
			recorded.Model, isChanged = model, true
		}
		if effort != "" && effort != recorded.Effort {
			recorded.Effort, isChanged = effort, true
		}
		if !isChanged {
			return false, nil
		}
	}
	return true, writeCEOEngine(stateDir, recorded)
}

// keepStreamedEngine keeps the engine record of the session a message view
// streams, the CEO's with no task, as its conversation says something new,
// so a view that is open shows a change within the stream's own tick rather
// than at the next read of every tree. A goblin's conversation is the one
// that read last found, and a record that cannot be written is that read's
// to report.
func (s *Service) keepStreamedEngine(task string) {
	isChanged := false
	if task == "" {
		isChanged, _ = s.keepCEOEngine()
	} else if meta, err := state.ReadTaskMeta(s.Store.Home.State, task); err == nil {
		if engine, isRead := s.Options.Tree.EngineAgain(task); isRead {
			isChanged, _ = s.keepEngine(meta, engine)
		}
	}
	if isChanged {
		s.notify()
	}
}
