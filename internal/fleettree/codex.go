package fleettree

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// rolloutMeta is what a Codex rollout's opening session_meta says of it,
// which it never rewrites. Codex 0.160 writes each child agent's own rollout
// with thread_source "subagent" and the thread that spawned it.
type rolloutMeta struct {
	// read is false while the opening entry cannot be read whole.
	read     bool
	id       string
	cwd      string
	parent   string
	subagent bool
	path     string
	nickname string
	role     string
	started  time.Time
}

// rolloutPattern is every Codex rollout under the user's home.
var rolloutPattern = filepath.Join(".codex", "sessions", "*", "*", "*", "rollout-*.jsonl")

// readRolloutMeta reads a rollout's opening session_meta.
func readRolloutMeta(path string) rolloutMeta {
	file, err := fsx.Open(path)
	if err != nil {
		return rolloutMeta{}
	}
	defer file.Close()
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			ID           string    `json:"id"`
			Cwd          string    `json:"cwd"`
			Timestamp    time.Time `json:"timestamp"`
			ParentThread string    `json:"parent_thread_id"`
			ThreadSource string    `json:"thread_source"`
			AgentPath    string    `json:"agent_path"`
			Nickname     string    `json:"agent_nickname"`
			Source       struct {
				Subagent struct {
					ThreadSpawn struct {
						ParentThread string `json:"parent_thread_id"`
						Role         string `json:"agent_role"`
					} `json:"thread_spawn"`
				} `json:"subagent"`
			} `json:"source"`
		} `json:"payload"`
	}
	if json.NewDecoder(io.LimitReader(file, TranscriptEntryReach)).Decode(&entry) != nil {
		return rolloutMeta{}
	}
	if entry.Type != "session_meta" {
		return rolloutMeta{read: true}
	}
	payload := entry.Payload
	parent := payload.ParentThread
	if parent == "" {
		parent = payload.Source.Subagent.ThreadSpawn.ParentThread
	}
	return rolloutMeta{
		read: true, id: payload.ID, cwd: payload.Cwd, parent: parent,
		subagent: payload.ThreadSource == "subagent" || parent != "",
		path:     payload.AgentPath, nickname: payload.Nickname, role: payload.Source.Subagent.ThreadSpawn.Role,
		started: payload.Timestamp.UTC(),
	}
}

// rollouts reads every rollout's opening entry, each once while it exists.
// The caller holds the reader's lock.
func (r *Reader) rollouts(ctx context.Context) map[string]rolloutMeta {
	matches, _ := filepath.Glob(filepath.Join(r.Home, rolloutPattern))
	kept := make(map[string]rolloutMeta, len(matches))
	for _, match := range matches {
		if ctx.Err() != nil {
			break
		}
		meta, ok := r.rolloutMetas[match]
		if !ok || !meta.read {
			meta = readRolloutMeta(match)
		}
		kept[match] = meta
	}
	r.rolloutMetas = kept
	return kept
}

// codexConversation is the goblin's own rollout: the one session names, or,
// for a native goblin whose terminal names none, the rollout bound to its
// worktree whose entries were written last, never a child agent's.
func (r *Reader) codexConversation(ctx context.Context, worktree, session string, metas map[string]rolloutMeta) string {
	if session != "" {
		return SessionTranscript(r.Home, "codex", session)
	}
	if !filepath.IsAbs(worktree) {
		return ""
	}
	worktree, err := fsx.Canonical(worktree)
	if err != nil {
		return ""
	}
	owned := map[string]bool{}
	newest := ""
	var latest time.Time
	for path, meta := range metas {
		if ctx.Err() != nil {
			break
		}
		if !meta.read || meta.subagent || meta.cwd == "" {
			continue
		}
		isOwned, seen := owned[meta.cwd]
		if !seen {
			resolved, err := fsx.Canonical(meta.cwd)
			isOwned = filepath.IsAbs(meta.cwd) && err == nil && strings.EqualFold(resolved, worktree)
			owned[meta.cwd] = isOwned
		}
		if !isOwned {
			continue
		}
		if written := WrittenAt(path); written.After(latest) {
			newest, latest = path, written
		}
	}
	return newest
}

// codexChildrenDepth bounds how far down a child agent's own children are
// followed.
const codexChildrenDepth = 3

// codexChildren are the child agents a Codex thread spawned, and theirs, each
// read from its own rollout.
func codexChildren(metas map[string]rolloutMeta, root string) []Node {
	var children []Node
	parents := map[string]string{root: ""}
	for depth := 0; depth < codexChildrenDepth; depth++ {
		next := map[string]string{}
		for path, meta := range metas {
			parentNode, isChild := parents[meta.parent]
			if !meta.read || meta.parent == "" || !isChild || meta.id == "" {
				continue
			}
			node := codexChild(path, meta)
			node.Parent = parentNode
			children = append(children, node)
			next[meta.id] = node.ID
		}
		if len(next) == 0 {
			break
		}
		parents = next
	}
	return children
}

// codexChild reads one child agent's rollout: it works from a task_started
// until its task_complete, which carries its last message, and a turn_aborted
// ends it unfinished.
func codexChild(path string, meta rolloutMeta) Node {
	written, entries := tail(path)
	name := meta.path[strings.LastIndex(meta.path, "/")+1:]
	detail := strings.TrimSpace(meta.nickname + " " + meta.role)
	if detail == "" {
		detail = "child agent"
	}
	node := Node{ID: "subagent:" + meta.id, Kind: KindSubagent, Label: label(strings.ReplaceAll(name, "_", " "), "Child agent"), Detail: detail, State: Working, Started: meta.started, LastActivity: written, SourceUpdatedAt: written}
	for i := len(entries) - 1; i >= 0; i-- {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Type      string `json:"type"`
				LastAgent string `json:"last_agent_message"`
				Reason    string `json:"reason"`
			} `json:"payload"`
		}
		if json.Unmarshal(entries[i], &entry) != nil || entry.Type != "event_msg" {
			continue
		}
		switch entry.Payload.Type {
		case "task_started":
			return node
		case "task_complete":
			node.State, node.Finished = Done, entry.Timestamp.UTC()
			node.LastLine = bounded(firstLine(entry.Payload.LastAgent), 200)
			return node
		case "turn_aborted":
			node.State, node.Finished = Failed, entry.Timestamp.UTC()
			node.LastLine = "turn aborted: " + entry.Payload.Reason
			return node
		}
	}
	return node
}

// CodexRollout is a Codex goblin's own rollout: the one session names, or,
// for a native goblin whose terminal names none, the rollout bound to its
// worktree whose entries were written last, never a child agent's.
func (r *Reader) CodexRollout(ctx context.Context, worktree, session string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.codexConversation(ctx, worktree, session, r.rollouts(ctx))
}
