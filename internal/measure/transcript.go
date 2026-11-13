package measure

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Spend is what one conversation spent on one model and effort in a window:
// its requests, their tokens, and when the first and the last of them were
// written. A harness that records no effort leaves it empty.
type Spend struct {
	Model    string
	Effort   string
	Requests int
	Usage
	First, Last time.Time
}

// spending is a conversation's spend by model and effort, in the order each
// first spent.
type spending []Spend

func (s *spending) add(model, effort string, at time.Time, usage Usage) {
	index := slices.IndexFunc(*s, func(spend Spend) bool { return spend.Model == model && spend.Effort == effort })
	if index < 0 {
		*s = append(*s, Spend{Model: model, Effort: effort, First: at})
		index = len(*s) - 1
	}
	spend := &(*s)[index]
	spend.Requests++
	spend.Usage.add(usage)
	spend.Last = at
}

// Worker is one child agent of a goblin's conversation: its agent type, or
// the role its harness spawned it in, and what it spent.
type Worker struct {
	Agent  string
	Spends []Spend
}

// eachLine calls each with every complete line of the file at path. A
// transcript its harness still writes may end in a line not yet whole,
// which is left for a later read.
func eachLine(path string, each func(line []byte)) error {
	file, err := fsx.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		each(line)
	}
}

// within says at falls after from and by to.
func within(at, from, to time.Time) bool { return at.After(from) && !at.After(to) }

// syntheticModel is what Claude Code names as the model of an entry it
// writes itself, such as a usage limit's notice, which spent nothing.
const syntheticModel = "<synthetic>"

// ClaudeSpend reads what the Claude Code transcript at path spent after from
// and by to. Claude Code writes one entry for each block of an answer and
// repeats the answer's usage on each, so an answer counts once.
func ClaudeSpend(path string, from, to time.Time) ([]Spend, error) {
	var spent spending
	counted := map[string]bool{}
	err := eachLine(path, func(line []byte) {
		if !bytes.Contains(line, []byte(`"usage"`)) {
			return
		}
		var entry struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					Input         int64 `json:"input_tokens"`
					CacheCreation int64 `json:"cache_creation_input_tokens"`
					CacheRead     int64 `json:"cache_read_input_tokens"`
					Output        int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" || entry.Message.Model == "" || entry.Message.Model == syntheticModel || !within(entry.Timestamp, from, to) || counted[entry.Message.ID] {
			return
		}
		if entry.Message.ID != "" {
			counted[entry.Message.ID] = true
		}
		usage := entry.Message.Usage
		spent.add(entry.Message.Model, "", entry.Timestamp, Usage{InputTokens: usage.Input + usage.CacheCreation + usage.CacheRead, OutputTokens: usage.Output, CacheReadTokens: usage.CacheRead})
	})
	return spent, err
}

// codexUsage is a token count as a Codex rollout records it, whose input
// already holds its cache reads.
type codexUsage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
}

func (u codexUsage) usage() Usage {
	return Usage{InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.Cached}
}

