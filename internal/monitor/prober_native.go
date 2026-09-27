package monitor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// NativeProber reads a native task's terminal: its host's record says whether
// the terminal still runs, and the console's own screen, read the way a
// native spawn reads it, says what the harness shows. The host ends with the
// harness it runs, so a running terminal is a live harness.
type NativeProber struct {
	StateDir string
	// ReadScreen reads a host's console; nil reads it through the host.
	ReadScreen func(host.Record) ([]string, error)
}

// Inspect samples the task's native terminal. The sample names the terminal
// as its tab, gb-<id>, the way a Herdr task's tab is named.
func (p NativeProber) Inspect(_ context.Context, meta state.TaskMeta) (EndpointSample, error) {
	unknown := func(detail string) EndpointSample {
		return EndpointSample{Verdict: ProbeUnknown, Detail: detail}
	}
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return unknown(fmt.Sprintf("native task %s runs %s, whose screen the monitor cannot read", meta.ID, meta.Harness)), nil
	}
	// A native terminal ends with its harness, and a reboot or sign-out
	// ends every one; switch restarts the harness in place with its own
	// resume.
	resume := fmt.Sprintf("cfo switch %s restarts its harness in place, resuming its session", meta.ID)
	record, err := host.ReadRecord(p.StateDir, meta.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return EndpointSample{Verdict: ProbeMissing, Detail: fmt.Sprintf("native terminal %s has no running host; %s", meta.ID, resume)}, nil
	}
	if err != nil {
		return unknown(fmt.Sprintf("native terminal %s's host record is unreadable: %v", meta.ID, err)), nil
	}
	read := p.ReadScreen
	if read == nil {
		read = host.ReadScreen
	}
	screen, err := read(record)
	if err != nil {
		// A host that was killed leaves its record behind, so only an answer
		// on its pipe says the terminal still runs.
		client, dialErr := host.Dial(record)
		if dialErr != nil {
			return EndpointSample{Verdict: ProbeMissing, Detail: fmt.Sprintf("native terminal %s's host does not answer (%v); %s", meta.ID, dialErr, resume)}, nil
		}
		_ = client.Close()
		return unknown(fmt.Sprintf("native terminal %s's screen is unreadable: %v", meta.ID, err)), nil
	}
	sample := EndpointSample{
		Verdict:             ProbePresent,
		TabLabel:            "gb-" + meta.ID,
		Agent:               herdr.AgentAlive,
		Capture:             []byte(strings.Join(screen, "\n")),
		Harness:             meta.Harness,
		CountersUnavailable: true,
	}
	_, dialog := screens.Dialog(screen)
	switch {
	case screens.IsWorking(screen):
		sample.Status, sample.Busy = herdr.AgentWorking, herdr.BusyWorking
	case dialog:
		sample.Status, sample.Busy = herdr.AgentBlocked, herdr.BusyIdle
	case screens.IsReady(screen):
		sample.Status, sample.Busy, sample.InteractiveReady = herdr.AgentDone, herdr.BusyIdle, true
	default:
		sample.Status, sample.Busy = herdr.AgentUnknown, herdr.BusyUnknown
	}
	return sample, nil
}

// BackendProber inspects each task with the prober of the terminal backend
// it runs in.
type BackendProber struct {
	Herdr  Prober
	Native Prober
}

// BeginScan starts the Herdr prober's cycle, which reads one snapshot of the
// whole session for every Herdr task.
func (p BackendProber) BeginScan(ctx context.Context) {
	if cycle, ok := p.Herdr.(CycleProber); ok {
		cycle.BeginScan(ctx)
	}
}

// Inspect samples meta's terminal through its backend's prober.
func (p BackendProber) Inspect(ctx context.Context, meta state.TaskMeta) (EndpointSample, error) {
	if meta.Backend == "native" {
		return p.Native.Inspect(ctx, meta)
	}
	return p.Herdr.Inspect(ctx, meta)
}
