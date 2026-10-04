// Package fleet reads and orders the local Code Goblin fleet and shapes what
// is sent to its goblins.
package fleet

import (
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// NativeTask is the record of the native task target names, by its id or as
// gb-<id>, and whether that record is a native task's.
func NativeTask(stateDir, target string) (state.TaskMeta, bool) {
	for _, id := range []string{target, strings.TrimPrefix(target, "gb-")} {
		if meta, err := state.ReadTaskMeta(stateDir, id); err == nil {
			return meta, meta.Backend == "native"
		}
	}
	return state.TaskMeta{}, false
}
