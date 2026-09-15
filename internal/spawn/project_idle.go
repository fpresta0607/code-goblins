package spawn

import (
	"context"
	"fmt"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Called under the home spawn lock before allocating another worktree.
// Historical done status does not make a still-attached worker inactive.
func (s Service) requireProjectIdle(ctx context.Context, project string) error {
	scan, err := state.ScanIDs(s.StateDir)
	if err != nil {
		return err
	}
	for _, id := range scan.MetaIDs {
		meta, err := state.ReadTaskMeta(s.StateDir, id)
		if err != nil {
			return err
		}
		if !fsx.SamePath(project, meta.Project) {
			continue
		}
		if meta.Backend != "herdr" {
			return fmt.Errorf("spawn: project task %s has unverifiable worker custody; inspect it before dispatch", id)
		}
		client := *s.Herdr
		client.Session = meta.HerdrSession
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		sample, err := monitor.NewHerdrProber(&client).Inspect(probeCtx, meta)
		cancel()
		if err != nil || sample.Verdict == monitor.ProbeUnknown {
			return fmt.Errorf("spawn: project task %s liveness unavailable; inspect it before dispatch", id)
		}
		if sample.Agent != herdr.AgentDead && sample.Verdict != monitor.ProbeMissing {
			return fmt.Errorf("spawn: project already has attached worker %s; finish or switch it before dispatch", id)
		}
	}
	return nil
}
