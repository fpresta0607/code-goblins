package standin

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Why a test never serves or runs a copy of its own test binary.
//
// Microsoft Defender judges a program by its bytes, and asks its cloud about
// one it has never met before it lets it start. A copy of a test binary is
// such a program at every build: on the Overlord's PC its first start was
// measured at 0.7 to 5 s on 2026-10-10, and at 39 s under load on 2026-10-07.
// Worse, the install tests served a copy of their test binary as the cfo.exe
// install.ps1 downloads, renames and runs, which is what a dropper does, and
// Defender's verdict on those bytes (Trojan:Win32/Bearfoos.B!ml, 22 times in
// three bursts between 2026-09-30 and 2026-10-07) then named every copy of
// them, the test binary itself included. So a test that needs some program to
// start uses the one stand-in (Put, Bytes): its bytes are the same at every
// run and are no test's own.

//go:embed program/main.go
var programSource []byte

// programModule is the go.mod the stand-in is built under: a module of its
// own, so the build reads nothing but the embedded source and the standard
// library, and gives the same bytes from any checkout.
const programModule = "module standin\n\ngo 1.22\n"

// buildFlags are the flags the stand-in is built with. -trimpath keeps the
// build folder out of the program, so two builds are the same bytes.
var buildFlags = []string{"-trimpath", "-buildvcs=false"}

// startEnvironment is the environment the test binary started with. The
// stand-in is found and built with it, since a test may have pointed
// LOCALAPPDATA or PATH elsewhere by the time it asks for the stand-in.
var startEnvironment = os.Environ()

// startCache is the user's cache folder as the test binary started.
var startCache, startCacheErr = os.UserCacheDir()

// executableSuffix ends a program's file name on this system.
func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// programKey names the folder of one stand-in build: the toolchain, the
// system and the source it was built from.
func programKey() string {
	sum := sha256.New()
	for _, part := range append([]string{runtime.Version(), runtime.GOOS, runtime.GOARCH, programModule, string(programSource)}, buildFlags...) {
		_, _ = io.WriteString(sum, part)
		_, _ = sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

var program = sync.OnceValues(func() (string, error) {
	if startCacheErr != nil {
		return "", startCacheErr
	}
	return ensure(filepath.Join(startCache, "cfo", "standin"))
})

// Program returns the stand-in program: internal/standin/program, built once
// for each Go toolchain into one folder of the user's cache (cfo\standin) and
// reused by every test run from every checkout. Do not write to it or start
// it where it is: Put it where the test needs a program.
func Program(t testing.TB) string {
	t.Helper()
	path, err := program()
	if err != nil {
		t.Fatalf("standin: %v", err)
	}
	return path
}

// Bytes returns the stand-in program's content, for a test that serves it as
// a download or packs it in an archive. Where the test itself writes the
// program to a path, use Put.
func Bytes(t testing.TB) []byte {
	t.Helper()
	content, err := fsx.ReadFile(Program(t))
	if err != nil {
		t.Fatalf("standin: %v", err)
	}
	return content
}

// Put writes the stand-in program to path, and fails the test for a path
// named after a Windows program.
func Put(t testing.TB, path string) {
	t.Helper()
	if err := place(Bytes(t), path); err != nil {
		t.Fatalf("standin: %v", err)
	}
}

// Rule is what the stand-in program does for one command it is given.
type Rule struct {
	// Args is the command: its arguments joined by single spaces.
	Args string `json:"args,omitempty"`
	// Prefix makes the rule match a command that goes on after Args too, as
	// install matches install --window-built.
	Prefix bool `json:"prefix,omitempty"`
	// Any makes the rule match every command.
	Any bool `json:"any,omitempty"`
	// Stdout and Stderr are written before the stand-in exits.
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	// Exit is the stand-in's exit code.
	Exit int `json:"exit,omitempty"`
	// Hold keeps the stand-in running until it is ended.
	Hold bool `json:"hold,omitempty"`
	// CopyTo are paths the stand-in writes itself to first, making their
	// folders, as an install puts its program in a home.
	CopyTo []string `json:"copy_to,omitempty"`
}

// Env is the environment that makes every stand-in program started under it
// append its name and arguments, as one line ending in CRLF, to record when
// record is not empty, and do what the first of rules matching its command
// says. A stand-in with no matching rule exits 0.
func Env(record string, rules ...Rule) []string {
	env := []string{}
	if record != "" {
		env = append(env, "CFO_STANDIN_RECORD="+record)
	}
	if len(rules) > 0 {
		// A Rule holds only strings, numbers and booleans, which always marshal.
		text, _ := json.Marshal(rules)
		env = append(env, "CFO_STANDIN_RULES="+string(text))
	}
	return env
}

// ensure returns the stand-in program under root, building it where it is
// missing or no longer the bytes its build recorded.
func ensure(root string) (string, error) {
	dir := filepath.Join(root, programKey())
	path := filepath.Join(dir, "standin"+executableSuffix())
	if isBuilt(path) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(dir, "build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	for name, content := range map[string][]byte{"go.mod": []byte(programModule), "main.go": programSource} {
		if err := os.WriteFile(filepath.Join(work, name), content, 0o644); err != nil {
			return "", err
		}
	}
	built := filepath.Join(work, filepath.Base(path))
	build := execx.Command(goTool(), append(append([]string{"build"}, buildFlags...), "-o", built, ".")...)
	build.Dir = work
	// The stand-in is built for this system by the toolchain that built the
	// test, whatever flags, workspace or cross-compilation the test run has.
	build.Env = append(append([]string{}, startEnvironment...), "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build the stand-in program: %w\n%s", err, output)
	}
	content, err := fsx.ReadFile(built)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	// The sum goes first: a program in place with no sum beside it is one
	// another test run is still putting there.
	if err := replace(filepath.Join(work, "sum"), sumPath(path), []byte(hex.EncodeToString(sum[:]))); err != nil {
		return "", err
	}
	if err := os.Rename(built, path); err != nil && !isBuilt(path) {
		return "", fmt.Errorf("put the stand-in program in place: %w", err)
	}
	return path, nil
}

// sumPath is where the SHA-256 of the stand-in at path is recorded.
func sumPath(path string) string {
	return path + ".sha256"
}

// isBuilt reports whether path holds the bytes its build recorded.
func isBuilt(path string) bool {
	recorded, err := fsx.ReadFile(sumPath(path))
	if err != nil {
		return false
	}
	content, err := fsx.ReadFile(path)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(content)
	return bytes.Equal(bytes.TrimSpace(recorded), []byte(hex.EncodeToString(sum[:])))
}

// replace writes content to path through temporary, so no reader sees part
// of it.
func replace(temporary, path string, content []byte) error {
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// goTool is the go command of the toolchain that runs this test: go test
// names its root in GOROOT, and PATH has it otherwise.
func goTool() string {
	for _, variable := range startEnvironment {
		if root, found := strings.CutPrefix(variable, "GOROOT="); found && root != "" {
			return filepath.Join(root, "bin", "go"+executableSuffix())
		}
	}
	return "go"
}

// place writes program to path, and refuses a path named after a Windows
// program.
func place(program []byte, path string) error {
	if IsWindowsProgram(filepath.Base(path)) {
		return fmt.Errorf("%s is the name of a Windows program, and a stand-in with one is what malware that hides as part of Windows looks like: Microsoft Defender detected a stand-in rundll32.exe as Behavior:Win32/DefenseEvasion.A!ml on 2026-09-26. Give the stand-in another name, and have the code under test take the program's path where it must", filepath.Base(path))
	}
	return os.WriteFile(path, program, 0o755)
}
