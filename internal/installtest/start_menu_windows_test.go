package installtest

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// startMenuInstall is one run of the install as far as it goes with a
// stand-in cfo.exe: every command it runs succeeds and records itself.
type startMenuInstall struct {
	output string
	// record holds, in order, each command the stand-in cfo.exe ran.
	record string
	// local is the session's LOCALAPPDATA, where the one-line install's home is.
	local string
	// programs is the session's Start-menu programs folder.
	programs string
}

// runInstallForStartMenu runs command, which runs install.ps1, in Windows
// PowerShell against the release served at base, with every other download
// failing, as offline, and no wait between attempts. seed runs first, with
// the session's LOCALAPPDATA and its Start-menu programs folder. Which program
// the Start menu entry starts is the script's own choice, the same whichever
// PowerShell runs it, so Windows PowerShell alone runs it; the install
// workflow runs the whole install in both PowerShells on a clean runner.
func runInstallForStartMenu(t *testing.T, base string, stubs map[string]string, command string, seed func(local, programs string)) startMenuInstall {
	t.Helper()
	record := filepath.Join(t.TempDir(), "record.txt")
	all := map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n"}
	for name, script := range stubs {
		all[name] = script
	}
	internet := "function Invoke-WebRequest {\n" +
		"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
		"  if ($Uri.StartsWith('" + base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  throw \"offline: $Uri\"\n" +
		"}\n" +
		"function Start-Sleep { param([int]$Seconds) }\n"
	cmd, local, _ := StrippedCommand(t, base, all, WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", internet+command)
	programs := ""
	for _, variable := range cmd.Env {
		if appData, found := strings.CutPrefix(variable, "APPDATA="); found {
			programs = filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
		}
	}
	cmd.Env = append(cmd.Env, standInVariable+"=1", standInRecordVariable+"="+record)
	if seed != nil {
		seed(local, programs)
	}

	output, _ := cmd.CombinedOutput()

	recorded, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return startMenuInstall{output: string(output), record: string(recorded), local: local, programs: programs}
}

// serveReleaseWithWindow serves a release holding binary as cfo.exe and, when
// window is not nil, window as the desktop window, each listed in SHA256SUMS.
func serveReleaseWithWindow(t *testing.T, binary, window []byte) string {
	t.Helper()
	files := map[string][]byte{"/cfo.exe": binary}
	sums := fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary))
	if window != nil {
		files["/goblins-window.exe"] = window
		sums += fmt.Sprintf("%x  goblins-window.exe\n", sha256.Sum256(window))
	}
	files["/SHA256SUMS"] = []byte(sums)
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(release.Close)
	return release.URL
}

