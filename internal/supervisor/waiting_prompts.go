package supervisor

import (
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// namedWaiting names, on each of his prompts a session's harness holds, the
// board message that typed it: the oldest message to that session the board
// has started typing and not settled, of the same words, taken before the
// harness recorded the prompt and named by no earlier prompt. The board
// commits a message as running before it types it, so a prompt the harness
// recorded from a board message finds it here, whatever order the board's
// snapshot and the session's messages reach a view in. A prompt typed in the
// terminal, or one recorded before the board took the message, names none,
// so a separate message of the same words keeps its own row. A shell command
// still waiting is named by the message that typed it, its words after its !.
// task is the
// goblin's task, or empty for the CFO, whose messages name no task.
func (s *Store) namedWaiting(task string, waiting []fleettree.WaitingPrompt) []fleettree.WaitingPrompt {
	if len(waiting) == 0 {
		return waiting
	}
	named := slices.Clone(waiting)
	s.readers.RLock()
	defer s.readers.RUnlock()
	var typing []Action
	for _, action := range s.committed.Actions {
		if action.Kind == "message" && action.TaskID == task && action.Status == "running" {
			typing = append(typing, action)
		}
	}
	slices.SortStableFunc(typing, func(one, other Action) int { return one.CreatedAt.Compare(other.CreatedAt) })
	for i, prompt := range named {
		if prompt.Source != fleettree.FromOverlord && prompt.Source != fleettree.FromShell {
			continue
		}
		at := slices.IndexFunc(typing, func(action Action) bool {
			typed := action.Text
			if prompt.Source == fleettree.FromShell {
				command, isShell := strings.CutPrefix(strings.TrimSpace(action.Text), "!")
				if !isShell {
					return false
				}
				typed = command
			}
			return !action.CreatedAt.After(prompt.At) && sameWords(typed, prompt.Text)
		})
		if at >= 0 {
			named[i].Action = typing[at].ID
			typing = slices.Delete(typing, at, at+1)
		}
	}
	return named
}

// sameWords says two texts hold the same words, however a harness wrapped or
// trimmed them.
func sameWords(one, other string) bool {
	return strings.Join(strings.Fields(one), " ") == strings.Join(strings.Fields(other), " ")
}
