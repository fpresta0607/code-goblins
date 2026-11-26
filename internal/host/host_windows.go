package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/conpty"
	"github.com/fpresta0607/code-goblins/internal/priority"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/processor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Run hosts spec's terminal until it ends or a viewer closes it: it records
// itself under stateDir, serves viewers over its pipe, and removes its record
// on the way out. The terminal's process and everything it starts share one
// job, which ends with this process however it ends.
func Run(stateDir string, spec Spec) error {
	// A host answers handshakes and relays keys and output for the terminal
	// it runs, and at the priority of the builds in that terminal it waits
	// its turn behind them. It runs one class above them instead, and
	// Windows starts the terminal's program at normal.
	defer priority.AboveTheWork()()
	if err := state.ValidTaskID(spec.ID); err != nil {
		return err
	}
	// One live host per terminal: a second would take over the record that
	// finds the first, and leave the first unreachable.
	if running, err := ReadRecord(stateDir, spec.ID); err == nil && isServing(running) {
		return fmt.Errorf("host: terminal %s already runs in host pid %d", spec.ID, running.HostPID)
	}
	var proof [32]byte
	if _, err := rand.Read(proof[:]); err != nil {
		return err
	}
	var processors uintptr
	if spec.ShouldLeaveCoresForApps {
		var err error
		// Cores that cannot be read hold no terminal back: it runs on every
		// one, as the start rule starts on processors it cannot read.
		if processors, err = processor.MachineFleetThreads(); err != nil {
			fmt.Fprintf(os.Stderr, "%s host: the terminal runs on every processor core, since this machine's cannot be read: %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
		}
	}
	if spec.IsStream {
		return runStream(stateDir, spec, hex.EncodeToString(proof[:]), processors)
	}
	output, err := newHistory(spec.Cols, spec.Rows)
	if err != nil {
		return err
	}
	console, err := conpty.Start(conpty.Spec{Args: spec.Args, Dir: spec.Dir, Env: terminalEnvironment(spec.ID, hex.EncodeToString(proof[:])), Cols: spec.Cols, Rows: spec.Rows, Unread: output.unread, Processors: processors})
	if err != nil {
		return err
	}
	if problem := conpty.ConsoleHostProblem(); problem != nil {
		fmt.Fprintf(os.Stderr, "%s host: %v\n", time.Now().UTC().Format(time.RFC3339Nano), problem)
		if err := reportConsoleHostProblem(stateDir, problem); err != nil {
			fmt.Fprintf(os.Stderr, "%s host: tell the CEO the terminal runs on the system conhost: %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
		}
	}
	record, pipe, err := announce(stateDir, spec.ID, console.PID(), hex.EncodeToString(proof[:]), false)
	if err != nil {
		_ = console.Close()
		return err
	}
	defer removeRecord(stateDir, spec.ID, record.HostPID)
	return runTerminal(stateDir, spec, record, console, output, pipe.accept)
}

// terminalEnvironment is this host's environment with IDVariable naming
// terminal id, in place of any value inherited from a terminal the host was
// launched in, since Windows keeps the first of two entries.
func terminalEnvironment(id, proof string) []string {
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(strings.ToUpper(entry), IDVariable+"=") || strings.HasPrefix(strings.ToUpper(entry), ProofVariable+"=")
	})
	return append(env, IDVariable+"="+id, ProofVariable+"="+proof)
}

// announce opens the host's pipe and records where to find it, with when the
// terminal's program started and the digest of the proof value its terminal
// carries.
func announce(stateDir, id string, childPID int, proof string, isStream bool) (Record, *listener, error) {
	name, err := pipeName()
	if err != nil {
		return Record{}, nil, err
	}
	pipe, err := listen(name)
	if err != nil {
		return Record{}, nil, err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Record{}, nil, err
	}
	// The pseudo console holds the program's handle, so its pid names it
	// until the host lets go, even once it has exited.
	childStart, ok := proc.StartTime(childPID)
	if !ok {
		return Record{}, nil, fmt.Errorf("host: the terminal's program pid %d has no start time", childPID)
	}
	record := Record{ID: id, Pipe: name, Token: hex.EncodeToString(secret[:]), Version: Version, HostPID: os.Getpid(), ChildPID: childPID, ChildStart: childStart, Started: time.Now().UTC(), ProofSum: ProofSum(proof), Stream: isStream}
	if err := writeRecord(stateDir, record); err != nil {
		return Record{}, nil, fmt.Errorf("host: record the host: %w", err)
	}
	// The record goes when this host does and is replaced by the next one's.
	// The terminal's proofs outlast both, for what it leaves running.
	if err := keepProof(stateDir, id, record.ProofSum, record.Started, time.Now().Add(-windows.DurationSinceBoot())); err != nil {
		return Record{}, nil, fmt.Errorf("host: keep the terminal's proof: %w", err)
	}
	return record, pipe, nil
}

// Running reports whether the host record names may still run: only a
// process Windows shows as ended, or as never started, does not, and neither
// does one that started after the host recorded itself, which reuses the pid
// of a host that ended without removing its record. A process this user may
// not open, as a system service's is, is judged by the start the machine's
// process list gives, which needs no handle: a host's pid such a process took
// after a restart once read as the host for good (issue #1206).
func Running(record Record) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(record.HostPID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false
	}
	if err == nil {
		defer windows.CloseHandle(handle)
		var code uint32
		if err := windows.GetExitCodeProcess(handle, &code); err == nil && code != stillActive {
			return false
		}
	}
	started, known := proc.StartTime(record.HostPID)
	if !known {
		processes, err := proc.Processes()
		if err != nil {
			return true
		}
		index := slices.IndexFunc(processes, func(process proc.Entry) bool { return process.PID == record.HostPID })
		if index < 0 {
			return false
		}
		started = processes[index].Start
	}
	return !started.After(record.Started)
}

// stillActive is the exit code Windows reports for a process that runs.
const stillActive = 259
