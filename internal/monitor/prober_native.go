package monitor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// screenReads is how many times a native screen is read before a failed read
// counts, screenReread apart.
const (
	screenReads  = 3
	screenReread = 250 * time.Millisecond
)

// NativeProber reads a native task's terminal: its host's record says whether
// the terminal still runs, and the console's own screen, read the way a
// native spawn reads it, says what the harness shows. The host ends with the
// harness it runs, so a running terminal is a live harness.
type NativeProber struct {
	StateDir string
	// ReadScreen reads a host's console; nil reads it through the host.
	ReadScreen func(host.Record) ([]string, error)
	// Dial checks that a host answers on its pipe; nil dials it.
	Dial func(host.Record) error
}

// Inspect samples the task's native terminal. The sample names the terminal
// as gb-<id>. A task an older build recorded in Herdr has no terminal this
// build can read, so it is unknown, with how to retire it.
func (p NativeProber) Inspect(_ context.Context, meta state.TaskMeta) (EndpointSample, error) {
	unknown := func(detail string) EndpointSample {
		return EndpointSample{Verdict: ProbeUnknown, Detail: detail}
	}
	if meta.Backend != "native" {
		return unknown(fmt.Sprintf("task %s was recorded in Herdr by an older build, whose pane this build cannot read; once that pane is closed, retire the record with cfo cleanup %s --force-archive", meta.ID, meta.ID)), nil
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
	// A read attaches a process of its own to the terminal's console, and
	// one can fail while the terminal is alive and working, so a failed read
	// is read again before anything is concluded from it.
	screen, err := read(record)
	for attempt := 1; err != nil && attempt < screenReads; attempt++ {
		time.Sleep(screenReread)
		screen, err = read(record)
	}
	if err != nil {
		// A host that was killed leaves its record behind, so only an answer
		// on its pipe says the terminal still runs.
		dial := p.Dial
		if dial == nil {
			dial = dialHost
		}
		if dialErr := dial(record); dialErr != nil {
			return EndpointSample{Verdict: ProbeMissing, Detail: fmt.Sprintf("native terminal %s's host does not answer (%v); %s", meta.ID, dialErr, resume)}, nil
		}
		sample := unknown(fmt.Sprintf("native terminal %s's screen is unreadable: %v", meta.ID, err))
		sample.ReadFailed = true
		return sample, nil
	}
	sample := EndpointSample{
		Verdict:             ProbePresent,
		TabLabel:            "gb-" + meta.ID,
		Capture:             []byte(strings.Join(screen, "\n")),
		Harness:             meta.Harness,
		CountersUnavailable: true,
	}
	_, dialog := screens.Dialog(screen)
	switch {
	case screens.IsWorking(screen):
		sample.Status, sample.Busy = StatusWorking, crewstate.BusyWorking
	case dialog:
		sample.Status, sample.Busy = StatusBlocked, crewstate.BusyIdle
	case screens.IsReady(screen):
		sample.Status, sample.Busy, sample.InteractiveReady = StatusDone, crewstate.BusyIdle, true
	default:
		sample.Status, sample.Busy = StatusUnknown, crewstate.BusyUnknown
	}
	return sample, nil
}

func dialHost(record host.Record) error {
	client, err := host.Dial(record)
	if err != nil {
		return err
	}
	return client.Close()
}
