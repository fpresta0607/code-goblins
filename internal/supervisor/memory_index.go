package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/memory"
)

// memoryIndexLook is how often the supervisor looks for a changed memory
// source. An accepted fact reaches recall within this, and a rebuild costs
// only its read of the memory files.
const memoryIndexLook = 10 * time.Second

// ErrNotRunning is what a recall meets when this home has no supervisor
// holding its watch lock, so the caller builds an index of its own.
var ErrNotRunning = errors.New("the supervisor is not running")

// memoryIndex is the supervisor's one keyword index of the memory files. It
// is rebuilt from them at start and when a source changes, and never written
// back to them. A rebuild that fails is reported to every recall rather than
// answered from the index before it.
type memoryIndex struct {
	mu      sync.Mutex
	index   *memory.Index
	failure error
}

// refresh rebuilds the index when it has none or a source changed. Only
// keepMemoryIndex calls it, and a recall waits for no rebuild: it is
// answered from the index before it until the new one is swapped in.
func (keeper *memoryIndex) refresh(dataDir, stateDir string) {
	keeper.mu.Lock()
	current, failure := keeper.index, keeper.failure
	keeper.mu.Unlock()
	if current != nil && failure == nil {
		if changed, err := current.IsChanged(); err == nil && !changed {
			return
		}
	}
	index, err := memory.BuildIndex(dataDir, stateDir, time.Now())
	keeper.mu.Lock()
	defer keeper.mu.Unlock()
	if err != nil {
		keeper.index, keeper.failure = nil, fmt.Errorf("the memory index could not be rebuilt: %w", err)
		return
	}
	keeper.index, keeper.failure = index, nil
}

func (keeper *memoryIndex) recall(query memory.RecallQuery) (memory.RecallResult, error) {
	keeper.mu.Lock()
	index, failure := keeper.index, keeper.failure
	keeper.mu.Unlock()
	if failure != nil {
		return memory.RecallResult{}, failure
	}
	if index == nil {
		return memory.RecallResult{}, errors.New("the memory index is still being built; ask again in a moment")
	}
	result, err := index.Recall(query)
	return result.Shortened(), err
}

// keepMemoryIndex builds the index at start and keeps it current until ctx
// ends.
func (s *Service) keepMemoryIndex(ctx context.Context) {
	look := time.NewTicker(memoryIndexLook)
	defer look.Stop()
	for {
		s.memory.refresh(s.Store.Home.Data, s.Store.Home.State)
		select {
		case <-ctx.Done():
			return
		case <-look.C:
		}
	}
}

// Recall asks the running supervisor of stateDir's home a question of its
// memory index, or returns ErrNotRunning at once when none holds the home.
func Recall(stateDir string, query memory.RecallQuery) (memory.RecallResult, error) {
	holder, err := lock.ReadNamed(stateDir, ".watch.lock")
	if err != nil || !lock.HeldByNamed(stateDir, ".watch.lock", holder.PID) {
		return memory.RecallResult{}, ErrNotRunning
	}
	reply, err := askPipe(stateDir, runPipeRequest{Kind: "recall", Recall: &query})
	if err != nil {
		return memory.RecallResult{}, err
	}
	if reply.Recall == nil {
		return memory.RecallResult{}, errors.New("the supervisor answered the recall without its result")
	}
	return *reply.Recall, nil
}
