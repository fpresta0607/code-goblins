package services

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// scriptedDocker answers each docker call by its arguments.
type scriptedDocker struct {
	answers map[string]execx.Result
	calls   []execx.Request
}

func (d *scriptedDocker) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	d.calls = append(d.calls, request)
	return d.answers[strings.Join(request.Args, " ")], nil
}

func TestCLINamesTheStackByItsFilesFromTheCheckout(t *testing.T) {
	// Arrange
	compose := Compose{Dir: `C:\dev\PrecisionDocs-AI`, File: "docker-compose.dev.yml", EnvFile: ".env.docker.local"}
	runner := &scriptedDocker{answers: map[string]execx.Result{
		"compose --file docker-compose.dev.yml --env-file .env.docker.local ps --services --status running": {Stdout: []byte("redis\r\nbackend\r\n\r\n")},
	}}
	cli := CLI{Commands: runner}

	// Act
	running, runErr := cli.Running(context.Background(), compose)
	upErr := cli.Up(context.Background(), compose, []string{"backend", "worker-light"})
	downErr := cli.Down(context.Background(), compose, []string{"worker-light"})
	wholeErr := cli.Down(context.Background(), compose, nil)

	// Assert
	if err := errorsOf(runErr, upErr, downErr, wholeErr); err != "" {
		t.Fatal(err)
	}
	if !slices.Equal(running, []string{"redis", "backend"}) {
		t.Errorf("running = %q, want the services one per line", running)
	}
	want := []string{
		"compose --file docker-compose.dev.yml --env-file .env.docker.local ps --services --status running",
		"compose --file docker-compose.dev.yml --env-file .env.docker.local up --detach --wait --wait-timeout 900 backend worker-light",
		"compose --file docker-compose.dev.yml --env-file .env.docker.local down worker-light",
		"compose --file docker-compose.dev.yml --env-file .env.docker.local down",
	}
	for index, call := range runner.calls {
		if call.Name != "docker" || call.Dir != compose.Dir {
			t.Errorf("call %d = %s in %q, want docker in the checkout", index, call.Name, call.Dir)
		}
		if !call.KillTree {
			t.Errorf("call %d does not end what it started when it is ended, as compose up's build must at the floor", index)
		}
		if got := strings.Join(call.Args, " "); index < len(want) && got != want[index] {
			t.Errorf("call %d = %q, want %q", index, got, want[index])
		}
	}
	if len(runner.calls) != len(want) {
		t.Errorf("calls = %d, want %d", len(runner.calls), len(want))
	}
}

func TestCLILeavesTheEnvFileOutWhenNoneIsDeclared(t *testing.T) {
	runner := &scriptedDocker{}

	_ = CLI{Commands: runner}.Down(context.Background(), Compose{Dir: `C:\p`, File: "compose.yml"}, nil)

	if got := strings.Join(runner.calls[0].Args, " "); got != "compose --file compose.yml down" {
		t.Fatalf("args = %q, want no --env-file", got)
	}
}

func TestCLIStartsTheEngineAndChecksItAnswers(t *testing.T) {
	cases := []struct {
		name    string
		info    execx.Result
		wantErr string
	}{
		{"answers", execx.Result{Stdout: []byte("29.7.2\n")}, ""},
		{"started but silent", execx.Result{ExitCode: 1, Stderr: []byte("failed to connect to the docker API")}, "does not answer docker info"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			runner := &scriptedDocker{answers: map[string]execx.Result{"info --format {{.ServerVersion}}": tc.info}}
			desktop := &fakeDesktop{}

			// Act
			err := CLI{Commands: runner, Desktop: desktop.desktop(10 * time.Millisecond), Out: io.Discard}.StartEngine(context.Background())

			// Assert
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if got := strings.Join(runner.calls[0].Args, " "); got != "desktop start --timeout 300" {
				t.Errorf("first call = %q, want Docker Desktop started and waited for", got)
			}
		})
	}
}

