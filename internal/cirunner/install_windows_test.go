package cirunner

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every script the install runs is one Windows PowerShell parses without
// error, so none fails first on the Overlord's machine.
func TestInstallScriptsParse(t *testing.T) {
	// Arrange
	scripts := map[string]string{"check": checkScript, "folder": folderScript, "disk": diskScript, "vm": vmScript, "network": networkScript, "checkpoint": checkpointScript, "service": serviceScript, "start": startScript, "machine": machineScript, "remove the service": removeServiceScript, "remove the VM": removeVMScript, "remove the folder": removeFolderScript}
	names := slices.Sorted(maps.Keys(scripts))
	texts := make([]string, len(names))
	for i, name := range names {
		texts[i] = "$ErrorActionPreference = 'Stop'; " + scripts[name]
	}

	// Act
	said := parseErrors(t, texts)

	// Assert
	for i, name := range names {
		if said[i] != "" {
			t.Errorf("the %s script does not parse: %s", name, said[i])
		}
	}
}

// The parse check names a script that does not parse, and passes one that
// does, so a script it could not read never passes as parsed.
func TestParseErrorsSaysWhatDoesNotParse(t *testing.T) {
	// Arrange
	texts := []string{"'fine'", "if ($true) {", "Get-Item -LiteralPath 'x'"}

	// Act
	said := parseErrors(t, texts)

	// Assert
	if said[0] != "" || !strings.Contains(said[1], "Missing closing '}'") || said[2] != "" {
		t.Errorf("the parse said %q; want only the second script refused for its missing '}'", said)
	}
}

// The service's folder, where the App's key goes, and its parent, when the
// install makes one, are made owned by Administrators, with an access list
// that takes nothing from the parent: the folder open to SYSTEM,
// Administrators and the service alone, and its parent to the first two.
func TestTheServicesFolderIsMadeHeldByItsThreeAlone(t *testing.T) {
	// Arrange
	sddls := regexp.MustCompile(`'(O:BA[^']*)'( \+ \$env:CG_CI_SERVICE_SID \+ '\)')?`).FindAllStringSubmatch(folderScript, -1)
	if len(sddls) != 2 {
		t.Fatalf("the folder script names %d access lists; want the parent's and the folder's", len(sddls))
	}
	service := ServiceSID(ServiceName)
	for i, want := range [][]string{
		{"S-1-5-18=0x1f01ff", "S-1-5-32-544=0x1f01ff"},
		{"S-1-5-18=0x1f01ff", "S-1-5-32-544=0x1f01ff", service + "=0x1301bf"},
	} {
		sddl := sddls[i][1]
		if sddls[i][2] != "" {
			sddl += service + ")"
		}

		// Act
		descriptor, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			t.Fatalf("%s: %v", sddl, err)
		}
		owner, _, err := descriptor.Owner()

		// Assert
		if err != nil || owner.String() != "S-1-5-32-544" {
			t.Errorf("%s is owned by %v (%v); want Administrators", sddl, owner, err)
		}
		if got := folderEntries(t, descriptor); !slices.Equal(got, sorted(want)) {
			t.Errorf("%s gives %q; want %q alone", sddl, got, sorted(want))
		}
	}
}

// folderEntries reads every entry of a protected DACL as its SID and access
// mask, sorted, and fails the test on a DACL that is not protected or an
// entry that is not an allowing one its folder's files and folders take,
// object and container inherit, and nothing else.
func folderEntries(t *testing.T, descriptor *windows.SECURITY_DESCRIPTOR) []string {
	t.Helper()
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the DACL in %s is not protected, so it may inherit entries", descriptor)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("no DACL to read: %v", err)
	}
	var entries []string
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || entry.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			t.Errorf("the DACL in %s holds an entry of type %d with flags %#x; want allowing entries its files and folders take", descriptor, entry.Header.AceType, entry.Header.AceFlags)
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
		entries = append(entries, fmt.Sprintf("%s=%#x", sid.String(), uint32(entry.Mask)))
	}
	return sorted(entries)
}

