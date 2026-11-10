package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDockerArgumentsMountTheCheckoutAndScratchAlone(t *testing.T) {
	// Arrange
	checkout, scratch := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(checkout, "frontend")

	// Act
	args, err := DockerArguments(Run{Task: "cg-x", Name: "cg-x-1", Image: "node:22", Checkout: checkout, Scratch: scratch, Cwd: sub, Command: []string{"npm", "test"}})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--rm", "--init", "--name", "cg-x-1", "--label", TaskLabel + "=cg-x", "--security-opt", "no-new-privileges",
		"--mount", "type=bind,source=" + checkout + ",target=/work",
		"--mount", "type=bind,source=" + filepath.Join(checkout, ".git") + ",target=/work/.git,readonly",
		"--mount", "type=bind,source=" + scratch + ",target=/scratch", "--env", "TMPDIR=/scratch",
		"--workdir", "/work/frontend", "node:22", "npm", "test"}
	if !slices.Equal(args, want) {
		t.Fatalf("DockerArguments =\n%q\nwant\n%q", args, want)
	}
}

func TestDockerArgumentsRefuseWhatDockerWouldMisread(t *testing.T) {
	checkout := t.TempDir()
	for _, test := range []struct {
		name    string
		run     Run
		refusal string
	}{
		{"no command", Run{Image: "node:22", Checkout: checkout}, "name the command"},
		{"an image that reads as a flag", Run{Image: "--privileged", Checkout: checkout, Command: []string{"true"}}, "not one image reference"},
		{"a folder outside the checkout", Run{Image: "node:22", Checkout: checkout, Cwd: t.TempDir(), Command: []string{"true"}}, "outside the checkout"},
		{"a checkout with a comma", Run{Image: "node:22", Checkout: checkout + ",target=/", Command: []string{"true"}}, "comma or a quote"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			_, err := DockerArguments(test.run)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("DockerArguments = %v, want a refusal saying %q", err, test.refusal)
			}
		})
	}
}

func TestShellCommandRunsAGatesCommandStringInTheContainersShell(t *testing.T) {
	for _, test := range []struct {
		command, want []string
	}{
		{[]string{`C:\WINDOWS\system32\cmd.exe`, "/c", "npm ci && npm test"}, []string{"sh", "-c", "npm ci && npm test"}},
		{[]string{"cmd", "/C", "go test ./..."}, []string{"sh", "-c", "go test ./..."}},
		{[]string{"npm", "test"}, []string{"npm", "test"}},
	} {
		// Act
		got := ShellCommand(test.command)

		// Assert
		if !slices.Equal(got, test.want) {
			t.Fatalf("ShellCommand(%q) = %q, want %q", test.command, got, test.want)
		}
	}
}