// startMenuEntry is the program the Code Goblins entry in programs starts and
// the arguments it gives it, read from the shortcut as Windows reads it.
func startMenuEntry(t *testing.T, run startMenuInstall) (target, arguments string) {
	t.Helper()
	shortcut := filepath.Join(run.programs, "Code Goblins.lnk")
	if _, err := os.Stat(shortcut); err != nil {
		t.Fatalf("the install made no Start-menu entry: %v\n%s", err, run.output)
	}
	out, err := execx.Command(WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-Command",
		"$shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut('"+shortcut+"'); Write-Output \"target=$($shortcut.TargetPath)\"; Write-Output \"arguments=$($shortcut.Arguments)\"").CombinedOutput()
	if err != nil {
		t.Fatalf("read %s: %v\n%s", shortcut, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if value, found := strings.CutPrefix(line, "target="); found {
			target = value
		}
		if value, found := strings.CutPrefix(line, "arguments="); found {
			arguments = value
		}
	}
	return target, arguments
}

// -Dev builds the desktop window beside its build of cfo.exe, outside the
// clone, and cfo install puts it in the home's bin, so Code Goblins in the
// Start menu starts that window alone; the clone keeps what it held.
func TestDevStartsAloneTheWindowItBuilt(t *testing.T) {
	// Arrange: a folder the script takes for a clone, with the script in it,
	// and a go whose build of cfo is the stand-in and whose build of the
	// window is a file of its own.
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "cmd", "cfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	built, builtWindow := filepath.Join(t.TempDir(), "built"), filepath.Join(t.TempDir(), "built-window")
	for path, content := range map[string][]byte{
		filepath.Join(checkout, "AGENTS.md"):          nil,
		filepath.Join(checkout, "install.ps1"):        source,
		filepath.Join(checkout, "goblins-window.exe"): []byte("a window from before"),
		built:       standIn(t),
		builtWindow: []byte("the window this clone builds"),
	} {
		if err := os.WriteFile(path, content, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stubs := map[string]string{"go": "@if not \"%9\"==\"./cmd/goblins-window\" copy /y \"" + built + "\" \"%4\" >nul & exit /b\r\n" +
		"@copy /y \"" + builtWindow + "\" \"%4\" >nul\r\n"}

	// Act
	run := runInstallForStartMenu(t, ServeRelease(t, nil, ""), stubs, "& '"+filepath.Join(checkout, "install.ps1")+"' -Dev", nil)

	// Assert
	target, arguments := startMenuEntry(t, run)
	wantTarget := fsx.LongPath(filepath.Join(run.local, "CodeGoblins", "bin", "goblins-window.exe"))
	if !strings.EqualFold(fsx.LongPath(target), wantTarget) || arguments != "" {
		t.Errorf("Code Goblins in the Start menu runs %q with %q, want %q alone:\n%s", target, arguments, wantTarget, run.output)
	}
	if !strings.Contains(run.record, "cfo install\r\n") {
		t.Errorf("cfo install was not run:\n%s\n%s", run.record, run.output)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "goblins-window.exe")); err != nil || string(got) != "a window from before" {
		t.Errorf("goblins-window.exe in the clone = %q (%v), want the clone left as it was", got, err)
	}
}

// Code Goblins in the Start menu starts the desktop window alone only where
// this install delivered that window itself, from a release that carries it:
// such a window opens the app when started alone. The stand-in cfo.exe puts
// nothing in the home, so a delivered window is told by the download and never
// by a file the home holds. A window the home held before, which a release
// with none leaves there, may be from before a window could do that, so the
// entry keeps running goblins --window, which opens any window, and that
// window is left as it was. A home with no window keeps the quick start, and
// the entry of a window installed on its own stays.
func TestOneLineInstallStartsAloneOnlyTheWindowItDelivered(t *testing.T) {
	const kept, standalone = "a window from before", "shortcut to a window installed on its own"
	for name, test := range map[string]struct {
		// delivered is the desktop window the release carries, or nil.
		delivered []byte
		// held is the window the home holds before the install, or "".
		held string
		// program and arguments are what the entry starts.
		program, arguments string
	}{
		"a release that carries the window, into a home with none":       {[]byte("this release's window"), "", "goblins-window.exe", ""},
		"a release that carries the window, over a window the home held": {[]byte("this release's window"), kept, "goblins-window.exe", ""},
		"a release with no window, over a window the home held":          {nil, kept, "goblins.exe", "--window"},
		"a release with no window, into a home with none":                {nil, "", "goblins.exe", ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			base := serveReleaseWithWindow(t, standIn(t), test.delivered)
			noWindow := test.delivered == nil && test.held == ""
			seed := func(local, programs string) {
				if test.held != "" {
					if err := os.MkdirAll(filepath.Join(local, "CodeGoblins", "bin"), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(local, "CodeGoblins", "bin", "goblins-window.exe"), []byte(test.held), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if noWindow {
					if err := os.MkdirAll(programs, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(programs, "Code Goblins Window.lnk"), []byte(standalone), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}

			// Act
			run := runInstallForStartMenu(t, base, nil, "Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression", seed)

			// Assert
			bin := filepath.Join(run.local, "CodeGoblins", "bin")
			target, arguments := startMenuEntry(t, run)
			wantTarget := fsx.LongPath(filepath.Join(bin, test.program))
			if !strings.EqualFold(fsx.LongPath(target), wantTarget) || arguments != test.arguments {
				t.Errorf("Code Goblins in the Start menu runs %q with %q, want %q with %q:\n%s", target, arguments, wantTarget, test.arguments, run.output)
			}
			if !strings.Contains(run.record, "cfo install\r\n") || strings.Contains(run.record, "--window-built") {
				t.Errorf("want cfo install run as it is, told of no window built here:\n%s\n%s", run.record, run.output)
			}
			if test.held != "" {
				if got, err := os.ReadFile(filepath.Join(bin, "goblins-window.exe")); err != nil || string(got) != test.held {
					t.Errorf("goblins-window.exe in the home = %q (%v), want the script to leave it as %q", got, err, test.held)
				}
			}
			if noWindow {
				if got, err := os.ReadFile(filepath.Join(run.programs, "Code Goblins Window.lnk")); err != nil || string(got) != standalone {
					t.Errorf("the entry of a window installed on its own = %q (%v), want it left as it was", got, err)
				}
			}
		})
	}
}

func TestCoreOnlyReinstallKeepsTheStandaloneShortcut(t *testing.T) {
	// Arrange
	base := serveReleaseWithWindow(t, standIn(t), nil)
	var originalShortcut []byte
	seed := func(local, programs string) {
		for path, content := range map[string]string{
			filepath.Join(local, "CodeGoblins", "goblins-window.exe"):       "retained older window",
			filepath.Join(local, "CodeGoblinsWindow", "goblins-window.exe"): "standalone window",
			filepath.Join(local, "CodeGoblinsWindow", "goblins-window.png"): "standalone picture",
		} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(local, "CodeGoblins", "goblins.exe"), standIn(t), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(programs, 0o755); err != nil {
			t.Fatal(err)
		}
		shortcut := filepath.Join(programs, "Code Goblins Window.lnk")
		window := filepath.Join(local, "CodeGoblinsWindow", "goblins-window.exe")
		out, err := execx.Command(WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-Command",
			"$shortcut = (New-Object -ComObject WScript.Shell).CreateShortcut('"+strings.ReplaceAll(shortcut, "'", "''")+"'); $shortcut.TargetPath = '"+strings.ReplaceAll(window, "'", "''")+"'; $shortcut.Arguments = '--board http://127.0.0.1:4310 --state old-state'; $shortcut.Save()").CombinedOutput()
		if err != nil {
			t.Fatalf("create standalone shortcut: %v\n%s", err, out)
		}
		originalShortcut, err = os.ReadFile(shortcut)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Act
	run := runInstallForStartMenu(t, base, nil, "for ($install = 0; $install -lt 2; $install++) { Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression }", seed)

	// Assert
	target, arguments := startMenuEntry(t, run)
	wantTarget := fsx.LongPath(filepath.Join(run.local, "CodeGoblins", "goblins.exe"))
	if !strings.EqualFold(fsx.LongPath(target), wantTarget) || arguments != "--window" {
		t.Errorf("Code Goblins runs %q with %q, want %q with --window", target, arguments, wantTarget)
	}
	shortcut := filepath.Join(run.programs, "Code Goblins Window.lnk")
	if got, err := os.ReadFile(shortcut); err != nil || !bytes.Equal(got, originalShortcut) {
		t.Errorf("standalone shortcut changed or was removed: %v\n%s", err, run.output)
	}
	for path, want := range map[string]string{
		filepath.Join(run.local, "CodeGoblins", "goblins-window.exe"):       "retained older window",
		filepath.Join(run.local, "CodeGoblinsWindow", "goblins-window.exe"): "standalone window",
		filepath.Join(run.local, "CodeGoblinsWindow", "goblins-window.png"): "standalone picture",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want it kept as %q", path, got, err, want)
		}
	}
}
