package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// pollAwaitedRuns raises ci_finished for each workflow run a paused goblin
// waits on once it completes on the head the goblin waits for. A run whose
// read fails is read again on the next poll, and the failure is returned
// only once the read failed failingPasses polls in a row.
func (s *Service) pollAwaitedRuns(ctx context.Context, watched *fleetWakes, now time.Time) error {
	var problems error
	seen := map[string]bool{}
	// failed holds each run read this poll and whether its read failed, so a
	// run two goblins wait on counts one failed poll, not two.
	failed := map[string]bool{}
	defer func() {
		maps.DeleteFunc(watched.Failing, func(read string, _ int) bool {
			target, isAwaited := strings.CutPrefix(read, "awaited:")
			_, isRead := failed[target]
			return isAwaited && !isRead
		})
	}()
	for _, meta := range liveTasks(s.Store.Home.State) {
		record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if record.Phase != "paused" || record.Generation != meta.SpawnGen || record.Pause == nil || record.Pause.Reason != "ci" && record.Pause.Reason != "deploy" {
			continue
		}
		wait, head, _ := strings.Cut(record.Pause.Until, "@")
		target, isRun := strings.CutPrefix(wait, "run:")
		if !isRun || seen[target] {
			continue
		}
		parsed, err := url.Parse(target)
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if failed[target] {
			continue
		}
		output, err := runOutput(ctx, s.Options.CI, meta.Project, "gh", "run", "view", parts[4], "--repo", parts[0]+"/"+parts[1], "--json", "databaseId,workflowName,status,conclusion,headSha,url,attempt,startedAt,updatedAt")
		failed[target] = err != nil
		if err != nil {
			problems = errors.Join(problems, watched.failing("awaited:"+target, fmt.Errorf("awaited run %s: %w", target, err)))
			continue
		}
		delete(watched.Failing, "awaited:"+target)
		var run ghRun
		if err := json.Unmarshal([]byte(output), &run); err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if run.Status != "completed" || run.Conclusion == "" || run.URL != target || run.HeadSHA != head {
			continue
		}
		seen[target] = true
		signature := fmt.Sprintf("%s|%s|%d|%s", run.HeadSHA, run.Conclusion, run.Attempt, run.UpdatedAt.Format(time.RFC3339))
		if completed := watched.Checks[target]; completed.Signature == signature {
			completed.ReportedAt = now
			watched.Checks[target] = completed
			continue
		}
		detail := fmt.Sprintf("ci_finished: %s's awaited %s run %d completed with %s at %s (%s); next: resume %s in place", meta.ID, record.Pause.Reason, run.ID, run.Conclusion, run.HeadSHA, target, meta.ID)
		if err := raiseFleetWake(s.Store.Home.State, "ci", meta.ID, detail); err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if watched.Checks == nil {
			watched.Checks = map[string]reportedChecks{}
		}
		watched.Checks[target] = reportedChecks{Signature: signature, At: now, ReportedAt: now}
		recordCIDuration(watched, target, record.Pause.Reason, run.Workflow, run.StartedAt, run.UpdatedAt)
	}
	return problems
}
