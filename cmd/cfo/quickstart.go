package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runQuickstart is goblins with no command, with --native or --harness, and
// goblins setup. It finds the supervisor or starts one, which never opens the
// board; one Enter at a time it makes the agent the CFO runs on ready; it
// starts the CFO in the Code Goblins home when none runs; and it ends on one
// screen: Enter shows the CFO's terminal here, and the board's link or B
// opens the board. It asks for no project, since the CFO works across every
// project from its home. The agent steps are skipped while a CFO runs and
// nothing asks for them: rerun, which goblins setup sets, or a harness named
// with --harness. native starts a new CFO in a native terminal rather than in
// Herdr. restart, which goblins resume sets, first restarts a CFO running in
// native terminal cfo on its conversation, as for a CFO whose screen froze.
// What it finds running it says first and starts nothing beside, and each
// step it finishes is one line with a tick, so the screen holds what was
// answered and the one step that waits.
func runQuickstart(stdout, stderr io.Writer, runtime commandRuntime, rerun, native, restart bool, harness string) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	board, startedServe, ok := launchBoard(ctx, runtime, h, stdout, stderr)
	if !ok {
		return 1
	}
	list := &onboarding.Checklist{Output: stdout, Tick: onboarding.MarksFor(onboarding.DrawsUnicode(os.Getenv)).Tick, Width: onboarding.ConsoleWidth, Plain: !consoleTakesEscapes(stdout), NoColor: onboarding.NoColor()}
	fmt.Fprintln(stdout)
	if startedServe {
		list.Done("Supervisor", "started")
	} else {
		list.Done("Supervisor", "already running")
	}
	// A step back to the choice of agent takes the agent's lines with it,
	// never this one.
	list.Keep()
	restarted := false
	if restart {
		conversation, resumed, err := runtime.restartCFO(h)
		switch {
		case err == nil:
			notes := runtime.settleCFO(ctx, h.State, conversation.Harness)
			if resumed {
				list.Done("CFO", fmt.Sprintf("restarted on its conversation %s, in native terminal %s", conversation.Session, supervisor.NativeCFOTerminal))
			} else {
				list.Done("CFO", fmt.Sprintf("restarted as %s on a new conversation, in native terminal %s", onboarding.Name(conversation.Harness), supervisor.NativeCFOTerminal))
				list.Note(fmt.Sprintf("Its conversation %s could not be resumed, so the CFO starts a new one.", conversation.Session))
			}
			list.Note("Its current response was interrupted; goblins keep running.")
			for _, note := range append(wakePath(conversation.Harness), notes...) {
				list.Note(note)
			}
			restarted = true
		case errors.Is(err, errNoRunningCFO):
			// A CFO that is not running comes back below, as goblins brings it.
		default:
			list.End()
			fmt.Fprintf(stderr, "goblins: %v\n", err)
			return 1
		}
	}
	// A CFO just restarted is the session this run ends on: nothing asks for
	// its agent again, and nothing says a second time that it runs.
	session, started := cfoSession{native: supervisor.NativeCFOTerminal}, restarted
	if !restarted {
		agent := ""
		if rerun || harness != "" || !cfoRuns(runtime, h.State) {
			if agent, err = runtime.setupAgent(ctx, h.State, harness, rerun, list, stdout, stderr); err != nil {
				list.End()
				fmt.Fprintf(stderr, "goblins: %v\n", err)
				return 1
			}
		}
		if session, started, err = ensureCFOSession(ctx, runtime, h, native, agent, list); err != nil {
			list.End()
			fmt.Fprintf(stderr, "goblins: %v\n", err)
			return 1
		}
		if !started && agent != "" {
			list.Note(fmt.Sprintf("It keeps its agent; %s is the agent goblins starts the next CFO as.", onboarding.Name(agent)))
		}
	}
	heading := "Your CFO is running"
	if started {
		heading = "Your CFO is starting"
	}
	link := board
	if consoleTakesEscapes(stdout) {
		link = "\x1b]8;;" + board + "\x1b\\" + board + "\x1b]8;;\x1b\\"
	}
	choice, err := runtime.choose(stdout, onboarding.Step{
		Title:   heading,
		Detail:  fmt.Sprintf("Home   %s\nBoard  %s  (Ctrl+click opens it)", h.Root, link),
		Choices: []onboarding.Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}},
	})
	if errors.Is(err, onboarding.ErrBack) {
		fmt.Fprintln(stdout, "\nThe CFO and the board keep running; run goblins to see them again.")
		return 0
	}
	if err != nil {
		// The CFO and the board run either way; only this screen had no
		// answer.
		fmt.Fprintf(stderr, "goblins: %v\n", err)
		return 1
	}
	if choice == 1 {
		if err := runtime.openURL(board); err != nil {
			fmt.Fprintf(stderr, "goblins: open the board at %s yourself (%v)\n", board, err)
			return 1
		}
		return 0
	}
	if session.native != "" {
		return runtime.attachNative(h.State, session.native, stdout, stderr)
	}
	if os.Getenv("HERDR_PANE_ID") != "" {
		fmt.Fprintln(stdout, "The CFO is in front in Herdr.")
		return 0
	}
	return runtime.attachHerdr(session.herdr)
}

