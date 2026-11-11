package fleettree

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Engine is the model and effort a session's own record last names, each
// with when the entry that names it was written, so a reader can tell a
// setting the running harness made from one an earlier run of it left in
// the same conversation. A field the record does not name is left empty.
type Engine struct {
	// Model is the model's id, as an answer, a turn or a model change names
	// it.
	Model string
	// ModelName is what Claude Code's /model printed for the model its
	// picker set or kept, with nothing typed after it, until an answer names
	// the model by its id.
	ModelName string
	ModelAt   time.Time
	// ModelTyped is what was typed after Claude Code's /model, which the
	// harness then set or kept, as a start takes it, and ModelTypedAt when.
	// It stays while the answers since name that model, which they name by
	// its id alone, and goes once an answer names another or the picker sets
	// one.
	ModelTyped   string
	ModelTypedAt time.Time
	Effort       string
	EffortAt     time.Time
}

// CFOOwner is the owner SessionEngine reads the CFO's own conversation for.
// No task's id can spell it, and Forget keeps its reading.
const CFOOwner = "/cfo"

// engineLog is what one conversation has named of its session's engine, read
// on from where the last read ended.
type engineLog struct {
	harness, path string
	read          int64
	engine        Engine
	// command is the /model or /effort Claude Code recorded last, whose
	// result is the entry after it, and value what was typed after it.
	command, value string
}

var (
	// modelSet is the model Claude Code's /model says it set or kept, read
	// only from the start of its result, so a cancelled /model sets none:
	// "Set model to `Fable 5.2` and saved as your default for new sessions"
	// and "Kept model as `Fable 5.2`".
	modelSet = regexp.MustCompile("(?i)^(?:set model to|kept model as) `([^`]+)`")
	// effortSet is the level Claude Code's /effort says it set, from its
	// picker or from the level typed after it, read only from the start of its
	// success line ("Set effort level to xhigh: ..." or "Effort level set to
	// auto"), so a cancelled /effort, or a refusal that lists every level as
	// "Invalid argument: hgih. Valid options are: low, ...", sets none.
	effortSet = regexp.MustCompile(`(?i)^(?:set effort level to|effort level set to) (low|medium|high|xhigh|max|auto)\b`)
	// engineEntries are the words an entry that can name an engine holds, by
	// harness: no other entry is decoded at all. A tag is named without its
	// brackets, which a writer may escape.
	engineEntries = map[string][][]byte{
		"claude": {[]byte(`"type":"assistant"`), []byte(`command-name`), []byte(`local-command-stdout`)},
		"codex":  {[]byte(`"turn_context"`)},
		"pi":     {[]byte(`"model_change"`), []byte(`"thinking_level_change"`), []byte(`"assistant"`)},
	}
)

// SessionEngine reads the engine the conversation at path names, which
// harness wrote, for owner: a task, or the CFO's terminal. It reads on from
// where owner's last read of that conversation ended.
func (r *Reader) SessionEngine(owner, harness, path string) (Engine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionEngine(owner, harness, path)
}

// sessionEngine is SessionEngine for a caller that holds the reader.
func (r *Reader) sessionEngine(owner, harness, path string) (Engine, error) {
	harness = strings.ToLower(harness)
	if r.engines == nil {
		r.engines = map[string]*engineLog{}
	}
	log := r.engines[owner]
	if log == nil || log.path != path || log.harness != harness {
		log = &engineLog{harness: harness, path: path}
		r.engines[owner] = log
	}
	err := log.update()
	return log.engine, err
}

// EngineAgain reads on the engine of the conversation owner's last read was
// of, and reports whether owner has read one that can still be read.
func (r *Reader) EngineAgain(owner string) (Engine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	log := r.engines[owner]
	if log == nil || log.update() != nil {
		return Engine{}, false
	}
	return log.engine, true
}

