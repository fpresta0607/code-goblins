package host

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// launchTimeout bounds how long a new host takes to record itself.
var launchTimeout = 15 * time.Second

// Launch starts a host for spec in a process of its own and returns the
// host's record once it is serving. command runs a host (cfo.exe and "host"
// in production), and env is the whole environment the host and its terminal
// run with. The host is detached from this process, in its own process group
// and out of this process's job where Windows allows it, so it outlives
// whatever launched it. Its output goes to state/hosts/<id>.log.
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
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Record{}, err
	}
	defer log.Close()
	args := append(append([]string(nil), command[1:]...), "--state", stateDir, "--id", spec.ID, "--dir", spec.Dir,
		"--cols", strconv.Itoa(spec.Cols), "--rows", strconv.Itoa(spec.Rows), "--")
	args = append(args, spec.Args...)
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(command[0], args...)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		return cmd, cmd.Start()
	}
	// A hidden console of its own, so nothing the host runs opens a window.
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	cmd, err := start(flags | windows.CREATE_BREAKAWAY_FROM_JOB)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// This process is in a job that forbids breaking away; the host
		// starts inside it instead.
		cmd, err = start(flags)
	}
	if err != nil {
		return Record{}, fmt.Errorf("host: start %s: %w", command[0], err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for deadline := time.Now().Add(launchTimeout); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if record, err := ReadRecord(stateDir, spec.ID); err == nil && record.HostPID == cmd.Process.Pid {
			return record, nil
		}
		select {
		case err := <-exited:
			return Record{}, fmt.Errorf("host: the host ended before it served (%v); see %s", err, logPath)
		default:
		}
	}
	_ = cmd.Process.Kill()
	return Record{}, fmt.Errorf("host: host pid %d did not record itself within %s; see %s", cmd.Process.Pid, launchTimeout, logPath)
}
