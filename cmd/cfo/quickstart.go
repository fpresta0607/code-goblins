package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

func quickstartDetector() onboarding.Detector {
	directory := os.Getenv("PI_CODING_AGENT_DIR")
	if directory == "" {
		userHome, err := os.UserHomeDir()
		if err == nil {
			directory = filepath.Join(userHome, ".pi", "agent")
		}
	}
	return onboarding.Detector{LookPath: exec.LookPath, PiDirectory: directory, Probe: func(ctx context.Context, name string, args ...string) (execx.Result, error) {
		program, err := spawn.NativeProgram(name, args...)
		if err != nil {
			return execx.Result{}, err
		}
		return execx.OSRunner{}.Run(ctx, execx.Request{Name: program[0], Args: program[1:], KillTree: true})
	}}
}

func setupAgent(ctx context.Context, stateDir, chosen string, isRerun bool, stdout, stderr io.Writer) (string, error) {
	fmt.Fprintln(stdout, "Checking installed agents and sign-in status...")
	if chosen == "" {
		data, err := os.ReadFile(cfoHarnessPath(stateDir))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		chosen = strings.TrimSpace(string(data))
	}
	flow := onboarding.Flow{
		Detect: quickstartDetector().Detect,
		Choose: func(title string, choices []string, selected int) (int, error) {
			return onboarding.ChooseConsole(stdout, title, choices, selected)
		},
		Install: func(name string) error {
			packageName, err := onboarding.Package(name)
			if err != nil {
				return err
			}
			if _, err := exec.LookPath("npm.cmd"); err != nil {
				fmt.Fprintln(stderr, "Install Node.js LTS first, then run goblins setup again: winget install OpenJS.NodeJS.LTS")
				return err
			}
			fmt.Fprintf(stdout, "\nInstalling %s with npm. Waiting for the installer to finish...\n", packageName)
			return runSetupProgram(stdout, stderr, "npm.cmd", "install", "-g", packageName)
		},
		Login: func(name string) error {
			fmt.Fprintln(stdout, "\nComplete sign-in in the agent's own login. Code Goblins never asks for your password.")
			switch name {
			case "claude":
				return runSetupProgram(stdout, stderr, name, "auth", "login")
			case "codex":
				return runSetupProgram(stdout, stderr, name, "login")
			case "pi":
				fmt.Fprintln(stdout, "In pi, use /login to sign in and /model to choose your default provider. Use /quit to return here for verification.")
				return runSetupProgram(stdout, stderr, name)
			default:
				return fmt.Errorf("unknown agent %q", name)
			}
		},
		Save: func(name string) error { return fsx.AtomicWriteFile(cfoHarnessPath(stateDir), []byte(name+"\n")) },
	}
	return flow.Run(ctx, chosen, isRerun)
}

func runSetupProgram(stdout, stderr io.Writer, name string, args ...string) error {
	program, err := spawn.NativeProgram(name, args...)
	if err != nil {
		return err
	}
	command := exec.Command(program[0], program[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, stdout, stderr
	return command.Run()
}

func printAgentInstallers(stdout io.Writer) int {
	installers := make(map[string]string, len(onboarding.Agents))
	for _, name := range onboarding.Agents {
		packageName, err := onboarding.Package(name)
		if err != nil {
			return 1
		}
		installers[name] = "npm.cmd install -g " + packageName
	}
	if json.NewEncoder(stdout).Encode(installers) != nil {
		return 1
	}
	return 0
}
