package codegoblins

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// oneLineShells are the PowerShells the one-line install must work in:
// Windows PowerShell 5.1, always present, and PowerShell 7 where installed.
func oneLineShells(t *testing.T) []string {
	t.Helper()
	shells := []string{filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")}
	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
		shells = append(shells, pwsh)
	}
	return shells
}

// runOneLineInstall runs install.ps1 the way the one-line command does, as
// text through Invoke-Expression, against the release served at base.
func runOneLineInstall(t *testing.T, shell, base string) (output, local, temp string, err error) {
	t.Helper()
	return runStrippedPowerShell(t, shell, base, "-Command", "Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression")
}

func installScript(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	return script
}

// runStrippedPowerShell runs shell with args against the release served at
// base, with stand-ins for git and gh, which the install otherwise stops for
// when winget is missing too.
func runStrippedPowerShell(t *testing.T, shell, base string, args ...string) (output, local, temp string, err error) {
	t.Helper()
	return runPowerShellWith(t, shell, base, []string{"git", "gh"}, args...)
}

// runPowerShellWith runs shell with args against the release served at base,
// with stand-ins for tools that do nothing and succeed.
func runPowerShellWith(t *testing.T, shell, base string, tools []string, args ...string) (output, local, temp string, err error) {
	t.Helper()
	stubs := map[string]string{}
	for _, tool := range tools {
		stubs[tool] = "@exit /b 0\r\n"
	}
	return runPowerShellWithStubs(t, shell, base, stubs, args...)
}

