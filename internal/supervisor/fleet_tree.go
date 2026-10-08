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
	metas := liveTasks(s.Store.Home.State)
	for _, meta := range metas {
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
	// A helper hangs under its parent as a child of the parent's tree; a
	// parent whose tree was not read, such as a paused one, holds none.
	now := time.Now().UTC()
	for _, meta := range metas {
		if parent, isRead := trees[meta.Parent]; meta.Parent != "" && isRead {
			parent.Children = append(parent.Children, fleettree.HelperNode(meta, trees[meta.ID], s.helperStanding(meta), now))
			trees[meta.Parent] = parent
		}
	}
	s.mu.Lock()
	s.trees = trees
	s.mu.Unlock()
}

// helperStanding is where helper meta stands by its own records: its latest
// report since it started and whether it is paused.
func (s *Service) helperStanding(meta state.TaskMeta) fleettree.HelperStanding {
	var standing fleettree.HelperStanding
	stateDir := s.Store.Home.State
	if record, err := state.ReadLifecycle(stateDir, meta.ID); err == nil && record.Generation == meta.SpawnGen && (record.Phase == "paused" || record.Phase == "pausing") {
		standing.Paused = true
	}
	lines, err := state.TailStatus(stateDir, meta.ID, 200)
	if err != nil {
		return standing
	}
	born := spawnTime(meta.SpawnGen)
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if !born.IsZero() && stamp.Before(born.Truncate(time.Second)) {
			break
		}
		if kind := reportKind(event); kind != "" {
			standing.Report, standing.ReportedAt = kind, stamp
			break
		}
	}
	return standing
}

// recordedSession is the conversation the board recorded for the task's
// goblin of its current generation, read from the store in memory.
func (s *Store) recordedSession(meta state.TaskMeta) string {
	s.readers.RLock()
	defer s.readers.RUnlock()
	session := s.committed.Sessions[s.committed.TaskSessions[meta.ID]]
	return fleettree.Owned(meta, fleettree.RecordedSession{NativeID: session.NativeID, Harness: session.Harness, Role: session.Role, TaskID: session.TaskID, Generation: session.Generation})
}