// fakeDesktop is the desktop as an engine start sees it. Processes running
// before the start are there from the first look, and the ones Docker
// Desktop starts appear once docker desktop start runs. The window process
// dashboard makes its dashboard window at the look madeAt and shows it from
// the look showsAt, and never with 0.
type fakeDesktop struct {
	mu              sync.Mutex
	before          []fakeProcess
	started         []fakeProcess
	isRun           bool
	dashboard       int
	madeAt, showsAt int
	looks           int
	isClosed        bool
	listed          []int
	asked           []proc.Window
}

type fakeProcess struct {
	entry     proc.Entry
	arguments []string
}

func (d *fakeDesktop) desktop(grace time.Duration) Desktop {
	return Desktop{Processes: d.processes, Arguments: d.arguments, Windows: d.windows, CloseWindow: d.closeWindow, Every: time.Millisecond, Grace: grace}
}

// run is docker desktop start: Docker Desktop's processes start.
func (d *fakeDesktop) run() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.isRun = true
	for index := range d.started {
		d.started[index].entry.Start = time.Now()
	}
}

func (d *fakeDesktop) all() []fakeProcess {
	if !d.isRun {
		return d.before
	}
	return append(slices.Clone(d.before), d.started...)
}

func (d *fakeDesktop) processes() ([]proc.Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var entries []proc.Entry
	for _, process := range d.all() {
		entries = append(entries, process.entry)
	}
	return entries, nil
}

func (d *fakeDesktop) arguments(pid int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, process := range d.all() {
		if process.entry.PID == pid {
			return process.arguments, nil
		}
	}
	return nil, errors.New("no such process")
}

// windows are the window process's windows at each look: the two that keep
// it in the tray, which carry no title, from the first, and its dashboard
// window, made hidden at the look madeAt and shown from the look showsAt,
// until it is closed.
func (d *fakeDesktop) windows(pid int) ([]proc.Window, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.listed = append(d.listed, pid)
	if pid != d.dashboard {
		return nil, nil
	}
	d.looks++
	owned := []proc.Window{
		{Handle: 1, Owner: pid, Class: "Electron_NotifyIconHostWindow"},
		{Handle: 2, Owner: pid, Class: "Chrome_WidgetWin_0"},
	}
	if d.madeAt != 0 && d.looks >= d.madeAt && !d.isClosed {
		owned = append(owned, proc.Window{Handle: 3, Owner: pid, Class: "Chrome_WidgetWin_1", Title: "Docker Desktop", IsShown: d.showsAt != 0 && d.looks >= d.showsAt})
	}
	return owned, nil
}

func (d *fakeDesktop) closeWindow(window proc.Window) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked = append(d.asked, window)
	d.isClosed = d.isClosed || window.Handle == 3
	return true, nil
}

// startingDocker is docker as Docker Desktop starts: desktop start starts
// the desktop's processes, and then the engine answers.
type startingDocker struct {
	desktop *fakeDesktop
}

func (d startingDocker) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch strings.Join(request.Args, " ") {
	case "desktop start --timeout 300":
		d.desktop.run()
		return execx.Result{}, nil
	case "info --format {{.ServerVersion}}":
		return execx.Result{Stdout: []byte("29.7.2\n")}, nil
	}
	return execx.Result{ExitCode: 1}, nil
}

const dockerDesktopProgram = `C:\Program Files\Docker\Docker\frontend\Docker Desktop.exe`

