package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/boardweb"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

// defaultBoardAddress is where cfo serve listens unless told otherwise.
const defaultBoardAddress = "127.0.0.1:4310"

func runServe(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	// Serve does not care where it was started. Started from a Herdr pane,
	// such as the CFO's own, it would hand that pane's variables to every
	// terminal and herdr client it runs (HERDR_ENV, HERDR_PANE_ID,
	// HERDR_TAB_ID, HERDR_WORKSPACE_ID, HERDR_STARTUP_CWD, HERDR_SOCKET_PATH
	// and HERDR_BIN_PATH), and herdr refuses to start inside what they name
	// as another Herdr, so it forgets them first. HERDR_SESSION and
	// configuration such as HERDR_CONFIG_PATH are not the pane's and are kept.
	// Started from a native terminal, such as the CFO's own, it would hand
	// that terminal's id and proof value to every process it starts, and the
	// proof value proves a process runs in that terminal, so it forgets them
	// too.
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); herdr.IsPaneVariable(name) || strings.EqualFold(name, host.IDVariable) || strings.EqualFold(name, host.ProofVariable) {
			if err := os.Unsetenv(name); err != nil {
				fmt.Fprintf(stderr, "cfo serve: forget the starting terminal's %s: %v\n", name, err)
				return 1
			}
		}
	}
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	f.SetOutput(stderr)
	address := f.String("listen", defaultBoardAddress, "loopback address for the native board")
	example := f.Bool("example", false, "label an isolated temporary example home and omit machine-wide orphan inventory")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		fmt.Fprintln(stderr, "serve requires a numeric loopback address, for example 127.0.0.1:4310")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// A supervisor already serving this home is the one supervisor: a second
	// serve says where it is rather than failing to take the address or the
	// lock from it.
	if record, err := readBoardRecord(h.State); err == nil && boardAlive(context.Background(), record) == nil {
		fmt.Fprintf(stderr, "cfo serve: the supervisor already serves this home's board at %s; goblins status shows it, goblins stop stops it\n", record.URL)
		return 1
	}
	if *example {
		rel, err := filepath.Rel(os.TempDir(), h.Root)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.EqualFold(filepath.Clean(h.State), filepath.Join(h.Root, "state")) {
			fmt.Fprintln(stderr, "--example requires a separate home under the temporary directory with its own state directory")
			return 2
		}
	}
	assets, err := boardweb.Assets()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	config := watch.ConfigFromEnv(h)
	if config.Cleanup != nil {
		config.Cleanup()
	}
	config.WaitEvent = nil
	config.Cleanup = nil
	if *example {
		// The production orphan collector intentionally inventories the entire
		// machine. A temporary example must not mix that with isolated panes.
		config.Reap = nil
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// An unresolvable projects root still lists this home's own merges.
	projects, _ := install.MachineProjectsRoot()
	// The first-run page reads each agent's sign-in under the home folder;
	// without one the board serves no first-run page.
	var firstRun *supervisor.FirstRun
	if userHome, err := os.UserHomeDir(); err != nil {
		fmt.Fprintf(stderr, "cfo serve: the first-run page is off, the home folder is unknown: %v\n", err)
	} else {
		firstRun = firstRunOn(h, userHome, *example, install.SetMachineProjectsRoot)
	}
	s, err := supervisor.Start(ctx, h, supervisor.Options{
		Example:          *example,
		CFO:              &supervisor.CFOConnection{State: h.State, Terminals: terminal.HerdrSessions(&herdr.Client{Commands: execx.OSRunner{}, Sockets: herdr.NewSocketCache()})},
		Gate:             pipeline.Reader{Root: root, Commands: execx.OSRunner{}},
		MergedPRs:        supervisor.GitMergedPRs(supervisor.FleetRepos(h, projects)),
		PullRequestState: supervisor.GitHubPullRequestState(execx.OSRunner{}),
		Reconcile:        func(ctx context.Context) error { return watch.Reconcile(ctx, config) },
		VerifyDelivery:   (supervisor.Git{}).VerifyDelivery,
		Runs:             supervisor.OSRunLauncher{},
		PollPage:         (axi.Lavish{Commands: execx.OSRunner{}}).Poll,
		FirstRun:         firstRun,
		Dispatch:         &supervisor.Dispatch{Memory: supervisor.MachineMemory, CommitHolders: supervisor.CommitHolders, Spawn: spawnFromBoard},
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer s.Close()
	// goblins finds the board through this record; the supervisor holds the
	// singleton by now, so no other one can be writing it.
	if err := writeBoardRecord(h.State, boardRecord{PID: os.Getpid(), URL: "http://" + listener.Addr().String()}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer removeBoardRecord(h.State, os.Getpid())
	server := &http.Server{Handler: supervisor.NewHTTP(s, listener.Addr().String(), assets), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		select {
		case <-ctx.Done():
		case <-s.Done():
		}
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Fprintf(stdout, "CFO native board: http://%s\nSupervisor PID %d; browser-independent; Ctrl-C stops this process.\n", listener.Addr(), os.Getpid())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// firstRunOn is what the first-run page reads and changes on this machine for
// the CFO home h: the home its CFO starts in, and the agent goblins
// remembered for it, which the page shows as chosen and remembers in turn.
// setMachine records the projects folder as this machine's setting; an
// example board, such as a test fixture, never calls it and records the
// folder for itself alone.
func firstRunOn(h home.Home, userHome string, example bool, setMachine func(root string) error) *supervisor.FirstRun {
	return &supervisor.FirstRun{
		Home:     userHome,
		LookPath: exec.LookPath,
		CFOHome:  h.Root,
		SavedAgent: func() string {
			// Only a choice the quick start wrote is one: a home with none
			// has no answer yet, and a file that names no agent is no answer.
			if _, err := os.Stat(cfoHarnessPath(h.State)); err != nil {
				return ""
			}
			agent, err := cfoHarness(h.State)
			if err != nil {
				return ""
			}
			return agent
		},
		SaveAgent: func(agent string) error {
			return fsx.AtomicWriteFile(cfoHarnessPath(h.State), []byte(agent+"\n"))
		},
		ProjectsRoot: install.MachineProjectsRoot,
		SetProjectsRoot: func(root string) error {
			if !example {
				if err := setMachine(root); err != nil {
					return err
				}
			}
			// This supervisor, and the CFO it starts, read the process
			// environment first, and it still holds the old root.
			return os.Setenv(install.ProjectsRootVariable, root)
		},
		CFORuns:  func() bool { return supervisor.CFORuns(h.State) },
		StartCFO: func(agent string) error { return startNativeCFO(h, h.Root, agent) },
	}
}

// spawnFromBoard runs this cfo binary with args, as a queued task's Start
// dispatches it through cfo spawn, and returns everything it printed.
func spawnFromBoard(ctx context.Context, args []string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	result, err := (execx.OSRunner{}).Run(ctx, execx.Request{Name: self, Args: args})
	output := string(result.Stdout) + string(result.Stderr)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("cfo spawn exited %d", result.ExitCode)
	}
	return output, err
}
