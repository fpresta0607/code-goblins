package reap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
)

type NativeValidationReader interface {
	InspectNative(ctx context.Context, task Task) (NativeValidator, bool, error)
}

type PipelineValidators struct {
	Home   home.Home
	Reader pipeline.Reader
	Gate   monitor.GateProber
}

func (p PipelineValidators) InspectNative(ctx context.Context, task Task) (NativeValidator, bool, error) {
	if task.Meta.Mode != "no-mistakes" {
		return NativeValidator{}, false, nil
	}
	if p.Gate == nil {
		return NativeValidator{}, false, errors.New("native gate reader is unavailable")
	}
	launchPath := filepath.Join(filepath.Dir(taskcontext.PathsFor(p.Home, task.ID).Manifest), "pipeline-launch.json")
	launch, err := pipeline.LoadLaunch(launchPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NativeValidator{}, false, errors.New("native launch association is unavailable")
		}
		return NativeValidator{}, false, err
	}
	run, err := p.Reader.BoundRun(ctx, launch)
	if err != nil {
		return NativeValidator{}, false, err
	}
	sample, err := p.Gate.InspectGate(ctx, task.Meta)
	if err != nil {
		return NativeValidator{}, false, err
	}
	if sample.RunID != run.RunID {
		return NativeValidator{}, false, errors.New("native status belongs to a different run")
	}
	if !sample.Active {
		return NativeValidator{}, false, nil
	}
	if sample.ActivePID <= 0 || sample.Step == "" || sample.ActiveFor <= 0 || sample.ObservedAt.IsZero() {
		return NativeValidator{}, false, errors.New("active native validation lacks process identity")
	}
	return NativeValidator{
		TaskID:        task.ID,
		RunID:         run.RunID,
		Step:          sample.Step,
		RootPID:       sample.ActivePID,
		StepStartedAt: sample.ObservedAt.Add(-sample.ActiveFor),
		ObservedAt:    sample.ObservedAt,
	}, true, nil
}

func verifyNativeValidator(validator NativeValidator, processes []Process) (NativeValidator, error) {
	return verifyNativeValidatorAt(validator, processes, time.Now().UTC())
}

const nativeObservationMaxAge = 2 * time.Minute

// verifyNativeValidatorAt only accepts a process generation that was already
// present when the native step began and a status observation that is still
// fresh. A PID reused after the step started, or a status read from a prior
// observation, is held because its identity cannot be established safely.
func verifyNativeValidatorAt(validator NativeValidator, processes []Process, now time.Time) (NativeValidator, error) {
	if validator.ObservedAt.IsZero() || now.Before(validator.ObservedAt) || now.Sub(validator.ObservedAt) > nativeObservationMaxAge {
		return NativeValidator{}, fmt.Errorf("native validator status observation is stale")
	}
	if validator.StepStartedAt.IsZero() || validator.StepStartedAt.After(validator.ObservedAt) {
		return NativeValidator{}, fmt.Errorf("native validator active step timing is invalid")
	}
	var root *Process
	for i := range processes {
		if processes[i].PID != validator.RootPID {
			continue
		}
		if root != nil {
			return NativeValidator{}, fmt.Errorf("native validator pid %d is ambiguous", validator.RootPID)
		}
		root = &processes[i]
	}
	if root == nil || root.Start.IsZero() {
		return NativeValidator{}, fmt.Errorf("native validator pid %d has no fresh creation evidence", validator.RootPID)
	}
	if root.Start.Before(validator.StepStartedAt.Add(-30*time.Second)) || root.Start.After(validator.StepStartedAt) {
		return NativeValidator{}, fmt.Errorf("native validator pid %d creation time does not match the active step", validator.RootPID)
	}
	validator.RootStart = root.Start
	return validator, nil
}