// update folds the entries written since the last read. A conversation that
// shrank was replaced, and is read again from its start.
func (l *engineLog) update() error {
	file, err := fsx.Open(l.path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < l.read {
		*l = engineLog{harness: l.harness, path: l.path}
	}
	l.read, err = readOn(context.Background(), file, l.read, info.Size(), func(start int64, lines [][]byte) (int64, error) {
		for _, line := range lines {
			l.entry(line)
			start += int64(len(line)) + 1
		}
		return start, nil
	})
	return err
}

// entry folds one entry of the conversation into the engine.
func (l *engineLog) entry(line []byte) {
	if !containsAny(line, engineEntries[l.harness]) {
		return
	}
	switch l.harness {
	case "claude":
		l.claudeEntry(line)
	case "codex":
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Model  string `json:"model"`
				Effort string `json:"effort"`
			} `json:"payload"`
		}
		// Each turn's context names the model and effort it runs with, as
		// the session's /model last set them.
		if json.Unmarshal(line, &entry) == nil && entry.Type == "turn_context" {
			l.model(entry.Payload.Model, entry.Timestamp)
			l.effort(entry.Payload.Effort, entry.Timestamp)
		}
	case "pi":
		var entry piSessionEntry
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		switch entry.Type {
		case "model_change":
			l.model(strings.Trim(entry.Provider+"/"+entry.ModelID, "/"), at)
		case "thinking_level_change":
			l.effort(entry.ThinkingLevel, at)
		case "message":
			var said piMessage
			if json.Unmarshal(entry.Message, &said) == nil && said.Role == "assistant" {
				l.model(strings.Trim(said.Provider+"/"+said.Model, "/"), at)
			}
		}
	}
}

// claudeEntry folds one entry of a Claude Code conversation: an answer of
// the session's own, which names its model and the effort it ran at, and a
// /model or /effort with what it printed.
func (l *engineLog) claudeEntry(line []byte) {
	var entry struct {
		Type        string    `json:"type"`
		Timestamp   time.Time `json:"timestamp"`
		IsSidechain bool      `json:"isSidechain"`
		Effort      string    `json:"effort"`
		Message     struct {
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.IsSidechain {
		return
	}
	switch entry.Type {
	case "assistant":
		// A notice Claude Code writes itself, such as a usage limit's, names
		// no model ("<synthetic>") and ran at no effort.
		if entry.Message.Model != syntheticModel {
			if entry.Message.Model != "" && !NamesClaudeModel(l.engine.ModelTyped, entry.Message.Model) {
				l.engine.ModelTyped, l.engine.ModelTypedAt = "", time.Time{}
			}
			l.model(entry.Message.Model, entry.Timestamp)
			l.effort(entry.Effort, entry.Timestamp)
		}
	case "user":
		var text string
		if json.Unmarshal(entry.Message.Content, &text) != nil {
			return
		}
		text = strings.TrimSpace(text)
		command, value := l.command, l.value
		l.command, l.value = "", ""
		if match := commandName.FindStringSubmatch(text); strings.HasPrefix(text, "<command-") && match != nil {
			l.command = strings.TrimSpace(match[1])
			if typed := commandArgs.FindStringSubmatch(text); typed != nil {
				l.value = strings.TrimSpace(typed[1])
			}
			return
		}
		if !strings.HasPrefix(text, "<local-command-stdout>") {
			return
		}
		for _, match := range commandStdout.FindAllStringSubmatch(text, -1) {
			printed := strings.TrimSpace(ansi.ReplaceAllString(match[3], ""))
			if match[2] != "stdout" {
				continue
			}
			if name := modelSet.FindStringSubmatch(printed); name != nil && command == "/model" {
				if value == "" {
					l.engine.Model, l.engine.ModelName, l.engine.ModelAt = "", name[1], entry.Timestamp.UTC()
					l.engine.ModelTyped, l.engine.ModelTypedAt = "", time.Time{}
				} else {
					l.engine.ModelTyped, l.engine.ModelTypedAt = value, entry.Timestamp.UTC()
				}
			}
			if level := effortSet.FindStringSubmatch(printed); level != nil && command == "/effort" {
				l.effort(strings.ToLower(level[1]), entry.Timestamp)
			}
		}
	}
}

// NamesClaudeModel reports whether named, a model as a start of Claude Code
// takes it, is the model id, as Claude Code's answers name it. A start takes
// a model's id or an alias for the newest model of its family, "opus" for
// claude-opus-5-5, and either with the context window asked for after it,
// "opus[1m]", while an answer names the id alone. An id is no alias of
// another, so claude-opus-5 does not name claude-opus-5-5.
func NamesClaudeModel(named, id string) bool {
	named = strings.TrimSuffix(named, "[1m]")
	return named == id || !strings.HasPrefix(named, "claude-") && strings.HasPrefix(id, "claude-"+named+"-")
}

// model keeps the model an entry written at names, when it names one.
func (l *engineLog) model(model string, at time.Time) {
	if model != "" {
		l.engine.Model, l.engine.ModelName, l.engine.ModelAt = model, "", at.UTC()
	}
}

// effort keeps the effort an entry written at names, when it names one.
func (l *engineLog) effort(effort string, at time.Time) {
	if effort != "" {
		l.engine.Effort, l.engine.EffortAt = effort, at.UTC()
	}
}

func containsAny(line []byte, words [][]byte) bool {
	for _, word := range words {
		if bytes.Contains(line, word) {
			return true
		}
	}
	return false
}