// The install refuses a service folder that is here already, whoever made
// it, and a parent that is a link or is owned by anyone but SYSTEM and
// Administrators, since an owner may change a folder's access at any time.
func TestTheInstallRefusesAFolderAnotherCouldHold(t *testing.T) {
	for name, row := range map[string]struct {
		arrange func(t *testing.T) string
		want    string
	}{
		"a folder here already": {
			arrange: func(t *testing.T) string { return t.TempDir() },
			want:    "is here already",
		},
		"a parent the user owns": {
			arrange: func(t *testing.T) string {
				parent := t.TempDir()
				protectFolder(t, parent)
				ownByThisUser(t, parent)
				return filepath.Join(parent, "ci-runner")
			},
			want: "is owned by " + thisUser(t),
		},
		"a parent that is a link": {
			arrange: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "Code Goblins")
				target := t.TempDir()
				if output, err := execx.CommandContext(context.Background(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "mklink", "/J", parent, target).CombinedOutput(); err != nil {
					t.Fatalf("mklink /J: %v: %s", err, output)
				}
				return filepath.Join(parent, "ci-runner")
			},
			want: "is a link",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := row.arrange(t)

			// Act
			_, err := powerShell(context.Background(), execx.OSRunner{}, installStepLimit, "check the service's folder", folderCheckScript, "CG_CI_DIR="+dir)

			// Assert
			if err == nil || !strings.Contains(err.Error(), row.want) {
				t.Errorf("the check = %v; want the folder refused, saying %q", err, row.want)
			}
		})
	}
}

// The install's folder and its parent must be held as the install makes
// them, owned by SYSTEM or Administrators, no link, and open to them, and the
// service in the folder, alone: a parent another made between the install's
// first check and its making of the folder, or a folder swapped since, is
// refused. A folder held so can be made only elevated, so the rows that need
// one run only there, as on GitHub's runner.
func TestTheInstallRefusesAFolderNotHeldAsItMadeIt(t *testing.T) {
	isElevated := windows.GetCurrentProcessToken().IsElevated()
	heldParent := func(t *testing.T) string {
		parent := t.TempDir()
		setAccess(t, parent, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
		return parent
	}
	for name, row := range map[string]struct {
		arrange      func(t *testing.T) string
		want         string
		needsElevate bool
	}{
		"a parent the user owns": {
			arrange: func(t *testing.T) string {
				parent := t.TempDir()
				protectFolder(t, parent)
				ownByThisUser(t, parent)
				return filepath.Join(parent, "ci-runner")
			},
			want: "is owned by " + thisUser(t),
		},
		"a parent that is a link": {
			arrange: func(t *testing.T) string {
				parent := filepath.Join(t.TempDir(), "Code Goblins")
				if output, err := execx.CommandContext(context.Background(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "mklink", "/J", parent, t.TempDir()).CombinedOutput(); err != nil {
					t.Fatalf("mklink /J: %v: %s", err, output)
				}
				return filepath.Join(parent, "ci-runner")
			},
			want: "is a link",
		},
		"a folder the user owns": {
			arrange: func(t *testing.T) string {
				dir := filepath.Join(heldParent(t), "ci-runner")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				protectFolder(t, dir)
				ownByThisUser(t, dir)
				return dir
			},
			want:         "is owned by " + thisUser(t),
			needsElevate: true,
		},
		"a folder open to the user": {
			arrange: func(t *testing.T) string {
				dir := filepath.Join(heldParent(t), "ci-runner")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				protectFolder(t, dir)
				return dir
			},
			want:         "lets " + thisUser(t) + " in",
			needsElevate: true,
		},
		"a folder held as the install makes it": {
			arrange: func(t *testing.T) string {
				dir := filepath.Join(heldParent(t), "ci-runner")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				setAccess(t, dir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;"+ServiceSID(ServiceName)+")")
				return dir
			},
			needsElevate: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if row.needsElevate && !isElevated {
				t.Skip("only an elevated test can make a folder Administrators own")
			}
			// Arrange
			dir := row.arrange(t)

			// Act
			err := checkInstalledFolder(dir)

			// Assert
			if row.want == "" && err != nil || row.want != "" && (err == nil || !strings.Contains(err.Error(), row.want)) {
				t.Errorf("checkInstalledFolder = %v; want %q, or nothing", err, row.want)
			}
		})
	}
}

// The step that makes the folder checks it once its script has run, and the
// step that keeps the App's key checks it again before it writes anything,
// so the key never goes into a folder another holds.
func TestTheInstallChecksItsFolderBeforeTheKeyGoesIn(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	protectFolder(t, parent)
	ownByThisUser(t, parent)
	options, _ := installOptions(t, parent, &recorder{})
	keys, _ := serviceFolder(t, goodSettings, "pkcs1")
	options.KeyFile = filepath.Join(keys, "app-key.pem")
	if err := os.Mkdir(options.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	steps := InstallSteps(options)

	// Act
	folderErr := steps[1].Run(context.Background())
	keepErr := steps[9].Run(context.Background())

	// Assert
	for step, err := range map[string]error{steps[1].Title: folderErr, steps[9].Title: keepErr} {
		if err == nil || !strings.Contains(err.Error(), "is owned by "+thisUser(t)) {
			t.Errorf("%s = %v; want the folder refused for its parent's owner", step, err)
		}
	}
	if entries, err := os.ReadDir(options.Dir); err != nil || len(entries) != 0 {
		t.Errorf("the folder holds %d file(s) (%v); want the key and settings kept out", len(entries), err)
	}
}

// The install's own making of the folder refuses one that is here already,
// as a folder another made between the check and the making would be.
func TestTheInstallMakesNoFolderThatIsHereAlready(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	_, err := powerShell(context.Background(), execx.OSRunner{}, installStepLimit, "make the service's folder", folderScript, "CG_CI_DIR="+dir, "CG_CI_SERVICE_SID="+ServiceSID(ServiceName))

	// Assert
	if err == nil || !strings.Contains(err.Error(), "is here already") {
		t.Errorf("the folder script = %v; want the folder refused", err)
	}
}

// ServiceSID names the account Windows would give any service, as sc.exe
// showsid does, which needs no service to exist.
func TestServiceSIDIsWhatWindowsSays(t *testing.T) {
	for _, name := range []string{"cg-ci-runner", "Code-Goblins-Test", "x"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			sc := filepath.Join(os.Getenv("SystemRoot"), "System32", "sc.exe")

			// Act
			output, err := execx.CommandContext(context.Background(), sc, "showsid", name).Output()

			// Assert
			said := regexp.MustCompile(`S-1-5-80(-\d+){5}`).FindString(string(output))
			if err != nil || said == "" || ServiceSID(name) != said {
				t.Errorf("ServiceSID(%q) = %q; sc.exe showsid says %q (%v)", name, ServiceSID(name), said, err)
			}
		})
	}
}

