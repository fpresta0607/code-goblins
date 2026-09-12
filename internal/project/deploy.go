package project

import (
	"context"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"time"
)

type DeployState string

const (
	NotRequired DeployState = "not-required"
	Pending     DeployState = "pending"
	Deploying   DeployState = "deploying"
	Deployed    DeployState = "deployed"
	Verifying   DeployState = "verifying"
	Healthy     DeployState = "healthy"
	Unhealthy   DeployState = "unhealthy"
	Blocked     DeployState = "blocked"
)

type DeployResult struct {
	Name, Provider         string
	State                  DeployState
	DeployExit, VerifyExit int
	DurationSeconds        float64
	Error                  string
}

func RunDeployment(ctx context.Context, r execx.Runner, d Deployment, dir, target string) ([]DeployResult, error) {
	if !d.Required {
		return []DeployResult{{State: NotRequired}}, nil
	}
	var out []DeployResult
	for _, t := range d.Targets {
		if target != "" && t.Name != target {
			continue
		}
		rr := DeployResult{Name: t.Name, Provider: t.Provider, State: Deploying}
		start := time.Now()
		res, e := r.Run(ctx, execx.Request{Name: t.Command[0], Args: t.Command[1:], Dir: dir})
		rr.DurationSeconds = time.Since(start).Seconds()
		if e != nil {
			rr.State = Blocked
			rr.Error = e.Error()
			out = append(out, rr)
			return out, e
		}
		rr.DeployExit = res.ExitCode
		if res.ExitCode != 0 {
			rr.State = Blocked
			rr.Error = fmt.Sprintf("deploy exited %d", res.ExitCode)
			out = append(out, rr)
			return out, fmt.Errorf("deploy %s: %s", t.Name, rr.Error)
		}
		rr.State = Deployed
		if len(t.Verify) > 0 {
			rr.State = Verifying
			v, e := r.Run(ctx, execx.Request{Name: t.Verify[0], Args: t.Verify[1:], Dir: dir})
			if e != nil {
				rr.State = Unhealthy
				rr.Error = e.Error()
				out = append(out, rr)
				return out, e
			}
			rr.VerifyExit = v.ExitCode
			if v.ExitCode != 0 {
				rr.State = Unhealthy
				rr.Error = fmt.Sprintf("health exited %d", v.ExitCode)
				out = append(out, rr)
				return out, fmt.Errorf("deploy %s: %s", t.Name, rr.Error)
			}
		}
		rr.State = Healthy
		out = append(out, rr)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("deployment target %q not found", target)
	}
	return out, nil
}