// Docker Desktop opens its dashboard on every start while its own setting to
// open it at start is on, which is the Overlord's. An engine start closes
// that window and only that one: the window process the start started,
// found by its program, its arguments and its start, and never Electron's
// helpers, a Docker Desktop of before the start, or any other program. A
// window process with no dashboard to show, as his setting turned off gives,
// is never asked, and nothing is when no window process starts.
func TestCLIStartEngineClosesOnlyTheDashboardItsStartOpened(t *testing.T) {
	launcher := fakeProcess{proc.Entry{PID: 100, ExeBase: "Docker Desktop.exe"}, []string{`C:\Program Files\Docker\Docker\Docker Desktop.exe`}}
	backend := fakeProcess{proc.Entry{PID: 101, ExeBase: "com.docker.backend.exe"}, []string{"com.docker.backend.exe"}}
	dashboard := fakeProcess{proc.Entry{PID: 102, ExeBase: "Docker Desktop.exe"}, []string{dockerDesktopProgram, "--reason=open-tray", "--analytics-enabled=true", "--disable-hardware-acceleration=false", "--name=dashboard"}}
	tray := fakeProcess{proc.Entry{PID: 102, ExeBase: "Docker Desktop.exe"}, []string{dockerDesktopProgram, "--reason=open-tray", "--analytics-enabled=true", "--disable-hardware-acceleration=false"}}
	renderer := fakeProcess{proc.Entry{PID: 103, ExeBase: "Docker Desktop.exe"}, []string{dockerDesktopProgram, "--type=renderer", "--reason=open-tray", "--name=dashboard"}}
	earlier := fakeProcess{proc.Entry{PID: 90, ExeBase: "Docker Desktop.exe", Start: time.Now().Add(-time.Hour)}, []string{dockerDesktopProgram, "--reason=open-tray", "--name=dashboard"}}
	editor := fakeProcess{proc.Entry{PID: 91, ExeBase: "notepad.exe", Start: time.Now().Add(-time.Hour)}, []string{"notepad.exe"}}
	hidden := proc.Window{Handle: 3, Owner: 102, Class: "Chrome_WidgetWin_1", Title: "Docker Desktop"}
	shown := hidden
	shown.IsShown = true
	cases := []struct {
		name            string
		started         []fakeProcess
		madeAt, showsAt int
		wantAsked       []proc.Window
		wantListed      bool
		wantSaid        string
	}{
		{"the dashboard is closed before it shows", []fakeProcess{launcher, backend, renderer, dashboard}, 3, 6, []proc.Window{hidden}, true, "closed the dashboard window Docker Desktop made as its engine started (pid 102) before it showed"},
		{"a dashboard shown as it is made is closed once shown", []fakeProcess{launcher, backend, dashboard}, 3, 3, []proc.Window{shown}, true, "closed the dashboard window Docker Desktop opened as its engine started (pid 102), which had shown before cfo found it"},
		{"the dashboard is never made", []fakeProcess{launcher, backend, dashboard}, 0, 0, nil, true, "Docker Desktop's window process (pid 102) made no dashboard window while cfo looked"},
		{"his setting shows no dashboard", []fakeProcess{launcher, backend, tray}, 1, 1, nil, false, ""},
		{"no window process starts", []fakeProcess{launcher, backend}, 1, 1, nil, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			desktop := &fakeDesktop{before: []fakeProcess{earlier, editor}, started: slices.Clone(tc.started), dashboard: 102, madeAt: tc.madeAt, showsAt: tc.showsAt}
			var said strings.Builder
			cli := CLI{Commands: startingDocker{desktop: desktop}, Desktop: desktop.desktop(200 * time.Millisecond), Out: &said}

			// Act
			err := cli.StartEngine(context.Background())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			desktop.mu.Lock()
			defer desktop.mu.Unlock()
			if !slices.Equal(desktop.asked, tc.wantAsked) {
				t.Errorf("asked %+v to close, want %+v", desktop.asked, tc.wantAsked)
			}
			for _, pid := range desktop.listed {
				if pid != 102 {
					t.Errorf("listed the windows of pid %d, want only the start's window process's", pid)
				}
			}
			if isListed := len(desktop.listed) > 0; isListed != tc.wantListed {
				t.Errorf("listed the window process's windows = %t, want %t", isListed, tc.wantListed)
			}
			if tc.wantSaid == "" && said.Len() != 0 || !strings.Contains(said.String(), tc.wantSaid) {
				t.Errorf("said %q, want %q", said.String(), tc.wantSaid)
			}
		})
	}
}

