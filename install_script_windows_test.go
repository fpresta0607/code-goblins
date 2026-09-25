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
// base. The child gets folders of its own for every per-user location and a
// PATH with only Windows on it, so nothing it could reach installs onto this
// machine.
func runStrippedPowerShell(t *testing.T, shell, base string, args ...string) (output, local, temp string, err error) {
	t.Helper()
	local, temp, profile := t.TempDir(), t.TempDir(), t.TempDir()
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
		"PATH=" + filepath.Join(system, "System32") + ";" + filepath.Join(system, "System32", "WindowsPowerShell", "v1.0"),
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
			caller := "$InstallDir = 'mine'; $Bootstrap = 'mine'; $ErrorActionPreference = 'SilentlyContinue'\n" +
				"try { Get-Content -Raw -LiteralPath '" + installScript(t) + "' | Invoke-Expression } catch { Write-Output \"refused: $($_.Exception.Message)\" }\n" +
				"Write-Output \"InstallDir=[$InstallDir] Bootstrap=[$Bootstrap] ErrorActionPreference=[$ErrorActionPreference]\""

			output, _, _, err := runStrippedPowerShell(t, shell, serveRelease(t, nil, ""), "-Command", caller)

			if err != nil || !strings.Contains(output, "refused: Code Goblins was not installed") {
				t.Fatalf("install = %v, want it refused and caught by the caller:\n%s", err, output)
			}
			if want := "InstallDir=[mine] Bootstrap=[mine] ErrorActionPreference=[SilentlyContinue]"; !strings.Contains(output, want) {
				t.Fatalf("the caller's session changed, want %q:\n%s", want, output)
			}
		})
	}
}

// Run as a file from a checkout, the script still takes -InstallDir and puts
// the verified download there as cfo.exe.
func TestCloneInstallPutsTheVerifiedDownloadInTheInstallDir(t *testing.T) {
	binary := []byte("not a program")
	sums := fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary))
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			checkout, installDir := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(checkout, "cmd", "cfo"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string][]byte{"AGENTS.md": nil, "install.ps1": source} {
				if err := os.WriteFile(filepath.Join(checkout, name), content, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			output, _, _, _ := runStrippedPowerShell(t, shell, serveRelease(t, binary, sums), "-File", filepath.Join(checkout, "install.ps1"), "-InstallDir", installDir)

			installed, err := os.ReadFile(filepath.Join(installDir, "cfo.exe"))
			if err != nil || string(installed) != string(binary) {
				t.Fatalf("cfo.exe in %s = %q (%v), want the verified download:\n%s", installDir, installed, err, output)
			}
		})
	}
}

// Run from a checkout, a download that does not match the release's checksum
// is refused outright: the install stops there instead of falling back to a
// source build, and leaves no cfo.exe behind.
func TestCloneInstallRefusesAMismatchedDownloadInsteadOfBuilding(t *testing.T) {
	sums := fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256([]byte("the build the release published")))
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			checkout, installDir := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(checkout, "cmd", "cfo"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string][]byte{"AGENTS.md": nil, "install.ps1": source} {
				if err := os.WriteFile(filepath.Join(checkout, name), content, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			output, _, _, err := runStrippedPowerShell(t, shell, serveRelease(t, []byte("a build the release did not publish"), sums), "-File", filepath.Join(checkout, "install.ps1"), "-InstallDir", installDir)

			if err == nil || !strings.Contains(output, "does not match the release's SHA256SUMS") || strings.Contains(output, "Building from source") {
				t.Fatalf("install = %v, want it refused without a source build:\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(installDir, "cfo.exe")); !os.IsNotExist(err) {
				t.Fatalf("cfo.exe was left in %s (%v), want none", installDir, err)
			}
		})
	}
}