// runPowerShellWithStubs runs shell with args against the release served at
// base. The child gets folders of its own for every per-user location and a
// PATH with only Windows and the stand-ins stubs names on it, each a .cmd with
// the given text, so nothing it could reach installs onto this machine.
func runPowerShellWithStubs(t *testing.T, shell, base string, stubs map[string]string, args ...string) (output, local, temp string, err error) {
	t.Helper()
	local, temp, profile, bin := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for tool, script := range stubs {
		if err := os.WriteFile(filepath.Join(bin, tool+".cmd"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	system := os.Getenv("SystemRoot")
	cmd := exec.Command(shell, append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass"}, args...)...)
	cmd.Dir = temp
	cmd.Env = []string{
		"SystemRoot=" + system,
		"windir=" + system,
		"SystemDrive=" + os.Getenv("SystemDrive"),
		"ComSpec=" + os.Getenv("ComSpec"),
		"PATHEXT=" + os.Getenv("PATHEXT"),
		"ProgramFiles=" + os.Getenv("ProgramFiles"),
		"ProgramData=" + os.Getenv("ProgramData"),
		"PATH=" + bin + ";" + filepath.Join(system, "System32") + ";" + filepath.Join(system, "System32", "WindowsPowerShell", "v1.0"),
		"USERPROFILE=" + profile,
		"APPDATA=" + filepath.Join(profile, "Roaming"),
		"LOCALAPPDATA=" + local,
		"TEMP=" + temp,
		"TMP=" + temp,
		"CODE_GOBLINS_RELEASE_BASE=" + base,
	}
	out, err := cmd.CombinedOutput()
	return string(out), local, temp, err
}

// assertNothingInstalled checks that no CFO home was set up and that the
// download folder the install made is gone. PowerShell keeps caches of its own
// in these folders, so only the install's own names are looked for.
func assertNothingInstalled(t *testing.T, local, temp string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(local, "CodeGoblins")); !os.IsNotExist(err) {
		t.Errorf("a CFO home was set up in %s (%v), want none", local, err)
	}
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*")); len(left) != 0 {
		t.Errorf("the download folder %v was left behind", left)
	}
}

// serveRelease serves a release holding binary and sums, or nothing at all.
func serveRelease(t *testing.T, binary []byte, sums string) string {
	t.Helper()
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case binary != nil && r.URL.Path == "/cfo.exe":
			_, _ = w.Write(binary)
		case binary != nil && r.URL.Path == "/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(release.Close)
	return release.URL
}

// The one-line install refuses a download that does not match the release's
// SHA256SUMS before it runs anything: nothing is installed, and the download
// is gone.
func TestOneLineInstallRefusesADownloadThatDoesNotMatchTheReleaseChecksum(t *testing.T) {
	binary := []byte("a build the release did not publish")
	for _, shell := range oneLineShells(t) {
		for name, test := range map[string]struct {
			binary []byte
			sums   string
			want   string
		}{
			"another build's checksum": {binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256([]byte("the build the release published"))), "does not match the release's SHA256SUMS"},
			"no checksum for cfo.exe":  {binary, fmt.Sprintf("%x  other.exe\n", sha256.Sum256(binary)), "does not match the release's SHA256SUMS"},
			"no release at all":        {nil, "", "the release could not be downloaded"},
		} {
			t.Run(filepath.Base(shell)+" "+name, func(t *testing.T) {
				output, local, temp, err := runOneLineInstall(t, shell, serveRelease(t, test.binary, test.sums))

				if err == nil || !strings.Contains(output, test.want) {
					t.Fatalf("install = %v, want it refused with %q:\n%s", err, test.want, output)
				}
				assertNothingInstalled(t, local, temp)
			})
		}
	}
}

// The check lets a download that matches through, so the refusals above are
// the checksum's doing. The stand-in binary is not a program, so it goes no
// further than being run.
func TestOneLineInstallRunsADownloadThatMatchesTheReleaseChecksum(t *testing.T) {
	binary := []byte("not a program")
	sum := sha256.Sum256(binary)
	for _, shell := range oneLineShells(t) {
		for name, sums := range map[string]string{
			"as release.yml writes it": fmt.Sprintf("%x  cfo.exe\n", sum),
			"in binary mode":           fmt.Sprintf("%X *cfo.exe\n", sum),
		} {
			t.Run(filepath.Base(shell)+" "+name, func(t *testing.T) {
				output, local, temp, err := runOneLineInstall(t, shell, serveRelease(t, binary, sums))

				if !strings.Contains(output, "Verified cfo.exe against the release's SHA256SUMS") || strings.Contains(output, "does not match") {
					t.Fatalf("install = %v, want the download verified and run:\n%s", err, output)
				}
				if err == nil {
					t.Fatalf("install succeeded with a stand-in binary that cannot run:\n%s", output)
				}
				assertNothingInstalled(t, local, temp)
			})
		}
	}
}

// The one-line install runs in the caller's own session and leaves it exactly
// as it was, even when it is refused.
func TestOneLineInstallLeavesTheCallersSessionAsItWas(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			caller := "$InstallDir = 'mine'; $Dev = 'mine'; $ErrorActionPreference = 'SilentlyContinue'\n" +
				"try { Get-Content -Raw -LiteralPath '" + installScript(t) + "' | Invoke-Expression } catch { Write-Output \"refused: $($_.Exception.Message)\" }\n" +
				"Write-Output \"InstallDir=[$InstallDir] Dev=[$Dev] ErrorActionPreference=[$ErrorActionPreference]\""

			output, _, _, err := runStrippedPowerShell(t, shell, serveRelease(t, nil, ""), "-Command", caller)

			if err != nil || !strings.Contains(output, "refused: Code Goblins was not installed") {
				t.Fatalf("install = %v, want it refused and caught by the caller:\n%s", err, output)
			}
			if want := "InstallDir=[mine] Dev=[mine] ErrorActionPreference=[SilentlyContinue]"; !strings.Contains(output, want) {
				t.Fatalf("the caller's session changed, want %q:\n%s", want, output)
			}
		})
	}
}

// fakeCheckout is a folder the script takes for a clone of Code Goblins, with
// the script itself in it.
func fakeCheckout(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "cmd", "cfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"AGENTS.md": nil, "install.ps1": source} {
		if err := os.WriteFile(filepath.Join(checkout, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return checkout
}

// A clone installs only through -Dev: run without it, the script names the
// command and changes nothing, and -Dev anywhere but a clone is refused.
func TestACloneInstallsOnlyThroughDev(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		for name, test := range map[string]struct {
			folder func(t *testing.T) string
			args   []string
			want   string
		}{
			"a clone without -Dev": {fakeCheckout, nil, `run: .\install.cmd -Dev`},
			"-Dev outside a clone": {func(t *testing.T) string {
				folder := t.TempDir()
				source, err := os.ReadFile(installScript(t))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "install.ps1"), source, 0o644); err != nil {
					t.Fatal(err)
				}
				return folder
			}, []string{"-Dev"}, "-Dev builds Code Goblins from a clone"},
		} {
			t.Run(filepath.Base(shell)+" "+name, func(t *testing.T) {
				var requests atomic.Int32
				release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					http.NotFound(w, r)
				}))
				defer release.Close()
				folder := test.folder(t)

				output, _, _, err := runStrippedPowerShell(t, shell, release.URL, append([]string{"-File", filepath.Join(folder, "install.ps1")}, test.args...)...)

				if err == nil || !strings.Contains(output, test.want) {
					t.Fatalf("install = %v, want it refused with %q:\n%s", err, test.want, output)
				}
				if n := requests.Load(); n != 0 {
					t.Errorf("the refused install made %d download requests, want none", n)
				}
				if _, err := os.Stat(filepath.Join(folder, "cfo.exe")); !os.IsNotExist(err) {
					t.Errorf("cfo.exe was left in %s (%v), want none", folder, err)
				}
			})
		}
	}
}

