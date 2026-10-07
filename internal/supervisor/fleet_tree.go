package supervisor

import (
	"context"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// treeEvery is how often the board reads every live goblin's tree, the
// board's own refresh: a child's state is never older on the board than one
// refresh and one read.
const treeEvery = snapshotRefresh

// treePassBudget bounds one read of the whole fleet, so a harness record
// that never answers cannot hold the next.
const treePassBudget = 30 * time.Second

// keepTrees reads every live goblin's tree on its own loop, away from the
// supervisor's, since a first read parses a goblin's whole conversation.
func (s *Service) keepTrees(ctx context.Context) {
	ticker := time.NewTicker(treeEvery)
	defer ticker.Stop()
	for {
		s.readTrees(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// readTrees reads the tree of every live goblin whose terminal runs: a
// paused or stopped goblin, or one whose terminal has ended, runs nothing.
func (s *Service) readTrees(ctx context.Context) {
	pass, cancel := context.WithTimeout(ctx, treePassBudget)
	defer cancel()
	trees := map[string]fleettree.Tree{}
	live := map[string]bool{}
	for _, meta := range liveTasks(s.Store.Home.State) {
		live[meta.ID] = true
		if pass.Err() != nil {
			break
		}
		if record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
			continue
		}
		goblin := fleettree.Goblin{Meta: meta}
		if meta.Backend == "native" {
			record, err := host.ReadRecord(s.Store.Home.State, meta.ID)
			if err != nil {
				continue
			}
			goblin.HarnessPID, goblin.HarnessStarted = record.ChildPID, record.ChildStart
		}
		tree, _ := s.Options.Tree.Read(pass, goblin)
		trees[meta.ID] = tree
	}
	s.Options.Tree.Forget(live)
	s.mu.Lock()
	s.trees = trees
	s.mu.Unlock()
}

// recordedSession is the conversation the board recorded for the task's
// goblin of its current generation, read from the store in memory.
func (s *Store) recordedSession(meta state.TaskMeta) string {
	s.readers.RLock()
	defer s.readers.RUnlock()
	session := s.committed.Sessions[s.committed.TaskSessions[meta.ID]]
	return fleettree.Owned(meta, fleettree.RecordedSession{NativeID: session.NativeID, Harness: session.Harness, Role: session.Role, TaskID: session.TaskID, Generation: session.Generation})
}
