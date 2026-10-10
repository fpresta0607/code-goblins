package installscript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// signatureBlock is, as PowerShell writes a string, the shape of what
// signing appends to a script: a block of comment lines holding the signature
// in base64. It is no signature.
const signatureBlock = `"` + "`r`n# SIG # Begin signature block`r`n" +
	"# MIIFgwYJKoZIhvcNAQcCoIIFdDCCBXACAQExCzAJBgUrDgMCGgUAMGkGCisGAQQB`r`n" +
	"# gjcCAQSgWzBZMDQGCisGAQQBgjcCAR4wJgIDAQAABBAfzDtgWUsITrck0sYpfvNR`r`n" +
	"# SIG # End signature block`r`n" + `"`

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

	// The step's own script, run against a script nobody signed, in each
	// PowerShell: the release runs it in PowerShell 7, which a GitHub runner
	// has and this test then runs it in too. It runs in a stripped session,
	// which hands PowerShell no module path of another PowerShell's: a
	// runner's test step runs in PowerShell 7, and Windows PowerShell given
	// that module path cannot load the module Get-AuthenticodeSignature is in.
	for _, shell := range installtest.OneLineShells(t) {
		t.Run("an install script nobody signed stops the release in "+filepath.Base(shell), func(t *testing.T) {
			// Arrange: any script nobody signed. It is not the install
			// script, since each new copy of that on disk is one more unsigned
			// script that downloads and runs programs, which Defender sends to
			// Microsoft.
			command, _, folder := installtest.StrippedCommand(t, "", nil, shell, "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference = 'Stop'\n"+verify)
			if err := os.MkdirAll(filepath.Join(folder, "release"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, "release", "install.ps1"), []byte("Write-Output 'nobody signed this'\r\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			command.Env = append(command.Env, "SIGNING_PUBLISHER=Code Goblins Test Publisher")

			// Act
			out, err := command.CombinedOutput()

			// Assert
			if err == nil || !strings.Contains(string(out), "is not validly signed") {
				t.Errorf("verifying an unsigned install script = %v, want the release stopped:\n%s", err, out)
			}
		})
	}
}

// The one-line install runs the published script as text, so the signature
// a signed release appends to it, a block of comment lines, changes nothing
// it does: it still downloads its release. The block is added in memory, to
// the script as the repository has it: a signed copy written to disk for the
// test would be one more unsigned script that downloads and runs programs,
// and Defender sent three such copies to Microsoft when this test first
// wrote them, on 2026-10-10.
func TestTheInstallRunsTheSameWithASignatureAtItsEnd(t *testing.T) {
	offline := "function Invoke-WebRequest([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing) { Write-Host ('GET ' + $Uri); throw 'offline' }; "
	signed := "((Get-Content -Raw -LiteralPath '" + installScript(t) + "') + " + signatureBlock + ")"
	for _, shell := range installtest.OneLineShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			// Act
			output, local, temp, err := runStrippedPowerShell(t, shell, "", "-Command", offline+"$signed = "+signed+"; if (-not $signed.TrimEnd().EndsWith('# SIG # End signature block')) { throw 'the script has no signature block' }; $signed | Invoke-Expression; exit $LASTEXITCODE")

			// Assert
			want := "GET https://github.com/fpresta0607/code-goblins/releases/latest/download/SHA256SUMS"
			if err == nil || !strings.Contains(output, want) {
				t.Fatalf("the signed install = %v, want it to download from %s as the unsigned one does:\n%s", err, want, output)
			}
			if strings.Contains(output, "SIG #") || strings.Contains(output, "ParserError") || strings.Contains(output, "has no signature block") {
				t.Errorf("the install read its signature as script, or had none:\n%s", output)
			}
			assertNothingInstalled(t, local, temp)
		})
	}
}
