//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// putScriptShim puts a script named name in dir that does nothing, as the
// shims npm installs are found on PATH.
func putScriptShim(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// shellCommand is a command that runs a line in this system's shell:
// unixLine in sh here.
func shellCommand(_, unixLine string) []string {
	return []string{"sh", "-c", unixLine}
}

// venvPython is the interpreter of the Python environment at environment.
func venvPython(environment string) string {
	return filepath.Join(environment, "bin", "python")
}

// lavishAxiOnPath puts a stand-in lavish-axi alone on PATH, beside the
// system's own programs. It waits delaySeconds, writes its arguments to
// argsFile when one is named, and prints output, one line each.
func lavishAxiOnPath(t *testing.T, argsFile string, delaySeconds int, output ...string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if delaySeconds > 0 {
		script += "sleep " + strconv.Itoa(delaySeconds) + "\n"
	}
	if argsFile != "" {
		script += "echo \"$*\" > '" + argsFile + "'\n"
	}
	script += "cat <<'END'\n" + strings.Join(output, "\n") + "\nEND\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
}