// A window process with no dashboard to show ends the look at once, so a
// start under his setting turned off waits for nothing.
func TestCLIStartEngineWaitsForNoDashboardWhenNoneWillShow(t *testing.T) {
	// Arrange
	tray := fakeProcess{proc.Entry{PID: 102, ExeBase: "Docker Desktop.exe"}, []string{dockerDesktopProgram, "--reason=open-tray"}}
	desktop := &fakeDesktop{started: []fakeProcess{tray}, dashboard: 102, madeAt: 1, showsAt: 1}
	cli := CLI{Commands: startingDocker{desktop: desktop}, Desktop: desktop.desktop(time.Minute), Out: io.Discard}
	began := time.Now()

	// Act
	err := cli.StartEngine(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("the start took %s, want it back long before its minute of grace", took)
	}
}

// The relief is the command cg-services-pd-build proved on 2026-10-11, run
// as root in Docker Desktop's own distribution, and a relief that fails says
// what wsl.exe said.
func TestCLIRelievesTheEnginesFileCache(t *testing.T) {
	cases := []struct {
		name    string
		answer  execx.Result
		wantErr string
	}{
		{"the cache is handed back", execx.Result{}, ""},
		{"wsl refuses", execx.Result{ExitCode: 1, Stderr: []byte("There is no distribution with the supplied name.")}, "no distribution"},
	}
	relief := "-d docker-desktop -u root -- sh -c sync && echo 3 > /proc/sys/vm/drop_caches"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			runner := &scriptedDocker{answers: map[string]execx.Result{relief: tc.answer}}

			// Act
			err := CLI{Commands: runner}.Relieve(context.Background())

			// Assert
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if len(runner.calls) != 1 || runner.calls[0].Name != "wsl.exe" || strings.Join(runner.calls[0].Args, " ") != relief || !runner.calls[0].KillTree {
				t.Fatalf("calls = %+v, want one wsl.exe %s that ends what it started when it is ended", runner.calls, relief)
			}
		})
	}
}

func TestCLIStopsTheEngineAndReadsWhatRuns(t *testing.T) {
	runner := &scriptedDocker{answers: map[string]execx.Result{"ps --format {{.Names}}": {Stdout: []byte("precisiondocs-dev-redis\n")}}}
	cli := CLI{Commands: runner}

	stopErr := cli.StopEngine(context.Background())
	containers, psErr := cli.RunningContainers(context.Background())

	if err := errorsOf(stopErr, psErr); err != "" {
		t.Fatal(err)
	}
	if got := strings.Join(runner.calls[0].Args, " "); got != "desktop stop --timeout 120" {
		t.Errorf("stop = %q", got)
	}
	if !slices.Equal(containers, []string{"precisiondocs-dev-redis"}) {
		t.Errorf("containers = %v", containers)
	}
}

func TestCLIReportsAFailedCallWithWhatDockerSaid(t *testing.T) {
	runner := &scriptedDocker{answers: map[string]execx.Result{
		"compose --file c.yml up --detach --wait --wait-timeout 900 backend": {ExitCode: 1, Stderr: []byte("container precisiondocs-dev-backend is unhealthy\n")},
	}}

	err := CLI{Commands: runner}.Up(context.Background(), Compose{Dir: `C:\p`, File: "c.yml"}, []string{"backend"})

	if err == nil || !strings.Contains(err.Error(), "exited 1") || !strings.Contains(err.Error(), "is unhealthy") {
		t.Fatalf("err = %v, want the exit and Docker's words", err)
	}
}

func TestCLIEngineRunningReadsAnEngineThatDoesNotAnswerAsStopped(t *testing.T) {
	runner := &scriptedDocker{answers: map[string]execx.Result{"info --format {{.ServerVersion}}": {ExitCode: 1}}}

	running, err := CLI{Commands: runner}.EngineRunning(context.Background())

	if err != nil || running {
		t.Fatalf("running = %v err = %v, want a stopped engine and no error", running, err)
	}
}

func errorsOf(errs ...error) string {
	var said []string
	for _, err := range errs {
		if err != nil {
			said = append(said, err.Error())
		}
	}
	return strings.Join(said, "\n")
}