// The install's first step refuses a program anywhere but in Program Files,
// which only Administrators may change, before it runs anything: the
// service runs the program in Hyper-V Administrators, holding the App's key.
func TestTheInstallRefusesAProgramOutsideProgramFiles(t *testing.T) {
	programFiles, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, row := range map[string]struct {
		program   string
		isRefused bool
	}{
		"in Program Files":           {program: filepath.Join(programFiles, "Code Goblins", "ci-runner", "cg-ci-runner.exe")},
		"in the user's own folder":   {program: filepath.Join(t.TempDir(), "cg-ci-runner.exe"), isRefused: true},
		"beside Program Files":       {program: programFiles + ` Evil\cg-ci-runner.exe`, isRefused: true},
		"out of Program Files by ..": {program: filepath.Join(programFiles, "..") + `\Users\cg-ci-runner.exe`, isRefused: true},
		"Program Files' own folder":  {program: programFiles, isRefused: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			commands := &recorder{}
			options, _ := installOptions(t, t.TempDir(), commands)
			options.Program = row.program

			// Act
			err := InstallSteps(options)[0].Run(context.Background())

			// Assert
			isRefused := err != nil && strings.Contains(err.Error(), "which only Administrators may change")
			if isRefused != row.isRefused || row.isRefused && len(commands.ran) != 0 {
				t.Errorf("the check = %v, having run %d script(s); want it refused %v, and nothing run when refused", err, len(commands.ran), row.isRefused)
			}
		})
	}
}

// runStubbed runs script as the install does, after stubs, functions that
// stand in for the Hyper-V and account cmdlets it calls, and returns what it
// wrote.
func runStubbed(t *testing.T, stubs, script string, env ...string) (string, error) {
	t.Helper()
	return powerShell(context.Background(), execx.OSRunner{}, installStepLimit, "run a script with stand-ins", stubs+"\n"+script, env...)
}