// -Dev builds from source, so without Go it stops before building or changing
// anything and names the install.
func TestDevStopsForGoBeforeChangingAnything(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			checkout := fakeCheckout(t)

			output, _, _, err := runStrippedPowerShell(t, shell, serveRelease(t, nil, ""), "-File", filepath.Join(checkout, "install.ps1"), "-Dev")

			if err == nil || !strings.Contains(output, "winget install -e --id GoLang.Go") || !strings.Contains(output, "needs Go") {
				t.Fatalf("install = %v, want it stopped for Go with its install:\n%s", err, output)
			}
			if left, _ := filepath.Glob(filepath.Join(checkout, "*.exe*")); len(left) != 0 {
				t.Errorf("the stopped install left %v, want nothing built", left)
			}
		})
	}
}

// -Dev replaces a cfo.exe that is still running, as a supervisor or a CFO's
// terminal host keeps it on a working clone: the running copy moves aside,
// and cfo.exe and goblins.exe both become the new build.
func TestDevReplacesABuildThatIsStillRunning(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			checkout := fakeCheckout(t)
			newBuild := filepath.Join(t.TempDir(), "built")
			if err := os.WriteFile(newBuild, []byte("the build from this clone"), 0o644); err != nil {
				t.Fatal(err)
			}
			// A running cfo.exe: ping, copied under that name, runs long
			// enough and needs no console.
			running := filepath.Join(checkout, "cfo.exe")
			ping, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(running, ping, 0o755); err != nil {
				t.Fatal(err)
			}
			old := exec.Command(running, "-n", "120", "127.0.0.1")
			old.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
			if err := old.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = old.Process.Kill()
				_ = old.Wait()
			})
			// go build -o <path> ./cmd/cfo copies the new build to <path>.
			stubs := map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n", "go": "@copy /y \"" + newBuild + "\" \"%3\" >nul\r\n"}

			output, _, _, _ := runPowerShellWithStubs(t, shell, serveRelease(t, nil, ""), stubs, "-File", filepath.Join(checkout, "install.ps1"), "-Dev")

			for _, name := range []string{"cfo.exe", "goblins.exe"} {
				if built, err := os.ReadFile(filepath.Join(checkout, name)); err != nil || string(built) != "the build from this clone" {
					t.Errorf("%s = %q (%v), want the new build:\n%s", name, built, err, output)
				}
			}
			if old.ProcessState != nil {
				t.Errorf("the running cfo.exe was stopped, want it left running under its old name")
			}
		})
	}
}

// Without winget, an install that needs it for git or gh stops before it
// downloads or changes anything, and names the one fix.
func TestInstallStopsForWingetBeforeDownloadingAnything(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		for want, tools := range map[string][]string{"git and gh": nil, "gh": {"git"}} {
			t.Run(filepath.Base(shell)+" needing "+want, func(t *testing.T) {
				var requests atomic.Int32
				release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					http.NotFound(w, r)
				}))
				defer release.Close()

				output, local, temp, err := runPowerShellWith(t, shell, release.URL, tools, "-Command", "Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression")

				if err == nil || !strings.Contains(output, "https://apps.microsoft.com/detail/9NBLGGH4NNS1") || !strings.Contains(output, "winget is missing, and the install needs it for "+want+".") {
					t.Fatalf("install = %v, want it stopped for winget with the App Installer fix:\n%s", err, output)
				}
				if n := requests.Load(); n != 0 {
					t.Errorf("the install made %d download requests before stopping, want none", n)
				}
				assertNothingInstalled(t, local, temp)
			})
		}
	}
}
