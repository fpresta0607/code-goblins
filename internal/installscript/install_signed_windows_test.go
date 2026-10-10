package installscript

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// signatureBlock is the shape of what signing appends to a script: a block
// of comment lines holding the signature in base64. It is no signature.
const signatureBlock = "\r\n# SIG # Begin signature block\r\n" +
	"# MIIFgwYJKoZIhvcNAQcCoIIFdDCCBXACAQExCzAJBgUrDgMCGgUAMGkGCisGAQQB\r\n" +
	"# gjcCAQSgWzBZMDQGCisGAQQBgjcCAR4wJgIDAQAABBAfzDtgWUsITrck0sYpfvNR\r\n" +
	"# AgEAAgEAAgEAAgEAAgEAMCEwCQYFKw4DAhoFAAQUCodeGoblinsTestBlockOnly\r\n" +
	"# SIG # End signature block\r\n"

// releaseStep returns the script of the release workflow's step named name,
// and the text of the workflow from that step on.
func releaseStep(t *testing.T, name string) (script, rest string) {
	t.Helper()
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(strings.ReplaceAll(string(workflow), "\r\n", "\n"), "      - name: "+name+"\n")
	if !found {
		t.Fatalf("release has no step named %q", name)
	}
	step, _, _ := strings.Cut(rest, "\n      - ")
	_, body, found := strings.Cut(step, "        run: |\n")
	if !found {
		return "", rest
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		code, found := strings.CutPrefix(line, "          ")
		if !found && strings.TrimSpace(line) != "" {
			break
		}
		lines = append(lines, code)
	}
	return strings.Join(lines, "\n"), rest
}

// A signed release signs the install script it publishes with the
// certificate that signs its programs, once the script is pinned, since
// pinning rewrites it, and checks that signature before it publishes. Microsoft
// Defender uploaded every unsigned install.ps1 the Overlord's PC met to
// Microsoft, 12 by 2026-10-10, and none of the signed programs beside them.
func TestTheReleaseSignsTheInstallScriptItPublishes(t *testing.T) {
	// Arrange
	_, afterPin := releaseStep(t, "Pin the installer to this release")
	_, afterSign := releaseStep(t, "Sign the installer")
	verify, afterVerify := releaseStep(t, "Verify the installer's signature")
	sign, _, _ := strings.Cut(afterSign, "\n      - ")

	// Assert
	if !strings.Contains(afterPin, "      - name: Sign the installer\n") || !strings.Contains(afterSign, "      - name: Verify the installer's signature\n") || !strings.Contains(afterVerify, "      - name: Publish release\n") {
		t.Fatal("want the installer pinned, then signed, then its signature verified, then published")
	}
	for _, want := range []string{"if: steps.signing.outputs.signed == 'true'", "uses: azure/artifact-signing-action@v2", `${{ github.workspace }}\release\install.ps1`, "timestamp-rfc3161:"} {
		if !strings.Contains(sign, want) {
			t.Errorf("the signing step lacks %q:\n%s", want, sign)
		}
	}
	verifyStep, _, _ := strings.Cut(afterVerify, "\n      - ")
	if !strings.Contains(verifyStep, "if: steps.signing.outputs.signed == 'true'") {
		t.Errorf("the signature is verified on an unsigned release too:\n%s", verifyStep)
	}

	t.Run("an install script nobody signed stops the release", func(t *testing.T) {
		// Arrange
		folder := t.TempDir()
		pinned, output, err := runPin(t, installtest.WindowsPowerShell(), "fpresta0607/code-goblins", "v1.2.3", "Code Goblins Test Publisher")
		if err != nil {
			t.Fatalf("pin-installer.ps1 = %v:\n%s", err, output)
		}
		script, err := os.ReadFile(pinned)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(folder, "release"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "release", "install.ps1"), script, 0o644); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference = 'Stop'\n"+verify)
		command.Dir = folder
		command.Env = append(os.Environ(), "SIGNING_PUBLISHER=Code Goblins Test Publisher")

		// Act
		out, err := command.CombinedOutput()

		// Assert
		if err == nil || !strings.Contains(string(out), "is not validly signed") {
			t.Errorf("verifying an unsigned install script = %v, want the release stopped:\n%s", err, out)
		}
	})
}

// The one-line install runs the published script as text, so the signature
// a signed release appends to it, a block of comment lines, changes nothing
// it does: it still downloads its own release.
func TestAPublishedInstallRunsTheSameWithItsSignature(t *testing.T) {
	pinned, output, err := runPin(t, installtest.WindowsPowerShell(), "fpresta0607/code-goblins", "v1.2.3", "Code Goblins Test Publisher")
	if err != nil {
		t.Fatalf("pin-installer.ps1 = %v:\n%s", err, output)
	}
	script, err := os.ReadFile(pinned)
	if err != nil {
		t.Fatal(err)
	}
	signed := filepath.Join(t.TempDir(), "install.ps1")
	if err := os.WriteFile(signed, append(script, signatureBlock...), 0o644); err != nil {
		t.Fatal(err)
	}
	offline := "function Invoke-WebRequest([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing) { Write-Host ('GET ' + $Uri); throw 'offline' }; "
	for _, shell := range installtest.OneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			// Act
			output, local, temp, err := runStrippedPowerShell(t, shell, "", "-Command", offline+"Get-Content -Raw -LiteralPath '"+signed+"' | Invoke-Expression; exit $LASTEXITCODE")

			// Assert
			want := "GET https://github.com/fpresta0607/code-goblins/releases/download/v1.2.3/SHA256SUMS"
			if err == nil || !strings.Contains(output, want) {
				t.Fatalf("the signed install = %v, want it to download from %s as the unsigned one does:\n%s", err, want, output)
			}
			if strings.Contains(output, "SIG #") || strings.Contains(output, "ParserError") {
				t.Errorf("the install read its signature as script:\n%s", output)
			}
			assertNothingInstalled(t, local, temp)
		})
	}
}