// CodexSpend reads what the Codex rollout at path spent after from and by
// to. Each token count Codex records carries its thread's running total, so
// a request spent what the total moved since the count before it, on the
// model and effort of the turn it fell in, and a count that repeats the
// total spent nothing. A count with no total adds its own request's.
func CodexSpend(path string, from, to time.Time) ([]Spend, error) {
	var spent spending
	var model, effort string
	var total Usage
	err := eachLine(path, func(line []byte) {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Type   string `json:"type"`
				Model  string `json:"model"`
				Effort string `json:"effort"`
				Info   *struct {
					Total *codexUsage `json:"total_token_usage"`
					Last  *codexUsage `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		if entry.Type == "turn_context" {
			model, effort = entry.Payload.Model, entry.Payload.Effort
			return
		}
		info := entry.Payload.Info
		if entry.Type != "event_msg" || entry.Payload.Type != "token_count" || info == nil {
			return
		}
		var request Usage
		switch {
		case info.Total != nil:
			now := info.Total.usage()
			request = Usage{InputTokens: now.InputTokens - total.InputTokens, OutputTokens: now.OutputTokens - total.OutputTokens, CacheReadTokens: now.CacheReadTokens - total.CacheReadTokens}
			total = now
		case info.Last != nil:
			request = info.Last.usage()
		}
		if request != (Usage{}) && within(entry.Timestamp, from, to) {
			spent.add(model, effort, entry.Timestamp, request)
		}
	})
	return spent, err
}

// PiSpend reads what the pi session at path spent after from and by to: each
// answer's own usage, whose input leaves out its cache reads and writes, on
// the thinking level the session last set.
func PiSpend(path string, from, to time.Time) ([]Spend, error) {
	var spent spending
	var effort string
	err := eachLine(path, func(line []byte) {
		var entry struct {
			Type          string `json:"type"`
			Timestamp     string `json:"timestamp"`
			ThinkingLevel string `json:"thinkingLevel"`
			Message       struct {
				Role  string `json:"role"`
				Model string `json:"model"`
				Usage struct {
					Input      int64 `json:"input"`
					Output     int64 `json:"output"`
					CacheRead  int64 `json:"cacheRead"`
					CacheWrite int64 `json:"cacheWrite"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		if entry.Type == "thinking_level_change" {
			effort = entry.ThinkingLevel
			return
		}
		at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil || entry.Type != "message" || entry.Message.Role != "assistant" || entry.Message.Model == "" || !within(at, from, to) {
			return
		}
		usage := entry.Message.Usage
		spent.add(entry.Message.Model, effort, at, Usage{InputTokens: usage.Input + usage.CacheRead + usage.CacheWrite, OutputTokens: usage.Output, CacheReadTokens: usage.CacheRead})
	})
	return spent, err
}

// ClaudeWorkers reads what each sub-agent of the Claude Code conversation
// whose transcript is at path spent after from and by to. Claude Code keeps
// a sub-agent's transcript and the record of its type beside its parent's,
// under the conversation's own folder.
func ClaudeWorkers(path string, from, to time.Time) ([]Worker, error) {
	transcripts, err := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-*.jsonl"))
	if err != nil {
		return nil, err
	}
	var workers []Worker
	for _, transcript := range transcripts {
		spends, err := ClaudeSpend(transcript, from, to)
		if err != nil {
			return nil, err
		}
		if len(spends) == 0 {
			continue
		}
		var meta struct {
			AgentType string `json:"agentType"`
		}
		data, err := fsx.ReadFile(strings.TrimSuffix(transcript, ".jsonl") + ".meta.json")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal(data, &meta); err != nil {
				return nil, err
			}
		}
		workers = append(workers, Worker{Agent: meta.AgentType, Spends: spends})
	}
	return workers, nil
}

// codexSessions is the folder under the user's home where Codex files each
// rollout by the date it started on.
var codexSessions = filepath.Join(".codex", "sessions")

// CodexWorkers reads what each child agent of the Codex conversation whose
// rollout is at path spent after from and by to. Codex writes a child's own
// rollout, opening on the thread that spawned it and the role it spawned it
// in, filed in home's sessions by the day it started, so only the days of
// the window are read, a day either side for the time zone it was filed in.
func CodexWorkers(home, path string, from, to time.Time) ([]Worker, error) {
	lead, err := readCodexMeta(path)
	if err != nil {
		return nil, err
	}
	if lead.id == "" {
		return nil, errors.New("measure: " + path + " names no thread")
	}
	var workers []Worker
	for day := from.AddDate(0, 0, -1); !day.After(to.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		rollouts, err := filepath.Glob(filepath.Join(home, codexSessions, day.Format("2006"), day.Format("01"), day.Format("02"), "rollout-*.jsonl"))
		if err != nil {
			return nil, err
		}
		for _, rollout := range rollouts {
			// A rollout whose opening is not yet written whole is no thread's
			// child yet.
			child, err := readCodexMeta(rollout)
			if err != nil || child.parent != lead.id {
				continue
			}
			spends, err := CodexSpend(rollout, from, to)
			if err != nil {
				return nil, err
			}
			if len(spends) > 0 {
				workers = append(workers, Worker{Agent: child.role, Spends: spends})
			}
		}
	}
	return workers, nil
}

// codexMeta is what a rollout's opening session_meta says of its thread.
type codexMeta struct {
	id, parent, role string
}

// readCodexMeta reads the opening session_meta of the rollout at path. A
// child's names the thread that spawned it, in the entry itself or in the
// spawn its source records.
func readCodexMeta(path string) (codexMeta, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return codexMeta{}, err
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil {
		return codexMeta{}, err
	}
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string          `json:"id"`
			Parent string          `json:"parent_thread_id"`
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return codexMeta{}, err
	}
	if entry.Type != "session_meta" {
		return codexMeta{}, errors.New("measure: " + path + " opens on no session_meta")
	}
	// A source is a word, as "cli" for a goblin's own thread, and an object
	// only for a child's; one of a shape not known here names no spawn.
	var source struct {
		Subagent struct {
			ThreadSpawn struct {
				Parent string `json:"parent_thread_id"`
				Role   string `json:"agent_role"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if bytes.HasPrefix(entry.Payload.Source, []byte("{")) {
		_ = json.Unmarshal(entry.Payload.Source, &source)
	}
	spawn := source.Subagent.ThreadSpawn
	return codexMeta{id: entry.Payload.ID, parent: cmp.Or(entry.Payload.Parent, spawn.Parent), role: spawn.Role}, nil
}
