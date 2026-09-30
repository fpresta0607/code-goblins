package codegoblins

import (
	"crypto/sha256"
	"errors"
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

// standInVariable makes a copy of this test binary stand in for a program,
// such as cfo.exe or no-mistakes.exe, whose every command succeeds.
const standInVariable = "CODE_GOBLINS_TEST_STAND_IN"

// standInRecordVariable names a file a stand-in appends each command it runs
// to, as its program's name and arguments on a line.
const standInRecordVariable = "CODE_GOBLINS_TEST_STAND_IN_RECORD"

// userEnvFileName is the file in a stripped session's LOCALAPPDATA that
// stands in for the user-scope environment, so no install a test runs writes
// this machine's own.
const userEnvFileName = "user-env.json"

func TestMain(m *testing.M) {
	if os.Getenv(standInVariable) != "" {
		if record := os.Getenv(standInRecordVariable); record != "" {
			program := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
			file, err := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				os.Exit(1)
			}
			_, _ = fmt.Fprintf(file, "%s\r\n", strings.Join(append([]string{program}, os.Args[1:]...), " "))
			_ = file.Close()
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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
// base, in the stripped environment strippedCommand gives it.
func runPowerShellWithStubs(t *testing.T, shell, base string, stubs map[string]string, args ...string) (output, local, temp string, err error) {
	t.Helper()
	cmd, local, temp := strippedCommand(t, base, stubs, shell, append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass"}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), local, temp, err
}

// strippedCommand is name with args against the release served at base. It
// gets folders of its own for every per-user location, a file standing in for
// the user-scope environment, and a PATH with only Windows and the stand-ins
// stubs names on it, each a .cmd with the given text, so nothing it could
// reach installs onto this machine.
func strippedCommand(t *testing.T, base string, stubs map[string]string, name string, args ...string) (cmd *exec.Cmd, local, temp string) {
	t.Helper()
	local, temp, profile, bin := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for tool, script := range stubs {
		if err := os.WriteFile(filepath.Join(bin, tool+".cmd"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	system := os.Getenv("SystemRoot")
	cmd = exec.Command(name, args...)
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
		"CFO_USER_ENV_FILE=" + filepath.Join(local, userEnvFileName),
	}
	return cmd, local, temp
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

// runPin runs tools/pin-installer.ps1 in shell, as release.yml does, and
// returns where it was told to write the release's install script.
func runPin(t *testing.T, shell, repository, tag, publisher string) (destination, output string, err error) {
	t.Helper()
	pin, err := filepath.Abs(filepath.Join("tools", "pin-installer.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	destination = filepath.Join(t.TempDir(), "release", "install.ps1")
	out, err := exec.Command(shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", pin, "-Repository", repository, "-Tag", tag, "-Publisher", publisher, "-Destination", destination).CombinedOutput()
	return destination, string(out), err
}

// The install script a release publishes downloads that release's own files:
// from the repository that published it, a fork's included, at its tag,
// never from the latest release or from another repository.
func TestAPublishedInstallDownloadsFromItsOwnRelease(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		for _, repository := range []string{"fpresta0607/code-goblins", "fpresta0607/code-goblins-native"} {
			t.Run(filepath.Base(shell)+" "+repository, func(t *testing.T) {
				// Arrange
				script, output, err := runPin(t, shell, repository, "v1.2.3", "Code Goblins Test Publisher")
				if err != nil {
					t.Fatalf("pin-installer.ps1 = %v:\n%s", err, output)
				}
				// The stand-in for Invoke-WebRequest says what the script
				// downloads and reaches nothing.
				offline := "function Invoke-WebRequest([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing) { Write-Host ('GET ' + $Uri); throw 'offline' }; "

				// Act
				output, local, temp, err := runStrippedPowerShell(t, shell, "", "-Command", offline+"Get-Content -Raw -LiteralPath '"+script+"' | Invoke-Expression")

				// Assert
				want := "GET https://github.com/" + repository + "/releases/download/v1.2.3/cfo.exe"
				if err == nil || !strings.Contains(output, want) {
					t.Fatalf("install = %v, want it to download from %s:\n%s", err, want, output)
				}
				assertNothingInstalled(t, local, temp)
			})
		}
	}
}

// The pin writes no install script it cannot pin as given, so a release
// never publishes one that downloads or trusts something else.
func TestThePinRefusesAValueItCannotWriteAsItIs(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		for name, test := range map[string]struct{ repository, tag, publisher string }{
			"a repository that is not owner/name": {"https://github.com/fpresta0607/code-goblins", "v1.2.3", "Code Goblins Test Publisher"},
			"a tag that is not a release's":       {"fpresta0607/code-goblins", "main", "Code Goblins Test Publisher"},
			"a publisher PowerShell would expand": {"fpresta0607/code-goblins", "v1.2.3", "Goblins $env:USERNAME"},
			"a publisher outside ASCII":           {"fpresta0607/code-goblins", "v1.2.3", "Caf\u00e9 Goblins"},
		} {
			t.Run(filepath.Base(shell)+" "+name, func(t *testing.T) {
				// Act
				script, output, err := runPin(t, shell, test.repository, test.tag, test.publisher)

				// Assert
				if err == nil {
					t.Fatalf("pin-installer.ps1 accepted %+v:\n%s", test, output)
				}
				if _, statErr := os.Stat(script); !os.IsNotExist(statErr) {
					t.Errorf("pin-installer.ps1 wrote %s (%v), want nothing written", script, statErr)
				}
			})
		}
	}
}

// The install script a release publishes names the release's publisher, and
// it refuses a download that is not validly signed by that publisher, however
// well it matches its sum.
func TestAPublishedInstallRefusesADownloadItsPublisherDidNotSign(t *testing.T) {
	binary := []byte("a build nobody signed")
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			// Arrange
			script, output, err := runPin(t, shell, "fpresta0607/code-goblins", "v1.2.3", "Code Goblins Test Publisher")
			if err != nil {
				t.Fatalf("pin-installer.ps1 = %v:\n%s", err, output)
			}
			base := serveRelease(t, binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary)))

			// Act
			output, local, temp, err := runStrippedPowerShell(t, shell, base, "-Command", "Get-Content -Raw -LiteralPath '"+script+"' | Invoke-Expression")

			// Assert
			if err == nil || !strings.Contains(output, "The downloaded cfo.exe is not validly signed by Code Goblins Test Publisher") {
				t.Fatalf("install = %v, want the unsigned download refused:\n%s", err, output)
			}
			assertNothingInstalled(t, local, temp)
		})
	}
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
// further than being run. The repository's own script names no publisher,
// and says it checks the sums only.
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
				if !strings.Contains(output, "names no publisher, so the download is checked against the release's SHA256SUMS only") {
					t.Errorf("an unpinned script does not say it checks sums only:\n%s", output)
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

// The one-line install saves each official installer it needs to a file and
// starts a child shell on that file with -File. A download-and-run one-liner
// (irm <url> | iex) on the child's command line is what Defender's
// command-line model blocks as Trojan:Win32/Commando.A!ml. The internet and
// the child shell are stand-ins: a download from the internet is recorded
// and answered with a script naming its URL, and the child records how it
// was started and the file it was given.
func TestOneLineInstallStartsOfficialInstallersFromAFile(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	base := serveRelease(t, binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary)))
	installers := []string{
		"https://claude.ai/install.ps1",
		"https://herdr.dev/install.ps1",
	}
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "record.txt")
			stubs := map[string]string{
				"git":        "@exit /b 0\r\n",
				"gh":         "@exit /b 0\r\n",
				"powershell": "@echo child %*>>\"" + record + "\"\r\n@if exist \"%~5\" type \"%~5\">>\"" + record + "\"\r\n@exit /b 1\r\n",
			}
			internet := "function Invoke-WebRequest {\n" +
				"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
				"  if ($Uri.StartsWith('" + base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
				"  Add-Content -LiteralPath '" + record + "' -Value \"download $Uri\"\n" +
				"  Set-Content -LiteralPath $OutFile -Value \"# installer from $Uri\"\n" +
				"}\n"
			cmd, _, temp := strippedCommand(t, base, stubs, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
				internet+"Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression")
			cmd.Env = append(cmd.Env, standInVariable+"=1")

			output, _ := cmd.CombinedOutput()

			recorded, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("no installer ran: %v\n%s", err, output)
			}
			lines := strings.Split(strings.TrimSpace(string(recorded)), "\r\n")
			started := 0
			for _, line := range lines {
				if !strings.HasPrefix(line, "child ") {
					continue
				}
				started++
				if !strings.Contains(line, " -File ") || strings.Contains(line, "-Command") || strings.Contains(line, "| iex") {
					t.Errorf("a child shell was started as %q, want it given a file with -File:\n%s", line, recorded)
				}
			}
			for _, url := range installers {
				if !strings.Contains(string(recorded), "download "+url+"\r\n") || !strings.Contains(string(recorded), "# installer from "+url) {
					t.Errorf("%s was not downloaded and handed to a child shell:\n%s\n%s", url, recorded, output)
				}
			}
			if started != len(installers) {
				t.Errorf("%d child shells started, want %d:\n%s", started, len(installers), recorded)
			}
			if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*")); len(left) != 0 {
				t.Errorf("the install left %v behind", left)
			}
			if strings.Contains(string(output), "Refreshing PATH so newly installed tools are visible") {
				t.Errorf("the install took the machine's PATH into the stripped session:\n%s", output)
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

// install.cmd runs install.ps1 under an execution policy that refuses to run
// the script itself, and hands back its exit code.
func TestInstallCmdRunsTheScriptWhateverTheExecutionPolicy(t *testing.T) {
	checkout := fakeCheckout(t)
	wrapper, err := os.ReadFile("install.cmd")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "install.cmd"), wrapper, 0o644); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n"}
	system := os.Getenv("SystemRoot")
	powershell := filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")

	// The premise: this policy refuses install.ps1 run directly.
	direct, _, _ := strippedCommand(t, serveRelease(t, nil, ""), stubs, powershell, "-NoProfile", "-NonInteractive", "-File", filepath.Join(checkout, "install.ps1"), "-Dev")
	direct.Env = append(direct.Env, "PSExecutionPolicyPreference=Restricted")
	if out, err := direct.CombinedOutput(); err == nil || strings.Contains(string(out), "needs Go") {
		t.Fatalf("install.ps1 run directly = %v, want the Restricted policy to refuse it:\n%s", err, out)
	}

	wrapped, _, _ := strippedCommand(t, serveRelease(t, nil, ""), stubs, filepath.Join(system, "System32", "cmd.exe"), "/d", "/c", filepath.Join(checkout, "install.cmd"), "-Dev")
	wrapped.Env = append(wrapped.Env, "PSExecutionPolicyPreference=Restricted")
	out, err := wrapped.CombinedOutput()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(out), "needs Go") {
		t.Fatalf("install.cmd -Dev = %v, want install.ps1 run to its stop for Go, exit code 1:\n%s", err, out)
	}
}

