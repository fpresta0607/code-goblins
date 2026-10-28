package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/priority"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Arguments are what a host is run with, after the command that runs one, to
// host spec and record itself under stateDir.
func Arguments(stateDir string, spec Spec) []string {
	args := []string{"--state", stateDir, "--id", spec.ID, "--dir", spec.Dir, "--cols", strconv.Itoa(spec.Cols), "--rows", strconv.Itoa(spec.Rows)}
	if spec.ShouldLeaveCoresForApps {
		args = append(args, "--leave-cores-for-apps")
	}
	return append(append(args, "--"), spec.Args...)
}

// launchTimeout bounds how long a new host takes to record itself, and a
// launch looks for that record through readLaunchRecord. Only time the
// machine ran spends launchTimeout (awake), since a pause of the whole
// machine stops the host too.
var (
	launchTimeout    = 15 * time.Second
	readLaunchRecord = ReadRecord
)

// Launch starts a host for spec in a process of its own and returns the
// host's record once it is serving. command runs a host (cfo.exe and "host"
// in production), and env is the whole environment the host and its terminal
// run with. The host is detached from this process, in its own process group
// and out of this process's job where Windows allows it, so it outlives
// whatever launched it. Where the job forbids it, the host starts inside the
// job and ends when the job closes: the record Launch returns says so as
// Contained, and so does the host's log. Its output goes to
// state/hosts/<id>.log. A host whose terminal's program ends at once can
// serve and end before Launch sees its record: it returns the record that
// host's end keeps, with no pipe to dial, since the host is gone. A host that
// ends before it serves fails the launch with how it ended and the end of its
// log.
func Launch(stateDir string, command, env []string, spec Spec) (Record, error) {
	if len(command) == 0 {
		return Record{}, errors.New("host: the command that runs a host is required")
	}
	if err := state.ValidTaskID(spec.ID); err != nil {
		return Record{}, err
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		return Record{}, err
	}
	logPath := filepath.Join(stateDir, "hosts", spec.ID+".log")
	log, err := fsx.OpenAppend(logPath, 0o600)
	if err != nil {
		return Record{}, err
	}
	defer log.Close()
	args := append(append([]string(nil), command[1:]...), Arguments(stateDir, spec)...)
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := execx.Command(command[0], args...)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr.CreationFlags |= flags
		return cmd, cmd.Start()
	}
	// A hidden console of its own, so nothing the host runs opens a window,
	// and one class above the work from its start, which this launch waits
	// on.
	flags := uint32(windows.CREATE_NO_WINDOW|windows.CREATE_NEW_PROCESS_GROUP) | priority.StartAboveTheWork()
	launched := time.Now()
	cmd, err := start(flags | windows.CREATE_BREAKAWAY_FROM_JOB)
	contained := errors.Is(err, windows.ERROR_ACCESS_DENIED)
	if contained {
		// This process is in a job that forbids breaking away; the host
		// starts inside it instead.
		fmt.Fprintf(log, "host: cannot break away from the job of the process that launched it (pid %d), so this host runs inside that job and ends when it closes\n", os.Getpid())
		cmd, err = start(flags)
	}
	if err != nil {
		return Record{}, fmt.Errorf("host: start %s: %w", command[0], err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	launching, cancelLaunching := awake.WithTimeout(context.Background(), launchTimeout)
	defer cancelLaunching()
	for ; launching.Err() == nil; time.Sleep(25 * time.Millisecond) {
		if record, err := readLaunchRecord(stateDir, spec.ID); err == nil && record.HostPID == cmd.Process.Pid {
			record.Contained = contained
			return record, nil
		}
		select {
		case err := <-exited:
			// A host that served removes its record as it ends, which can
			// fall between two looks, and leaves its end, which names it. An
			// end from before this launch is an earlier host's, whose pid
			// this one may have been given.
			if end, endErr := ReadEnd(stateDir, spec.ID); endErr == nil && end.HostPID == cmd.Process.Pid && end.Ended.After(launched) {
				return Record{ID: spec.ID, HostPID: end.HostPID, ChildPID: end.ChildPID, ChildStart: end.ChildStart, Contained: contained}, nil
			}
			if err == nil {
				err = errors.New("exit status 0")
			}
			return Record{}, fmt.Errorf("host: the host ended before it served (%v); %s ends:\n%s", err, logPath, logTail(logPath))
		default:
		}
	}
	// A host that never announced leaves its log as the only evidence, and
	// this launch ends it. So the log says what was true at the bound, since on
	// 2026-10-24 a launch that timed out left nothing but the notice that it
	// could not break away, and the phase the host had reached was lost with
	// it.
	alive := cmd.ProcessState == nil
	fmt.Fprintf(log, "host: pid %d had not recorded itself after %s of the %s this launch allows (still running: %t); this launch ends it\n", cmd.Process.Pid, time.Since(launched).Round(time.Millisecond), launchTimeout, alive)
	_ = cmd.Process.Kill()
	// Kill only asks Windows to end the host, which holds its log and its
	// terminal's job until it has ended, and on 2026-10-24 still held the log
	// when the test that launched it removed its folder. So the launch returns
	// once the host has ended, and the log says how long that took.
	killed := time.Now()
	ending, cancelEnding := awake.WithTimeout(context.Background(), closeEndWait)
	defer cancelEnding()
	select {
	case <-exited:
		fmt.Fprintf(log, "host: pid %d ended %s after this launch ended it\n", cmd.Process.Pid, time.Since(killed).Round(time.Millisecond))
	case <-ending.Done():
		return Record{}, fmt.Errorf("host: host pid %d did not record itself within %s, and had not ended %s after this launch ended it; see %s", cmd.Process.Pid, launchTimeout, closeEndWait, logPath)
	}
	return Record{}, fmt.Errorf("host: host pid %d did not record itself within %s; see %s", cmd.Process.Pid, launchTimeout, logPath)
}

// logTailSize bounds how much of a host's log a failed launch shows: every
// host of a terminal appends to its log, and the one that just ended wrote
// last.
const logTailSize = 2 << 10

// logTail is the end of the host log at path, from its first whole line in
// the last logTailSize bytes.
func logTail(path string) string {
	file, err := fsx.Open(path)
	if err != nil {
		return err.Error()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err.Error()
	}
	from := max(0, info.Size()-logTailSize)
	data, err := io.ReadAll(io.NewSectionReader(file, from, info.Size()-from))
	if err != nil {
		return err.Error()
	}
	if _, rest, found := bytes.Cut(data, []byte("\n")); from > 0 && found {
		data = rest
	}
	if text := strings.TrimSpace(string(data)); text != "" {
		return text
	}
	return "(nothing)"
}
