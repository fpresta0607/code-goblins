package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// putScriptShim puts a script named name in dir that does nothing, as the
// .cmd shims npm installs on Windows are found on PATH.
func putScriptShim(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".cmd"), []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// shellCommand is a command that runs a line in this system's shell:
// windowsLine in cmd here.
func shellCommand(windowsLine, _ string) []string {
	return []string{"cmd", "/d", "/c", windowsLine}
}

// venvPython is the interpreter of the Python environment at environment.
func venvPython(environment string) string {
	return filepath.Join(environment, "Scripts", "python.exe")
}

// lavishAxiOnPath puts a stand-in lavish-axi alone on PATH, beside the
// system's own programs. It waits delaySeconds, writes its arguments to
// argsFile when one is named, and prints output, one line each.
func lavishAxiOnPath(t *testing.T, argsFile string, delaySeconds int, output ...string) {
	t.Helper()
	bin := t.TempDir()
	script := "@echo off\r\n"
	if delaySeconds > 0 {
		script += "ping -n " + strconv.Itoa(delaySeconds+1) + " 127.0.0.1 >nul\r\n"
	}
	if argsFile != "" {
		script += "echo %* > \"" + argsFile + "\"\r\n"
	}
	for _, line := range output {
		script += "echo " + line + "\r\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
}
