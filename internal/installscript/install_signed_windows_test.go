package installscript

import (
	"cmp"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/installtest"
)

// readStepScript has PowerShell's parser read the script in CG_RELEASE_STEP,
// as a runner reads a step's run: a line "foreach" for each foreach ($file
// in ...), then "file <name>" for each file of its list, which must be
// constants, and a line "command <name>" for each command, then
// "parameter <name>=<value>" for each of its parameters, with no value where
// it is given none that is a constant.
const readStepScript = `$ErrorActionPreference = 'Stop'
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($env:CG_RELEASE_STEP, [ref]$null, [ref]$errors)
if ($errors) { throw "the script does not parse: $errors" }
foreach ($loop in $ast.FindAll({ param($node) $node -is [Management.Automation.Language.ForEachStatementAst] -and $node.Variable.VariablePath.UserPath -eq 'file' }, $true)) {
    'foreach'
    foreach ($file in $loop.Condition.GetPureExpression().SafeGetValue()) { 'file ' + $file }
}
foreach ($command in $ast.FindAll({ param($node) $node -is [Management.Automation.Language.CommandAst] }, $true)) {
    'command ' + $command.GetCommandName()
    $elements = $command.CommandElements
    for ($i = 1; $i -lt $elements.Count; $i++) {
        if ($elements[$i] -isnot [Management.Automation.Language.CommandParameterAst]) { continue }
        $name = $elements[$i].ParameterName
        $argument = $elements[$i].Argument
        if (-not $argument -and $i + 1 -lt $elements.Count -and $elements[$i + 1] -isnot [Management.Automation.Language.CommandParameterAst]) {
            $i++
            $argument = $elements[$i]
        }
        'parameter ' + $name + '=' + $(if ($argument -is [Management.Automation.Language.ConstantExpressionAst]) { $argument.Value })
    }
}`

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
	for _, want := range []string{"if: steps.signing.outputs.signed == 'true'", "uses: azure/artifact-signing-action@", `${{ github.workspace }}\release\install.ps1`, "timestamp-rfc3161:"} {
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

// Every program the release lists in SHA256SUMS is one its build job built
// and handed over, and one it signs, checks the signature of, scans and
// publishes: the install refuses a program its release's publisher did not
// sign. no-mistakes is among them, built by tools/build-no-mistakes.ps1 as
// install.ps1 pins it, and published with the licences of what it compiles in.
func TestTheReleaseSignsChecksAndPublishesEveryProgramItLists(t *testing.T) {
	// Arrange
	source, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		Name string            `yaml:"name"`
		If   string            `yaml:"if"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		With map[string]string `yaml:"with"`
	}
	var workflow struct {
		Jobs map[string]struct {
			If    string `yaml:"if"`
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(source, &workflow); err != nil {
		t.Fatal(err)
	}
	find := func(job, what string, isIt func(step) bool) step {
		t.Helper()
		for _, candidate := range workflow.Jobs[job].Steps {
			if isIt(candidate) {
				return candidate
			}
		}
		t.Fatalf("the release's %s job has no step that %s", job, what)
		return step{}
	}
	named := func(name string) func(step) bool { return func(s step) bool { return s.Name == name } }
	using := func(action string) func(step) bool {
		return func(s step) bool { return strings.HasPrefix(s.Uses, action+"@") }
	}
	// lines is a with value that lists one file to a line.
	lines := func(text string) []string {
		var listed []string
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				listed = append(listed, line)
			}
		}
		return listed
	}
	// parse reads a step's script with PowerShell's parser, so a comment is
	// no code: the files each foreach ($file in ...) walks, and the
	// parameters each command is given, by command and in lower case.
	parse := func(s step) (loops [][]string, commands map[string]map[string]string) {
		t.Helper()
		command := execx.Command(installtest.WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-Command", readStepScript)
		command.Env = append(os.Environ(), "CG_RELEASE_STEP="+s.Run)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("PowerShell could not read the %s step's script: %v\n%s", s.Name, err, out)
		}
		commands = map[string]map[string]string{}
		var parameters map[string]string
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			kind, value, _ := strings.Cut(line, " ")
			switch kind {
			case "foreach":
				loops = append(loops, nil)
			case "file":
				loops[len(loops)-1] = append(loops[len(loops)-1], value)
			case "command":
				parameters = map[string]string{}
				commands[path.Clean(strings.ReplaceAll(value, `\`, "/"))] = parameters
			case "parameter":
				name, argument, _ := strings.Cut(value, "=")
				parameters[strings.ToLower(name)] = argument
			}
		}
		return loops, commands
	}
	// files is the one list a step's script walks with foreach ($file in ...).
	files := func(s step) []string {
		t.Helper()
		loops, _ := parse(s)
		if len(loops) != 1 {
			t.Fatalf("the %s step walks %d lists of files, want one:\n%s", s.Name, len(loops), s.Run)
		}
		return loops[0]
	}
	// condition is the expression of an if, with or without ${{ }}.
	condition := func(expression string) string {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(expression), "${{"), "}}"))
	}
	sums := find("publish", "sums the programs", named("Checksum"))
	builder := find("build", "builds no-mistakes", named("Build no-mistakes"))
	upload := find("build", "hands the build over", using("actions/upload-artifact"))
	sign := find("publish", "signs the programs", func(s step) bool { return s.Name == "Sign" && using("azure/artifact-signing-action")(s) })
	verify := find("publish", "verifies the signatures", named("Verify the signatures"))
	scan := find("publish", "scans the programs", named("Scan with Microsoft Defender"))
	publish := find("publish", "publishes the release", using("softprops/action-gh-release"))

	// Act
	programs := files(sums)
	_, commands := parse(builder)
	build, isBuilt := commands["tools/build-no-mistakes.ps1"]
	handedOver := lines(upload.With["path"])
	signed := lines(sign.With["files"])
	verified := files(verify)
	scanned := files(scan)
	published := lines(publish.With["files"])

	// Assert
	if !slices.Contains(programs, "no-mistakes.exe") {
		t.Errorf("the release lists %q, want no-mistakes.exe among them", programs)
	}
	if !isBuilt || build["destination"] != "no-mistakes.exe" || build["notices"] != "no-mistakes-NOTICES" {
		t.Errorf("the build job builds no-mistakes with %q, want tools/build-no-mistakes.ps1 writing no-mistakes.exe and its notices", builder.Run)
	}
	// A step or job that never runs, as one under if: false, does none of
	// what it lists: each runs on every release, and the signing and its
	// check on every signed one.
	for _, job := range []string{"build", "publish"} {
		if workflow.Jobs[job].If != "" {
			t.Errorf("the release's %s job runs only if %q, want it on every release", job, workflow.Jobs[job].If)
		}
	}
	for _, s := range []step{sums, builder, upload, scan, publish} {
		if s.If != "" {
			t.Errorf("the %s step runs only if %q, want it on every release", cmp.Or(s.Name, s.Uses), s.If)
		}
	}
	for _, s := range []step{sign, verify} {
		if condition(s.If) != "steps.signing.outputs.signed == 'true'" {
			t.Errorf("the %s step runs if %q, want it on every signed release", s.Name, s.If)
		}
	}
	for _, program := range programs {
		for what, list := range map[string][]string{"hand over": handedOver, "verify": verified, "scan": scanned, "publish": published} {
			if !slices.Contains(list, program) {
				t.Errorf("the release does not %s %s: %q", what, program, list)
			}
		}
		if !slices.Contains(signed, `${{ github.workspace }}\`+program) {
			t.Errorf("the release does not sign %s: %q", program, signed)
		}
	}
	if !slices.Contains(handedOver, "no-mistakes-NOTICES") || !slices.Contains(published, "no-mistakes-NOTICES") {
		t.Errorf("the release does not hand over and publish no-mistakes-NOTICES: %q, %q", handedOver, published)
	}
}
