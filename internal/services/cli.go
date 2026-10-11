package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Timeouts, in seconds, for the Docker Desktop and compose calls that wait:
// a cold engine start pulls nothing but can take minutes on a loaded
// machine, and a first stack start may build its images.
const (
	engineStartSeconds = "300"
	engineStopSeconds  = "120"
	stackWaitSeconds   = "900"
)

// CLI is Docker through the docker command: Docker Desktop's engine through
// its desktop plugin, and stacks through compose.
type CLI struct {
	Commands execx.Runner
	// Desktop is where an engine start finds the dashboard window Docker
	// Desktop opens, and Out takes what it says of it.
	Desktop Desktop
	Out     io.Writer
}

// reliefTimeout bounds one cache relief, which first writes out what the
// virtual machine has not written yet, slow in the middle of a build.
const reliefTimeout = 30 * time.Second

// Relieve asks the Linux kernel in Docker Desktop's virtual machine to write
// out its file cache and drop it, so the machine gets that memory back. A
// build fills the cache with every file it reads and writes, Windows counts
// all of it as used until the virtual machine hands it back, and the virtual
// machine hands it back slowly: on 2026-10-10 PrecisionDocs' build reached
// the floor in a burst of file copies alone. It runs as root in
// docker-desktop, Docker Desktop's own WSL distribution, which runs while
// the engine does.
func (c CLI) Relieve(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, reliefTimeout)
	defer cancel()
	request := execx.Request{Name: "wsl.exe", Args: []string{"-d", "docker-desktop", "-u", "root", "--", "sh", "-c", "sync && echo 3 > /proc/sys/vm/drop_caches"}, KillTree: true}
	result, err := c.Commands.Run(ctx, request)
	if err != nil {
		return fmt.Errorf("services: hand the engine's file cache back: %w", err)
	}
	if result.ExitCode != 0 {
		// wsl.exe writes its own messages in UTF-16.
		said := strings.ReplaceAll(string(result.Stdout)+string(result.Stderr), "\x00", "")
		return fmt.Errorf("services: hand the engine's file cache back: wsl.exe exited %d: %s", result.ExitCode, strings.TrimSpace(said))
	}
	return nil
}

// EngineRunning reports whether the engine answers docker info.
func (c CLI) EngineRunning(ctx context.Context) (bool, error) {
	result, err := c.Commands.Run(ctx, execx.Request{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}, KillTree: true})
	if err != nil {
		return false, fmt.Errorf("services: run docker info: %w", err)
	}
	return result.ExitCode == 0 && strings.TrimSpace(string(result.Stdout)) != "", nil
}

// StartEngine starts Docker Desktop and waits for its engine, and closes the
// dashboard window Docker Desktop opens as it starts (closeDashboard), which
// it looks for from before the start until it closed it, until the start's
// window process shows it has none to show, or until Desktop.Grace after
// the start returned. On 2026-10-11 the dashboard showed 3.3 s after the
// start began and 8 s before the engine answered, and a loaded machine can
// draw it later.
func (c CLI) StartEngine(ctx context.Context) error {
	began := time.Now()
	look, endLook := context.WithCancel(ctx)
	defer endLook()
	looked := make(chan dashboardLook, 1)
	go func() { looked <- c.Desktop.closeDashboard(look, began) }()

	_, err := c.run(ctx, "", "desktop", "start", "--timeout", engineStartSeconds)
	if err == nil {
		var running bool
		if running, err = c.EngineRunning(ctx); err == nil && !running {
			err = errors.New("services: Docker Desktop started but its engine does not answer docker info")
		}
	}

	grace := time.NewTimer(c.Desktop.Grace)
	defer grace.Stop()
	var result dashboardLook
	select {
	case result = <-looked:
	case <-grace.C:
		endLook()
		result = <-looked
	}
	switch {
	case result.err != nil:
		fmt.Fprintf(c.Out, "cfo services: could not look for the dashboard window Docker Desktop opens as it starts: %v\n", result.err)
	case result.isClosed:
		fmt.Fprintf(c.Out, "cfo services: closed the dashboard window Docker Desktop opened as its engine started (pid %d), and Docker Desktop runs on in the tray\n", result.pid)
	case result.pid != 0:
		fmt.Fprintf(c.Out, "cfo services: Docker Desktop's window process (pid %d) showed no dashboard while cfo looked, so cfo closed no window\n", result.pid)
	}
	return err
}

// StopEngine stops Docker Desktop.
func (c CLI) StopEngine(ctx context.Context) error {
	_, err := c.run(ctx, "", "desktop", "stop", "--timeout", engineStopSeconds)
	return err
}

// RunningContainers names every running container.
func (c CLI) RunningContainers(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// Running names the stack's running services.
func (c CLI) Running(ctx context.Context, compose Compose) ([]string, error) {
	out, err := c.run(ctx, compose.Dir, compose.args("ps", "--services", "--status", "running")...)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// Up starts services and waits for them.
func (c CLI) Up(ctx context.Context, compose Compose, services []string) error {
	args := compose.args("up", "--detach", "--wait", "--wait-timeout", stackWaitSeconds)
	_, err := c.run(ctx, compose.Dir, append(args, services...)...)
	return err
}

// Down stops and removes services, or the whole stack and its network when
// none are named. Named volumes are kept, so the next start keeps its data
// and caches.
func (c CLI) Down(ctx context.Context, compose Compose, services []string) error {
	_, err := c.run(ctx, compose.Dir, append(compose.args("down"), services...)...)
	return err
}

// args are the compose arguments naming the stack, then the command.
func (c Compose) args(command ...string) []string {
	args := []string{"compose", "--file", c.File}
	if c.EnvFile != "" {
		args = append(args, "--env-file", c.EnvFile)
	}
	return append(args, command...)
}

func (c CLI) run(ctx context.Context, dir string, args ...string) (string, error) {
	// A call that is ended, as the floor ends compose up's build, ends with
	// everything it started: docker runs compose and buildx as programs of
	// their own.
	result, err := c.Commands.Run(ctx, execx.Request{Dir: dir, Name: "docker", Args: args, KillTree: true})
	if err != nil {
		return "", fmt.Errorf("services: docker %s: %w", strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("services: docker %s exited %d: %s", strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return string(result.Stdout), nil
}

func lines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
