package supervisor

import (
	"os"
	"path/filepath"
	"time"
)

// launchWatchEvery is how often a start or resume under way looks for its
// goblin's task record and terminal host.
const launchWatchEvery = 250 * time.Millisecond

// watchLaunch publishes a change to task id's record or its terminal host
// while its start or resume runs, until done closes. cfo writes both while it
// runs, and nothing else tells the board: it saw a started goblin's session,
// and opened its terminal, only at the next change of anything else or at
// the 15 second refresh, 4 to 6 seconds after its terminal ran on
// 2026-10-08.
func (s *Service) watchLaunch(id string, done <-chan struct{}) {
	seen := launchMark(s.Store.Home.State, id)
	ticker := time.NewTicker(launchWatchEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if mark := launchMark(s.Store.Home.State, id); mark != seen {
				seen = mark
				s.notify()
			}
		}
	}
}

// launchMark is when task id's record and its terminal host's record were
// last written, zero for one that is absent.
func launchMark(stateDir, id string) [2]time.Time {
	var mark [2]time.Time
	for index, path := range []string{filepath.Join(stateDir, id+".meta"), filepath.Join(stateDir, "hosts", id+".json")} {
		if info, err := os.Stat(path); err == nil {
			mark[index] = info.ModTime()
		}
	}
	return mark
}