// parseErrors parses each of texts with Windows PowerShell's own parser and
// returns what it said of each, in their order: nothing of one that parses.
// One Windows PowerShell parses them all: on a slow CI runner each one
// started took up to a minute and a half, and this package, starting them
// one after another, ran past its 20 minutes (#1017). Each text reaches it
// in a variable of its own, and it says how long each text it parsed was,
// so a text it never read fails the test instead of passing unparsed.
func parseErrors(t *testing.T, texts []string) []string {
	t.Helper()
	env := make([]string, len(texts))
	for i, text := range texts {
		env[i] = fmt.Sprintf("CG_CI_SCRIPT_%d=%s", i, text)
	}
	parse := fmt.Sprintf(`foreach ($i in 0..%d) {
  $text = [Environment]::GetEnvironmentVariable('CG_CI_SCRIPT_' + $i)
  $errors = $null
  [void][System.Management.Automation.Language.Parser]::ParseInput($text, [ref]$null, [ref]$errors)
  "$i parsed $($text.Length)"
  foreach ($e in $errors) { "$i error " + ($e.Message -replace '\s+', ' ') }
}`, len(texts)-1)
	output, err := powerShell(context.Background(), execx.OSRunner{}, installStepLimit, "parse the scripts", parse, env...)
	if err != nil {
		t.Fatal(err)
	}
	said := make([]string, len(texts))
	parsed := make([]int, len(texts))
	for _, line := range strings.Split(output, "\n") {
		index, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
		kind, what, _ := strings.Cut(rest, " ")
		i, err := strconv.Atoi(index)
		if err != nil || i < 0 || i >= len(texts) {
			t.Fatalf("the parse said %q; want a line about one of the %d scripts", line, len(texts))
		}
		switch kind {
		case "parsed":
			parsed[i], _ = strconv.Atoi(what)
		case "error":
			said[i] = strings.TrimSpace(said[i] + " " + what)
		default:
			t.Fatalf("the parse said %q; want what it parsed or an error", line)
		}
	}
	for i, text := range texts {
		if want := len(utf16.Encode([]rune(text))); parsed[i] != want {
			t.Fatalf("Windows PowerShell parsed %d characters of script %d; want its %d: %s", parsed[i], i, want, output)
		}
	}
	return said
}

// The VM's jobs may reach no address in closedRanges but by DNS and DHCP, the
// rules that open those come first, and the VM may not answer as a DHCP
// server or a router.
func TestTheVMsNetworkIsClosedToTheHostAndTheLAN(t *testing.T) {
	// Arrange
	const stubs = `function Set-VMNetworkAdapter { [CmdletBinding()] param([string]$VMName, [string]$DhcpGuard, [string]$RouterGuard) "guards $VMName $DhcpGuard $RouterGuard" }
function Add-VMNetworkAdapterExtendedAcl { [CmdletBinding()] param([string]$VMName, [string]$Action, [string]$Direction, [string]$RemoteIPAddress, [string]$Protocol, [string]$RemotePort, [int]$Weight) "rule $VMName $Action $Direction remote=$RemoteIPAddress $Protocol $RemotePort weight=$Weight" }`

	// Act
	output, err := runStubbed(t, stubs, networkScript)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "guards "+VMName+" On On" {
		t.Fatalf("the script said %q first; want DHCP and router guards on", lines)
	}
	rule := regexp.MustCompile(`^rule ` + VMName + ` (Allow|Deny) Outbound remote=(\S*) (\S*) (\S*) weight=(\d+)$`)
	var closed, opened []string
	weights := map[string]bool{}
	lowestOpen, highestClosed := 1<<30, 0
	for _, line := range lines[1:] {
		match := rule.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("the script said %q; want outbound rules for the VM alone", line)
		}
		weight, _ := strconv.Atoi(match[5])
		if weights[match[5]] {
			t.Errorf("two rules weigh %d, which Hyper-V refuses", weight)
		}
		weights[match[5]] = true
		if match[1] == "Deny" && match[3] == "" && match[4] == "" {
			closed = append(closed, match[2])
			highestClosed = max(highestClosed, weight)
		} else if match[1] == "Allow" && match[2] == "" {
			opened = append(opened, match[3]+" "+match[4])
			lowestOpen = min(lowestOpen, weight)
		} else {
			t.Errorf("the rule %q is neither a closed range nor DNS or DHCP", line)
		}
	}
	if !slices.Equal(closed, strings.Split(closedRanges, ",")) {
		t.Errorf("closed %q; want %q", closed, strings.Split(closedRanges, ","))
	}
	if !slices.Equal(opened, []string{"UDP 53", "TCP 53", "UDP 67"}) || lowestOpen <= highestClosed {
		t.Errorf("opened %q from weight %d, above closing to weight %d; want DNS and DHCP alone, weighed over every closed range", opened, lowestOpen, highestClosed)
	}
}

