package supervisor

import "sync"

// sharedSnapshots is the board's snapshot built once for everyone who asks
// for one while it is being built. A snapshot reads the fleet from several
// hundred files, and every open board, the desktop app and the ticket keeper
// each built their own for every change: on 2026-10-02 one build took 3 to 13
// seconds on the live board.
type sharedSnapshots struct {
	mu sync.Mutex
	// running is the build in progress, waiting the one that starts when it
	// ends, for readers it began too early for, and last the newest that
	// finished without an error.
	running, waiting, last *snapshotBuild
}

// snapshotBuild is one build. began is the service's revision when it began,
// so it shows every change up to that revision.
type snapshotBuild struct {
	began    uint64
	done     chan struct{}
	snapshot Snapshot
	err      error
}

// Revision is the number of changes the service has published.
func (s *Service) Revision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// SnapshotSince returns a snapshot that shows the fleet as of revision need
// or later: the newest one built, if it began no earlier than that; else the
// one being built, if it did; else the next build, which everyone who asks
// meanwhile shares. So one change costs one build however many readers there
// are. The snapshot is shared: a reader replaces its fields and never writes
// into what they hold.
func (s *Service) SnapshotSince(need uint64) (Snapshot, error) {
	shared := &s.snapshots
	shared.mu.Lock()
	var build *snapshotBuild
	switch {
	case shared.last != nil && shared.last.began >= need:
		build = shared.last
	case shared.running != nil && shared.running.began >= need:
		build = shared.running
	case shared.running != nil:
		if shared.waiting == nil {
			shared.waiting = &snapshotBuild{done: make(chan struct{})}
		}
		build = shared.waiting
	default:
		build = &snapshotBuild{done: make(chan struct{})}
		s.beginSnapshot(build)
	}
	shared.mu.Unlock()
	<-build.done
	return build.snapshot, build.err
}

// beginSnapshot starts build, and the one waiting behind it when it ends.
// The caller holds the shared snapshots' lock.
func (s *Service) beginSnapshot(build *snapshotBuild) {
	shared := &s.snapshots
	build.began = s.Revision()
	shared.running = build
	go func() {
		if s.buildSnapshot != nil {
			build.snapshot, build.err = s.buildSnapshot()
		} else {
			build.snapshot, build.err = s.buildBoardSnapshot()
		}
		shared.mu.Lock()
		shared.running = nil
		if build.err == nil {
			shared.last = build
		}
		if next := shared.waiting; next != nil {
			shared.waiting = nil
			s.beginSnapshot(next)
		}
		shared.mu.Unlock()
		close(build.done)
	}()
}