// cfoRuns reports whether a CFO runs for the home: one registered in a native
// terminal or in Herdr, or native terminal cfo up for one that has not
// registered yet.
func cfoRuns(runtime commandRuntime, stateDir string) bool {
	if _, live := runtime.nativeCFO(stateDir); live {
		return true
	}
	if _, live := runtime.liveCFO(stateDir); live {
		return true
	}
	return runtime.nativeTerminalRuns(stateDir, supervisor.NativeCFOTerminal)
}

// cfoSession is where the CFO runs: a native terminal, or the Herdr session it
// runs in.
type cfoSession struct {
	native string
	herdr  string
}

// ensureCFOSession finds the CFO's session, or starts the CFO as agent in the
// home and reports whether it started one: in native terminal cfo when native
// is set, or else in Herdr's cfo tab. A CFO registered in a native terminal
// comes first, then one whose registration names a live process in Herdr,
// which is brought to the front where it registered, then native terminal cfo
// while its host answers, since the CFO started there may not have registered
// yet. A CFO is never started beside one that runs. Either way list says so
// in one line.
func ensureCFOSession(ctx context.Context, runtime commandRuntime, h home.Home, native bool, agent string, list *onboarding.Checklist) (cfoSession, bool, error) {
	if id, live := runtime.nativeCFO(h.State); live {
		list.Done("CFO", "already running in native terminal "+id)
		return cfoSession{native: id}, false, nil
	}
	if endpoint, live := runtime.liveCFO(h.State); live {
		if err := runtime.focusCFO(ctx, endpoint); err != nil {
			return cfoSession{}, false, fmt.Errorf("the CFO could not be brought to the front in Herdr: %w", err)
		}
		list.Done("CFO", "already running in Herdr")
		return cfoSession{herdr: endpoint.Target.Session}, false, nil
	}
	if runtime.nativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		list.Done("CFO", "already starting in native terminal "+supervisor.NativeCFOTerminal)
		return cfoSession{native: supervisor.NativeCFOTerminal}, false, nil
	}
	if agent == "" {
		// The CFO that ran when this launch began has ended since.
		var err error
		if agent, err = cfoHarness(h.State); err != nil {
			return cfoSession{}, false, err
		}
	}
	resume, why, ranNatively := cfoResume(h, agent)
	// said is what the lines under the CFO's own say, starting with why it
	// did not come back on its conversation, when it did not.
	var said []string
	if why != "" {
		said = append(said, why+", so the CFO starts a new one.")
	}
	// A CFO that ran in its own terminal comes back in it, and a harness the
	// supervisor wakes by typing is woken only in a native terminal, so its
	// CFO starts in one.
	native = native || ranNatively || supervisor.CFOWakeFor(agent) == supervisor.CFOWakeTyped
	if len(resume) > 0 {
		conversation := resume[len(resume)-1]
		list.Working("CFO", "coming back as "+onboarding.Name(agent)+" on its conversation")
		if err := runtime.startNativeCFO(h, h.Root, agent, resume); err != nil {
			return cfoSession{}, false, fmt.Errorf("the CFO could not be started in a native terminal: %w", err)
		}
		cfoResumeWait(cfoResumeSettle)
		if runtime.nativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
			notes := runtime.settleCFO(ctx, h.State, agent)
			list.Done("CFO", fmt.Sprintf("back as %s on its conversation %s, in native terminal %s", onboarding.Name(agent), conversation, supervisor.NativeCFOTerminal))
			for _, note := range append(wakePath(agent), notes...) {
				list.Note(note)
			}
			return cfoSession{native: supervisor.NativeCFOTerminal}, true, nil
		}
		said = append(said, fmt.Sprintf("Its conversation %s could not be resumed, so the CFO starts a new one.", conversation))
	}
	list.Working("CFO", "starting as "+onboarding.Name(agent))
	if native {
		if err := runtime.startNativeCFO(h, h.Root, agent, nil); err != nil {
			return cfoSession{}, false, fmt.Errorf("the CFO could not be started in a native terminal: %w", err)
		}
		// Its startup dialogs are answered before the line says it started.
		notes := runtime.settleCFO(ctx, h.State, agent)
		list.Done("CFO", fmt.Sprintf("started as %s in %s, in native terminal %s", onboarding.Name(agent), h.Root, supervisor.NativeCFOTerminal))
		for _, note := range append(append(said, wakePath(agent)...), notes...) {
			list.Note(note)
		}
		return cfoSession{native: supervisor.NativeCFOTerminal}, true, nil
	}
	started, err := runtime.startCFO(ctx, h.Root, agent)
	if err != nil {
		return cfoSession{}, false, fmt.Errorf("the CFO session could not be started in Herdr: %w", err)
	}
	if !started {
		list.Done("CFO", "already running in Herdr's cfo tab")
		return cfoSession{herdr: herdrSession()}, false, nil
	}
	list.Done("CFO", fmt.Sprintf("started as %s in %s", onboarding.Name(agent), h.Root))
	for _, note := range unreached(agent, nil) {
		list.Note(note)
	}
	return cfoSession{herdr: herdrSession()}, true, nil
}

