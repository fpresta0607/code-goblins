package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/release"
)

func TestRun(t *testing.T) {
	drainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(drainHome, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessionStartHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sessionStartHome, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args prints usage", args: nil, wantExit: 2, wantStderr: "usage: cfo"},
		{name: "unknown command", args: []string{"nonsense"}, wantExit: 2, wantStderr: `unknown command "nonsense"`},
		{name: "version", args: []string{"version"}, wantExit: 0, wantStdout: "cfo dev"},
		{name: "doctor runs and reports each tool", args: []string{"doctor"}, wantExit: -1, wantStdout: "git"},
		{name: "drain empty queue", args: []string{"drain"}, env: map[string]string{"CFO_HOME": drainHome}, wantExit: 0, wantStdout: "WAKE QUEUE: empty"},
		{name: "watch refuses outside a primary home", args: []string{"watch"}, wantExit: 1, wantStderr: "not a primary", env: map[string]string{"CFO_HOME": t.TempDir()}},
		{name: "session-start alias", args: []string{"session-start"}, wantExit: 0, wantStdout: "== SESSION LOCK ==", env: map[string]string{"CFO_HOME": sessionStartHome}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var stdout, stderr bytes.Buffer
			got := run(tt.args, &stdout, &stderr)
			if tt.wantExit != -1 && got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if tt.wantExit == -1 && got != 0 && got != 1 {
				t.Errorf("exit = %d, want 0 or 1 (doctor's health verdict)", got)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunUsageListsAllRequiredDoctorTools(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run(nil, &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	for _, tool := range []string{"codex", "pi"} {
		if !strings.Contains(stderr.String(), tool) {
			t.Errorf("usage = %q, want it to contain %q", stderr.String(), tool)
		}
	}
}

func TestRunUsageListsFleetCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run(nil, &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	for _, command := range []string{
		"cfo spawn <id> --project <name|path> --brief <path> [--harness <claude|codex|pi>]",
		"cfo send <target> [--key <key>] <text...>",
		"cfo peek <target> [lines]",
		"cfo fleet-view [--json]",
		"cfo tickets <project> [--brief <file>] [--files <paths>] [--json]",
	} {
		if !strings.Contains(stderr.String(), command) {
			t.Errorf("usage = %q, want it to contain %q", stderr.String(), command)
		}
	}
}

// TestMain keeps this suite off the machine's own fleet. On a host where the
// CFO is installed, CFO_HOME, CFO_STATE_OVERRIDE, and a real ~/.claude are
// all in the environment, and a test that resolves any of them writes into
// the live wake queue - which is not a hypothetical: it is how this guard
// came to be written.
// startedAs holds the markers this binary started with that say a goblin or a
// gate agent runs it. TestMain takes them out of this process so the hooks
// under test act, and a proof against real harnesses hands them back to the
// commands it runs itself that act as the CFO (wakeProof.commandEnv).
var startedAs []string

func TestMain(m *testing.M) {
	// Every build a test runs, in this process or as a stand-in a test
	// installs, stands in for a build that carries its board, which CI's go
	// jobs never build. A test of the refusal says otherwise itself.
	boardBuilt = func() bool { return true }
	// No test's supervisor takes the board's usual address, which the fleet
	// of the machine the tests run on may hold: each asks for any free port,
	// and so does every program a test starts.
	if err := os.Setenv(boardAddressVariable, "127.0.0.1:0"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Nor does any supervisor a test starts look for a release on GitHub:
	// it looks at a port that refuses at once, unless its test names a
	// release server of its own.
	if _, isSet := os.LookupEnv(release.APIVariable); !isSet {
		if err := os.Setenv(release.APIVariable, "http://127.0.0.1:1/latest"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	// Nor does a doctor a test runs ask npm or Claude Code's channel for the
	// harnesses' newest versions, unless its test names a source of its own.
	if _, isSet := os.LookupEnv(doctor.ReleasesVariable); !isSet {
		if err := os.Setenv(doctor.ReleasesVariable, "http://127.0.0.1:1"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	// A build stand-in an update test installs runs as that build.
	if code, ok := runStandInBuild(); ok {
		os.Exit(code)
	}
	// The tests of cfo gate turn run this binary as the wrapper and as the
	// command it starts.
	if code, ok := runGateTurnTestProgram(); ok {
		os.Exit(code)
	}
	// goblins starts its own program as serve. A test that reaches that start
	// would run this binary, and with it every test again, detached and with
	// no one waiting: it serves nothing instead.
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(2)
	}
	// fakeDoctorTool's claude.exe is this binary answering doctor's --version
	// probe; run as claude.exe any other way, it is the native CFO test's
	// harness, below.
	if strings.EqualFold(filepath.Base(os.Args[0]), "claude.exe") && len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("claude 1.0.0")
		os.Exit(0)
	}
	if stateDir := os.Getenv(watchLockStandInVariable); stateDir != "" {
		os.Exit(holdWatchLockAs(stateDir, os.Getenv(watchLockRoleVariable)))
	}
	if report := os.Getenv(consoleProbeVariable); report != "" {
		os.Exit(probeConsole(report))
	}
	if report := os.Getenv(environmentReportVariable); report != "" {
		os.Exit(reportEnvironment(report))
	}
	if len(os.Args) > 1 && os.Args[1] == attachTestTerminal {
		attachTestProgram()
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == nativeSendPasteComposer {
		os.Exit(runNativeSendPasteComposer(os.Args[2:]))
	}
	if len(os.Args) > 2 && os.Args[1] == attachTestViewer {
		os.Exit(attachTestView(os.Args[2], os.Args[3:]))
	}
	if len(os.Args) > 1 && os.Args[1] == authStoreConsole {
		os.Exit(runAuth(os.Args[2:], os.Stdout, os.Stderr, commandRuntime{}))
	}
	// A stand-in session's harness runs this binary as cfo.exe, as Claude
	// Code runs the home's cfo for a hook or for a command of its tools.
	if strings.EqualFold(filepath.Base(os.Args[0]), sessionCLI) {
		os.Exit(runSessionCLI())
	}
	// The native CFO test starts this binary as cfo host, and as the
	// claude.exe its terminal runs.
	if len(os.Args) > 1 && os.Args[1] == "host" {
		os.Exit(runHost(os.Args[2:], os.Stderr))
	}
	if strings.EqualFold(filepath.Base(os.Args[0]), "claude.exe") {
		os.Exit(runFakeClaude())
	}
	// HERDR_PANE_ID and CFO_HOST_ID are unset too: a hook under test must
	// never register this machine's real Herdr pane or native terminal as a
	// test home's CFO. NO_MISTAKES_GATE is unset because every hook does
	// nothing under it, and a gate agent running this suite exports it, so
	// the hook tests would test nothing; a test of that behaviour sets it.
	for _, name := range []string{harness.RoleVariable, gateAgentVariable} {
		if value := os.Getenv(name); value != "" {
			startedAs = append(startedAs, name+"="+value)
		}
	}
	for _, name := range []string{"CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_ROLE", "HERDR_PANE_ID", host.IDVariable, gateAgentVariable} {
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	configDir, err := os.MkdirTemp("", "cfo-test-claude-config-")
	if err != nil {
		panic(err)
	}
	// An uninstall a test runs removes the desktop shortcut from a folder of
	// its own, never from this machine's desktop.
	if err := os.Setenv(install.DesktopVariable, configDir); err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", configDir); err != nil {
		panic(err)
	}
	// A Codex spawn or switch reads the MCP servers of CODEX_HOME's
	// config.toml, which is never this machine's own.
	if err := os.Setenv("CODEX_HOME", configDir); err != nil {
		panic(err)
	}
	// Doctor's harness map reads Pi's folder, which is this machine's own and
	// absent on a runner with no Pi.
	if err := os.Setenv("PI_CODING_AGENT_DIR", configDir); err != nil {
		panic(err)
	}
	// The process value answers before the user scope is read, so pinning it
	// keeps every test that runs a real command off this machine's registry,
	// and off whichever checkouts its operator happens to keep.
	if err := os.Setenv(install.ProjectsRootVariable, configDir); err != nil {
		panic(err)
	}
	// A test binary an agent runs, as a goblin's or a gate's is, has that
	// agent's harness among its parents: no test is refused a command that
	// acts as the CFO for it. A test of the refusal names the session itself.
	notTheCFO = func(string) error { return nil }
	code := m.Run()
	os.RemoveAll(configDir)
	removeSessionPrograms()
	os.Exit(code)
}