// The VM is made with its first boot's DVD in its drive, and with its
// Key-Value Pair Exchange off, and the install stops when it cannot find
// that service to turn it off.
func TestTheVMIsMadeWithItsSeedAndNoKeyValueExchange(t *testing.T) {
	services := []string{`Heartbeat\84EAAE65-2F2E-45F5-9BB5-0E857DC8EB47`, `Key-Value Pair Exchange` + exchangeID, `Shutdown\9F8233AC-BE49-4C79-8EE3-E7E1985B2077`}
	for name, row := range map[string]struct {
		services []string
		isMade   bool
	}{
		"its services as Hyper-V gives them": {services: services, isMade: true},
		"no Key-Value Pair Exchange":         {services: []string{services[0], services[2]}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stubs := `function New-VM { [CmdletBinding()] param([string]$Name, [int]$Generation, [int64]$MemoryStartupBytes, [string]$VHDPath, [string]$Path, [string]$SwitchName) [pscustomobject]@{ Name = $Name } }
function Set-VMMemory { [CmdletBinding()] param($VM, [bool]$DynamicMemoryEnabled, [int64]$MinimumBytes, [int64]$StartupBytes, [int64]$MaximumBytes) }
function Set-VMProcessor { [CmdletBinding()] param($VM, [int]$Count) }
function Get-VMHardDiskDrive { [CmdletBinding()] param($VM, [int]$ControllerNumber, [int]$ControllerLocation) 'the disk' }
function Set-VMFirmware { [CmdletBinding()] param($VM, [string]$EnableSecureBoot, [string]$SecureBootTemplate, $FirstBootDevice) }
function Add-VMDvdDrive { [CmdletBinding()] param($VM, [string]$Path) "dvd $Path" }
function Get-VMIntegrationService { [CmdletBinding()] param($VM) foreach ($service in $env:CG_TEST_SERVICES.Split(';')) { $name, $id = $service.Split('\'); [pscustomobject]@{ Name = $name; Id = 'Microsoft:0F0E\' + $id } } }
function Disable-VMIntegrationService { [CmdletBinding()] param([Parameter(ValueFromPipeline)]$VMIntegrationService) process { "off $($VMIntegrationService.Name)" } }
function Set-VM { [CmdletBinding()] param($VM, [string]$CheckpointType, [bool]$AutomaticCheckpointsEnabled, [string]$AutomaticStartAction, [string]$AutomaticStopAction) 'set' }`
			seed := filepath.Join(t.TempDir(), "seed.iso")

			// Act
			output, err := runStubbed(t, stubs, vmScript, "CG_CI_DISK=disk.vhdx", "CG_CI_VM=vm", "CG_CI_SEED="+seed, "CG_TEST_SERVICES="+strings.Join(row.services, ";"))

			// Assert
			want := "dvd " + seed + "\r\noff Key-Value Pair Exchange\r\nset"
			if row.isMade && (err != nil || output != want) {
				t.Errorf("the VM script = %v, saying %q; want %q", err, output, want)
			}
			if !row.isMade && (err == nil || !strings.Contains(err.Error(), "0 Key-Value Pair Exchange services")) {
				t.Errorf("the VM script = %v, saying %q; want it stopped", err, output)
			}
		})
	}
}

// The clean checkpoint is taken with the first boot's DVD out of the drive
// and deleted, so no job ever boots with it.
func TestTheCheckpointIsTakenWithoutTheSeed(t *testing.T) {
	// Arrange
	const stubs = `function Set-VMDvdDrive { [CmdletBinding()] param([string]$VMName, $Path) if ($null -eq $Path) { "ejected from $VMName" } else { "inserted $Path" } }
function Checkpoint-VM { [CmdletBinding()] param([string]$Name, [string]$SnapshotName) "checkpoint $SnapshotName, the seed here $(Test-Path -LiteralPath $env:CG_CI_SEED)" }`
	seed := filepath.Join(t.TempDir(), "seed.iso")
	if err := BuildSeed(seed); err != nil {
		t.Fatal(err)
	}

	// Act
	output, err := runStubbed(t, stubs, checkpointScript, "CG_CI_SEED="+seed)

	// Assert
	if want := "ejected from " + VMName + "\r\ncheckpoint " + CleanCheckpoint + ", the seed here False"; err != nil || output != want {
		t.Errorf("the checkpoint script = %v, saying %q; want %q", err, output, want)
	}
}