// quickstartDetector reads how ready each agent is on this machine: PATH,
// each agent's own status command, pi's settings in its agent folder, and
// whether Claude Code's native build is installed.
func quickstartDetector() onboarding.Detector {
	directory, claudeDirectory := os.Getenv("PI_CODING_AGENT_DIR"), ""
	if userHome, err := os.UserHomeDir(); err == nil {
		if directory == "" {
			directory = filepath.Join(userHome, ".pi", "agent")
		}
		claudeDirectory = filepath.Join(userHome, ".local", "bin")
	}
	return onboarding.Detector{LookPath: exec.LookPath, PiDirectory: directory, ClaudeDirectory: claudeDirectory, Probe: func(ctx context.Context, name string, args ...string) (execx.Result, error) {
		program, err := spawn.NativeProgram(name, args...)
		if err != nil {
			return execx.Result{}, err
		}
		return execx.OSRunner{}.Run(ctx, execx.Request{Name: program[0], Args: program[1:], KillTree: true})
	}}
}

// setupAgent runs the quick start's agent steps in this console and returns
// the agent the CFO starts as. An installer and a sign-in run in this console,
// where the person sees them and answers them: nothing is typed for them.
// Each step finished is a line of list.
func setupAgent(ctx context.Context, stateDir, chosen string, rerun bool, list *onboarding.Checklist, stdout, stderr io.Writer) (string, error) {
	list.Working("Agent", "checking the agents on this machine")
	return rememberAgent(ctx, stateDir, chosen, rerun, onboarding.Flow{
		Detect:      quickstartDetector().Detect,
		Ask:         func(step onboarding.Step) (int, error) { return onboarding.AskConsole(stdout, step) },
		Done:        list.Done,
		Undo:        list.Clear,
		Marks:       onboarding.MarksFor(onboarding.DrawsUnicode(os.Getenv)).Agents,
		Recommended: recommendedCFOAgent(),
		Notes:       cfoAgentNotes(),
		Install:     func(id string) error { return installAgent(ctx, id, stdout, stderr) },
		Login:       func(id string) error { return signInAgent(id, stdout, stderr) },
	})
}

// rememberAgent runs flow for the home whose state is stateDir and returns
// the agent the CFO starts as. The flow starts from chosen, the agent
// --harness named, or else from the agent the home remembers, and the agent
// it ends on is remembered for every later start. A flow that ends on none
// leaves what the home remembers as it was.
func rememberAgent(ctx context.Context, stateDir, chosen string, rerun bool, flow onboarding.Flow) (string, error) {
	if chosen == "" {
		data, err := fsx.ReadFile(cfoHarnessPath(stateDir))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		chosen = strings.TrimSpace(string(data))
	}
	flow.Save = func(id string) error {
		return fsx.AtomicWriteFile(cfoHarnessPath(stateDir), []byte(id+"\n"))
	}
	return flow.Run(ctx, chosen, rerun)
}

// installAgent installs an agent the way install.ps1 does, in this console,
// then brings this process's PATH up to date so the agent it installed is
// found.
func installAgent(ctx context.Context, id string, stdout, stderr io.Writer) error {
	installer, ok := onboarding.InstallerFor(id)
	if !ok {
		return fmt.Errorf("%s has no installer", id)
	}
	heading := "Running " + installer.Describe() + ". This screen closes when it ends."
	switch installer.Kind {
	case "npm":
		if _, err := exec.LookPath("npm.cmd"); err != nil {
			return errors.New("npm is not installed; install Node.js first with: winget install OpenJS.NodeJS.LTS")
		}
		if err := runInConsole(stdout, stderr, heading, "npm.cmd", "install", "-g", installer.Source); err != nil {
			return err
		}
	default:
		// The script is saved to a file and run from it. A download-and-run
		// one-liner on a child's command line is what Defender blocks as
		// Trojan:Win32/Commando.A!ml.
		script, err := downloadInstaller(ctx, installer.Source)
		if err != nil {
			return err
		}
		defer os.Remove(script)
		if err := runInConsole(stdout, stderr, heading, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script); err != nil {
			return err
		}
	}
	return refreshPath(id)
}