// install.cmd started from PowerShell 7, as from a pwsh terminal, hands
// Windows PowerShell a PSModulePath that lists PowerShell 7's own modules
// first, which Windows PowerShell cannot load, so an installer it runs, such
// as no-mistakes' own, lost New-TemporaryFile. install.cmd gives Windows
// PowerShell its own module path.
func TestInstallCmdGivesWindowsPowerShellItsOwnModules(t *testing.T) {
	checkout := t.TempDir()
	wrapper, err := os.ReadFile("install.cmd")
	if err != nil {
		t.Fatal(err)
	}
	// A stand-in install.ps1 that runs a nested Windows PowerShell, as the
	// install does for a tool's own installer.
	probe := "& powershell -NoProfile -Command { $ErrorActionPreference = 'Stop'; New-TemporaryFile | Remove-Item; 'nested ok' }\r\nexit $LASTEXITCODE\r\n"
	for name, content := range map[string]string{"install.cmd": string(wrapper), "install.ps1": probe} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// PowerShell 7's Microsoft.PowerShell.Utility, which only PowerShell 7
	// can load, first on the inherited module path.
	modules := t.TempDir()
	utility := filepath.Join(modules, "Microsoft.PowerShell.Utility")
	if err := os.MkdirAll(utility, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "@{\r\n GUID = '1da87e53-152b-403e-98dc-74d7b4d63d59'\r\n ModuleVersion = '7.0.0.0'\r\n CompatiblePSEditions = @('Core')\r\n PowerShellVersion = '7.0'\r\n CmdletsToExport = @('New-TemporaryFile')\r\n}\r\n"
	if err := os.WriteFile(filepath.Join(utility, "Microsoft.PowerShell.Utility.psd1"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	system := os.Getenv("SystemRoot")
	inherited := "PSModulePath=" + modules + ";" + filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "Modules")
	run := func(name string, args ...string) (string, error) {
		cmd, _, _ := strippedCommand(t, serveRelease(t, nil, ""), nil, name, args...)
		cmd.Env = append(cmd.Env, inherited)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// The premise: that module path breaks the nested New-TemporaryFile.
	if out, err := run(filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(checkout, "install.ps1")); err == nil || strings.Contains(out, "nested ok") {
		t.Fatalf("install.ps1 run directly = %v, want the inherited module path to break New-TemporaryFile:\n%s", err, out)
	}

	out, err := run(filepath.Join(system, "System32", "cmd.exe"), "/d", "/c", filepath.Join(checkout, "install.cmd"))

	if err != nil || !strings.Contains(out, "nested ok") {
		t.Fatalf("install.cmd = %v, want the nested Windows PowerShell to run New-TemporaryFile:\n%s", err, out)
	}
}

// -Dev replaces a cfo.exe that is still running, as a supervisor or a CFO's
// terminal host keeps it on a working clone: the running copy moves aside,
// and cfo.exe and goblins.exe both become the new build. A rerun after the
// next pull replaces them again while that copy still runs, and removes
// every old copy nothing runs.
func TestDevReplacesABuildThatIsStillRunning(t *testing.T) {
	for _, shell := range oneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			checkout := fakeCheckout(t)
			newBuild := filepath.Join(t.TempDir(), "built")
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
			exited := make(chan struct{})
			go func() {
				_ = old.Wait()
				close(exited)
			}()
			t.Cleanup(func() {
				_ = old.Process.Kill()
				<-exited
			})
			// go build -trimpath -o <path> ./cmd/cfo copies the new build to
			// <path>; a build that would keep this machine's folders in the
			// binary fails.
			stubs := map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n", "go": "@if not \"%2\"==\"-trimpath\" exit /b 1\r\n@copy /y \"" + newBuild + "\" \"%4\" >nul\r\n"}

			for _, build := range []string{"the build from this clone", "the build after the next pull"} {
				if err := os.WriteFile(newBuild, []byte(build), 0o644); err != nil {
					t.Fatal(err)
				}

				output, _, _, _ := runPowerShellWithStubs(t, shell, serveRelease(t, nil, ""), stubs, "-File", filepath.Join(checkout, "install.ps1"), "-Dev")

				if !strings.Contains(output, "Built cfo.exe and goblins.exe") {
					t.Fatalf("install -Dev did not replace the build with %q:\n%s", build, output)
				}
				for _, name := range []string{"cfo.exe", "goblins.exe"} {
					if built, err := os.ReadFile(filepath.Join(checkout, name)); err != nil || string(built) != build {
						t.Errorf("%s = %q (%v), want %q:\n%s", name, built, err, build, output)
					}
				}
			}
			select {
			case <-exited:
				t.Errorf("the running cfo.exe was stopped, want it left running under its old name")
			default:
			}
			left, err := filepath.Glob(filepath.Join(checkout, "*.exe.*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 1 {
				t.Fatalf("copies left beside the build = %v, want only the one still running", left)
			}
			if kept, err := os.ReadFile(left[0]); err != nil || string(kept) != string(ping) {
				t.Errorf("%s (%v) is not the copy still running", left[0], err)
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
