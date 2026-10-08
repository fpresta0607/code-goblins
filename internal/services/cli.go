package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
}

// EngineRunning reports whether the engine answers docker info.
func (c CLI) EngineRunning(ctx context.Context) (bool, error) {
	result, err := c.Commands.Run(ctx, execx.Request{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}})
	if err != nil {
		return false, fmt.Errorf("services: run docker info: %w", err)
	}
	return result.ExitCode == 0 && strings.TrimSpace(string(result.Stdout)) != "", nil
}

// StartEngine starts Docker Desktop and waits for its engine.
func (c CLI) StartEngine(ctx context.Context) error {
	if _, err := c.run(ctx, "", "desktop", "start", "--timeout", engineStartSeconds); err != nil {
		return err
	}
	running, err := c.EngineRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		return errors.New("services: Docker Desktop started but its engine does not answer docker info")
	}
	return nil
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
	result, err := c.Commands.Run(ctx, execx.Request{Dir: dir, Name: "docker", Args: args})
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