// installerTimeout bounds the download of an install script.
const installerTimeout = 2 * time.Minute

// downloadInstaller saves the install script at address to a temporary file
// and returns its path.
func downloadInstaller(ctx context.Context, address string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, installerTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", address, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", address, response.StatusCode)
	}
	file, err := os.CreateTemp("", "code-goblins-install-*.ps1")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(file, response.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("save %s: %w", address, err)
	}
	return file.Name(), nil
}

// refreshPath makes what an installer added to the user's PATH visible to
// this process, which still holds the PATH it started with: every entry the
// user's own environment has and this one lacks is appended. Claude Code's
// native installer puts claude.exe in ~\.local\bin and may leave that folder
// off PATH, so it is added to the user's PATH as install.ps1 adds it.
func refreshPath(id string) error {
	userEnv, err := spawn.UserEnvironment()
	if err != nil {
		return fmt.Errorf("read the user's environment: %w", err)
	}
	var added []string
	for _, entry := range userEnv {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "PATH") {
			added = filepath.SplitList(value)
		}
	}
	if id == "claude" {
		if userHome, err := os.UserHomeDir(); err == nil {
			bin := filepath.Join(userHome, ".local", "bin")
			if _, err := os.Stat(filepath.Join(bin, "claude.exe")); err == nil {
				if err := install.AddToUserPath(bin); err != nil {
					return fmt.Errorf("add %s to your PATH: %w", bin, err)
				}
				added = append(added, bin)
			}
		}
	}
	entries := filepath.SplitList(os.Getenv("PATH"))
	for _, entry := range added {
		if entry != "" && !containsPath(entries, entry) {
			entries = append(entries, entry)
		}
	}
	return os.Setenv("PATH", strings.Join(entries, string(os.PathListSeparator)))
}

// containsPath reports whether entries names dir, as Windows compares paths.
func containsPath(entries []string, dir string) bool {
	for _, entry := range entries {
		if fsx.SamePath(entry, dir) {
			return true
		}
	}
	return false
}

// signInAgent opens an agent's own sign-in in this console and returns when
// it ends. The person signs in there: Code Goblins types nothing and never
// sees a password.
func signInAgent(id string, stdout, stderr io.Writer) error {
	const comesBack = " Code Goblins never sees your password. This screen closes when the sign-in ends."
	switch id {
	case "claude":
		return runInConsole(stdout, stderr, "Claude Code's own sign-in."+comesBack, id, "auth", "login")
	case "codex":
		return runInConsole(stdout, stderr, "Codex's own sign-in."+comesBack, id, "login")
	case "pi":
		// pi signs in from inside itself.
		return runInConsole(stdout, stderr, "pi opens next. In pi, sign in with /login, pick a model with /model, then leave with /quit to come back here.", id)
	}
	return fmt.Errorf("%s has no sign-in", id)
}

// The console's other screen: a program run on it leaves nothing behind on
// the screen the quick start draws on.
const (
	enterAlternateScreen = "\x1b[?1049h\x1b[H"
	leaveAlternateScreen = "\x1b[?1049l"
)

// runInConsole runs a program in this console, as the person would run it,
// and waits for it. In a console it runs on the other screen under heading,
// so what it printed leaves with it and the steps finished stay as they
// were; a program that failed keeps that screen until the person has read
// why. Output that is no console, such as a log, gets heading and the
// program's output as they come.
func runInConsole(stdout, stderr io.Writer, heading, name string, args ...string) error {
	program, err := spawn.NativeProgram(name, args...)
	if err != nil {
		return err
	}
	command := execx.Command(program[0], program[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, stdout, stderr
	if !consoleTakesEscapes(stdout) {
		fmt.Fprintf(stdout, "\n%s\n", heading)
		return command.Run()
	}
	fmt.Fprintf(stdout, "%s%s\n\n", enterAlternateScreen, heading)
	defer fmt.Fprint(stdout, leaveAlternateScreen)
	err = command.Run()
	if err != nil {
		// Only Enter or Escape leaves: either way the person has seen it.
		_, _ = onboarding.AskConsole(stdout, onboarding.Step{Title: "That did not finish", Detail: err.Error(), Choices: onboarding.Labels("Go back")})
	}
	return err
}
