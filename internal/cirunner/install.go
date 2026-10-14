package cirunner

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ServiceName is the desktop CI runner's Windows service, which runs as its
// own virtual account, NT SERVICE\cg-ci-runner.
const ServiceName = "cg-ci-runner"

const (
	// installStepLimit bounds a step's script, and firstBootLimit the VM's
	// first boot, which installs Docker and the runner.
	installStepLimit = 10 * time.Minute
	firstBootLimit   = 30 * time.Minute
	// readyLimit is how long the started service has to answer on its pipe:
	// it first puts the VM back and deletes its runners at GitHub.
	readyLimit = 3 * time.Minute
	// minimumFree is the free disk the install needs where the service's
	// folder is: the image, its conversion, and room for the VM's disk to
	// grow.
	minimumFree = 40 << 30
)

// Step is one step of the install or the uninstall: what it does, what it
// runs, exactly, for a dry run to show, and the run itself.
type Step struct {
	Title string
	Says  []string
	Run   func(ctx context.Context) error
}

// InstallOptions are what the install is given. User is the SID of the
// fleet's Windows user, which may open the service's pipe.
type InstallOptions struct {
	AppID          int64
	InstallationID int64
	Owner          string
	Repo           string
	KeyFile        string
	User           string
	// Dir is the service's folder, Program the installed cg-ci-runner.exe.
	Dir     string
	Program string
	// Commands runs Windows PowerShell, Download fetches a URL to a file,
	// Ask asks the started service, and Sleep waits.
	Commands execx.Runner
	Download func(ctx context.Context, url, path string) error
	Ask      func(ctx context.Context, request Request, reply func(Reply)) error
	Sleep    func(ctx context.Context, d time.Duration)
	Now      func() time.Time
}

// ServiceSID is the SID Windows gives a service's virtual account: S-1-5-80
// and the SHA-1 of its name in capitals, as UTF-16, read as five numbers. It
// names the account before the service is made.
func ServiceSID(name string) string {
	units := utf16.Encode([]rune(strings.ToUpper(name)))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	sum := sha1.Sum(data)
	parts := []string{"S-1-5-80"}
	for i := 0; i < len(sum); i += 4 {
		parts = append(parts, strconv.FormatUint(uint64(binary.LittleEndian.Uint32(sum[i:])), 10))
	}
	return strings.Join(parts, "-")
}

// folderCheckScript refuses a service folder that is here already, and a
// parent folder that is a link or is owned by anyone but SYSTEM and
// Administrators. The install makes the folder itself, owned by
// Administrators, since whoever made it first owns it, and an owner may
// change its access at any time and read the App's key once it lands.
const folderCheckScript = `if (Test-Path -LiteralPath $env:CG_CI_DIR) { throw ($env:CG_CI_DIR + ' is here already, and the install makes it itself, so that no other user made it: uninstall the runner first, or remove it') }
$parent = Split-Path -Parent $env:CG_CI_DIR
if (Test-Path -LiteralPath $parent) {
    if ((Get-Item -LiteralPath $parent -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw ($parent + ' is a link, so the install will not make the service folder in it') }
    $owner = (Get-Acl -LiteralPath $parent).GetOwner([Security.Principal.SecurityIdentifier]).Value
    if ($owner -ne 'S-1-5-18' -and $owner -ne 'S-1-5-32-544') { throw ($parent + ' is owned by ' + $owner + ', not SYSTEM or Administrators, so the install will not make the service folder in it') }
}`

// The install's scripts. Each takes the paths it needs from the environment,
// never in its text, and names the VM, its checkpoint and the service only as
// the constants they are.
const (
	checkScript = folderCheckScript + `
if (-not (Get-Command Get-VM -ErrorAction SilentlyContinue)) { throw 'Hyper-V is not on: turn on the Hyper-V feature, restart, and run the install again' }
if (Get-VM -Name '` + VMName + `' -ErrorAction SilentlyContinue) { throw 'a VM named ` + VMName + ` is here already: uninstall the runner first' }
if (Get-Service -Name '` + ServiceName + `' -ErrorAction SilentlyContinue) { throw 'the ` + ServiceName + ` service is here already: uninstall the runner first' }
$null = Get-VMSwitch -Name 'Default Switch'
$free = (Get-Item -LiteralPath $env:CG_CI_ROOT).PSDrive.Free
if ($free -lt [int64]$env:CG_CI_FREE) { throw ('the drive holds ' + [math]::Floor($free / 1GB) + ' GB free, and the runner needs ' + [math]::Floor([int64]$env:CG_CI_FREE / 1GB)) }`
	folderScript = `function New-HeldFolder([string]$Path, [string]$Sddl) {
    if (Test-Path -LiteralPath $Path) { throw ($Path + ' is here already, and the install makes it itself') }
    $security = New-Object Security.AccessControl.DirectorySecurity
    $security.SetSecurityDescriptorSddlForm($Sddl)
    $null = [IO.Directory]::CreateDirectory($Path, $security)
    $owner = (Get-Acl -LiteralPath $Path).GetOwner([Security.Principal.SecurityIdentifier]).Value
    if ($owner -ne 'S-1-5-32-544' -or @(Get-ChildItem -LiteralPath $Path -Force).Count -ne 0) { throw ($Path + ' was made by another before the install made it') }
}
$parent = Split-Path -Parent $env:CG_CI_DIR
if (-not (Test-Path -LiteralPath $parent)) { New-HeldFolder $parent 'O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)' }
New-HeldFolder $env:CG_CI_DIR ('O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;' + $env:CG_CI_SERVICE_SID + ')')`
	diskScript = `Convert-VHD -Path $env:CG_CI_VHD -DestinationPath $env:CG_CI_DISK -VHDType Dynamic
Resize-VHD -Path $env:CG_CI_DISK -SizeBytes 64GB
Remove-Item -LiteralPath $env:CG_CI_VHD, $env:CG_CI_IMAGE`
	settingsScript = `$null = New-VHD -Path $env:CG_CI_SETTINGS -SizeBytes 64MB -Dynamic
$volume = Mount-VHD -Path $env:CG_CI_SETTINGS -Passthru | Get-Disk | Initialize-Disk -PartitionStyle MBR -PassThru | New-Partition -UseMaximumSize -AssignDriveLetter | Format-Volume -FileSystem FAT32 -NewFileSystemLabel 'cidata' -Confirm:$false
Copy-Item -Path (Join-Path $env:CG_CI_SETTINGS_FILES '*') -Destination ($volume.DriveLetter + ':\')
Dismount-VHD -Path $env:CG_CI_SETTINGS`
	vmScript = `$vm = New-VM -Name '` + VMName + `' -Generation 2 -MemoryStartupBytes 2GB -VHDPath $env:CG_CI_DISK -Path $env:CG_CI_VM -SwitchName 'Default Switch'
Set-VMMemory -VM $vm -DynamicMemoryEnabled $true -MinimumBytes 2GB -StartupBytes 2GB -MaximumBytes 6GB
Set-VMProcessor -VM $vm -Count 4
Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate 'MicrosoftUEFICertificateAuthority' -FirstBootDevice (Get-VMHardDiskDrive -VM $vm -ControllerNumber 0 -ControllerLocation 0)
Add-VMDvdDrive -VM $vm
Add-VMHardDiskDrive -VM $vm -Path $env:CG_CI_SETTINGS
Set-VM -VM $vm -CheckpointType Standard -AutomaticCheckpointsEnabled $false -AutomaticStartAction Nothing -AutomaticStopAction TurnOff`
	checkpointScript = `Get-VMHardDiskDrive -VMName '` + VMName + `' | Where-Object { $_.Path -eq $env:CG_CI_SETTINGS } | Remove-VMHardDiskDrive
Remove-Item -LiteralPath $env:CG_CI_SETTINGS
Checkpoint-VM -Name '` + VMName + `' -SnapshotName '` + CleanCheckpoint + `'`
	serviceScript = `$null = New-Service -Name '` + ServiceName + `' -BinaryPathName ('"' + $env:CG_CI_PROGRAM + '" service') -DisplayName 'Code Goblins desktop CI runner' -StartupType Automatic
& sc.exe sidtype ` + ServiceName + ` unrestricted
if ($LASTEXITCODE -ne 0) { throw ('sc.exe sidtype failed with exit ' + $LASTEXITCODE) }
& sc.exe config ` + ServiceName + ` obj= 'NT SERVICE\` + ServiceName + `'
if ($LASTEXITCODE -ne 0) { throw ('sc.exe config failed with exit ' + $LASTEXITCODE) }
Add-LocalGroupMember -SID 'S-1-5-32-578' -Member ('NT SERVICE\` + ServiceName + `')`
	startScript = `Start-Service -Name '` + ServiceName + `'`
)

// InstallSteps are the install's steps, in order. Each says the scripts it
// runs and what else it does, so a dry run shows the install whole.
func InstallSteps(o InstallOptions) []Step {
	vmDir := filepath.Join(o.Dir, "vm")
	downloads := filepath.Join(o.Dir, "download")
	image := filepath.Join(downloads, "disk.img")
	vhd := filepath.Join(downloads, "disk.vhd")
	disk := filepath.Join(vmDir, "disk.vhdx")
	settings := filepath.Join(vmDir, "settings.vhdx")
	settingsFiles := filepath.Join(vmDir, "settings")
	checkEnv := []string{"CG_CI_DIR=" + o.Dir, "CG_CI_ROOT=" + filepath.VolumeName(o.Dir) + `\`, "CG_CI_FREE=" + strconv.Itoa(minimumFree)}
	folderEnv := []string{"CG_CI_DIR=" + o.Dir, "CG_CI_SERVICE_SID=" + ServiceSID(ServiceName)}
	diskEnv := []string{"CG_CI_VHD=" + vhd, "CG_CI_DISK=" + disk, "CG_CI_IMAGE=" + image}
	settingsEnv := []string{"CG_CI_SETTINGS=" + settings, "CG_CI_SETTINGS_FILES=" + settingsFiles}
	vmEnv := []string{"CG_CI_DISK=" + disk, "CG_CI_VM=" + vmDir, "CG_CI_SETTINGS=" + settings}
	checkpointEnv := []string{"CG_CI_SETTINGS=" + settings}
	serviceEnv := []string{"CG_CI_PROGRAM=" + o.Program}
	vm := HyperV{Commands: o.Commands}
	return []Step{
		{
			Title: "check this machine",
			Says:  append([]string{"refuse the program unless it is in Program Files: " + o.Program}, scriptSays(checkScript, checkEnv...)...),
			Run: func(ctx context.Context) error {
				if err := checkProgram(o.Program); err != nil {
					return err
				}
				return runScript(o.Commands, "check this machine", checkScript, checkEnv...)(ctx)
			},
		},
		{
			Title: "make the service's folder, owned by Administrators, which only SYSTEM, Administrators and the service may open",
			Says:  scriptSays(folderScript, folderEnv...),
			Run:   runScript(o.Commands, "make the service's folder", folderScript, folderEnv...),
		},
		{
			Title: "download and check the image",
			Says:  []string{"download " + ImageURL + " to " + image, "refuse it unless its SHA-256 is " + ImageSHA256},
			Run: func(ctx context.Context) error {
				if err := os.MkdirAll(downloads, 0o700); err != nil {
					return err
				}
				if err := o.Download(ctx, ImageURL, image); err != nil {
					return fmt.Errorf("cirunner: download the image: %w", err)
				}
				return checkSHA256(image, ImageSHA256)
			},
		},
		{
			Title: "make the disk",
			Says:  append([]string{"convert " + image + " from qcow2 to the fixed VHD " + vhd + ", in cg-ci-runner"}, scriptSays(diskScript, diskEnv...)...),
			Run: func(ctx context.Context) error {
				if err := os.MkdirAll(vmDir, 0o700); err != nil {
					return err
				}
				if err := ConvertQCOW2(image, vhd, o.Now()); err != nil {
					return err
				}
				return runScript(o.Commands, "make the disk", diskScript, diskEnv...)(ctx)
			},
		},
		{
			Title: "write the first boot's settings",
			Says:  append([]string{"write user-data and meta-data to " + settingsFiles}, scriptSays(settingsScript, settingsEnv...)...),
			Run: func(ctx context.Context) error {
				if err := writeFirstBoot(settingsFiles); err != nil {
					return err
				}
				return runScript(o.Commands, "write the first boot's settings", settingsScript, settingsEnv...)(ctx)
			},
		},
		{
			Title: "create the VM",
			Says:  scriptSays(vmScript, vmEnv...),
			Run:   runScript(o.Commands, "create the VM", vmScript, vmEnv...),
		},
		{
			Title: "first boot and its checkpoint",
			Says:  append([]string{"start the VM and wait, for 30 minutes at most, for its first boot to power it off"}, scriptSays(checkpointScript, checkpointEnv...)...),
			Run: func(ctx context.Context) error {
				if err := waitForFirstBoot(ctx, vm, o.Sleep, firstBootLimit); err != nil {
					return err
				}
				return runScript(o.Commands, "take the clean checkpoint", checkpointScript, checkpointEnv...)(ctx)
			},
		},
		{
			Title: "create the service and its account",
			Says:  scriptSays(serviceScript, serviceEnv...),
			Run:   runScript(o.Commands, "create the service and its account", serviceScript, serviceEnv...),
		},
		{
			Title: "keep the App's settings and key",
			Says:  []string{"write app.json to " + o.Dir + " for App " + strconv.FormatInt(o.AppID, 10) + ", installation " + strconv.FormatInt(o.InstallationID, 10) + ", on " + o.Owner + "/" + o.Repo + ", whose pipe " + o.User + " may open", "copy " + o.KeyFile + " to " + filepath.Join(o.Dir, "app-key.pem")},
			Run:   func(context.Context) error { return keepApp(o) },
		},
		{
			Title: "start the service",
			Says:  scriptSays(startScript),
			Run:   runScript(o.Commands, "start the service", startScript),
		},
		{
			Title: "readiness check",
			Says:  []string{"ask the service, on " + PipeName + ", what runs, for 3 minutes at most"},
			Run:   func(ctx context.Context) error { return ready(ctx, o.Ask, o.Sleep, readyLimit) },
		},
	}
}

// checkProgram refuses a program outside Program Files, which only
// Administrators may change. The service runs it in Hyper-V Administrators,
// holding the App's key, so whoever may change the program owns both.
func checkProgram(program string) error {
	programFiles, err := programFilesFolder()
	if err != nil {
		return err
	}
	if relative, err := filepath.Rel(programFiles, program); err != nil || !filepath.IsLocal(relative) {
		return fmt.Errorf("cirunner: %s is not in %s, which only Administrators may change, so the service will not run it: install from the release's ci-runner-install.ps1, which puts it there", program, programFiles)
	}
	return nil
}

// runScript is a step's run of script in Windows PowerShell with commands,
// within installStepLimit, with env added to its environment.
func runScript(commands execx.Runner, doing, script string, env ...string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := powerShell(ctx, commands, installStepLimit, doing, script, env...)
		return err
	}
}

// scriptSays is what a dry run says of a step that runs script with env: the
// environment, then each line.
func scriptSays(script string, env ...string) []string {
	return append(append([]string{}, env...), strings.Split(script, "\n")...)
}

// RunSteps runs steps in order, saying each to out, and stops at the first
// that fails. A dry run says each step and what it runs, and runs nothing.
func RunSteps(ctx context.Context, steps []Step, isDryRun bool, out io.Writer) error {
	for i, step := range steps {
		fmt.Fprintf(out, "step %d of %d: %s\n", i+1, len(steps), step.Title)
		if isDryRun {
			for _, line := range step.Says {
				fmt.Fprintf(out, "    %s\n", line)
			}
			continue
		}
		if err := step.Run(ctx); err != nil {
			return fmt.Errorf("step %d, %s: %w", i+1, step.Title, err)
		}
	}
	return nil
}

// checkSHA256 refuses, and removes, a download whose SHA-256 is not want.
func checkSHA256(path, want string) error {
	file, err := fsx.Open(path)
	if err != nil {
		return err
	}
	sum := sha256.New()
	_, err = io.Copy(sum, file)
	file.Close()
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return errors.Join(fmt.Errorf("cirunner: %s's SHA-256 is %s, not the pinned %s", filepath.Base(path), got, want), os.Remove(path))
	}
	return nil
}

// writeFirstBoot writes the VM's first boot settings, user-data and
// meta-data, to dir.
func writeFirstBoot(dir string) error {
	data, err := UserData()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "user-data"), []byte(data), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta-data"), []byte(MetaData), 0o600)
}

// waitForFirstBoot starts the VM and waits, within limit, for its first boot to
// power it off, which only a setup run to its end does.
func waitForFirstBoot(ctx context.Context, vm Hypervisor, sleep func(context.Context, time.Duration), limit time.Duration) error {
	if err := vm.Start(ctx); err != nil {
		return err
	}
	waiting, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		isRunning, err := vm.IsRunning(waiting)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case waiting.Err() != nil:
			return fmt.Errorf("cirunner: the VM's first boot did not power it off within %v, so its setup failed or did not end: the VM's console in Hyper-V Manager shows why", limit)
		case err != nil:
			return err
		case !isRunning:
			return nil
		}
		sleep(waiting, 10*time.Second)
	}
}

// keepApp writes the service's settings and copies the App's key into its
// folder, which only SYSTEM, Administrators and the service may open, and
// reads them back as the service will.
func keepApp(o InstallOptions) error {
	if _, err := readKey(o.KeyFile); err != nil {
		return err
	}
	settings, err := json.MarshalIndent(Config{AppID: o.AppID, InstallationID: o.InstallationID, Owner: o.Owner, Repo: o.Repo, Allowed: o.User}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNew(filepath.Join(o.Dir, "app.json"), append(settings, '\n')); err != nil {
		return err
	}
	data, err := fsx.ReadFile(o.KeyFile)
	if err != nil {
		return err
	}
	if err := writeNew(filepath.Join(o.Dir, "app-key.pem"), data); err != nil {
		return err
	}
	_, _, err = ReadConfig(o.Dir)
	return err
}

// writeNew writes data to a file at path that is not there yet, so a file
// another user made, and owns, never holds the App's settings or key.
func writeNew(path string, data []byte) (failure error) {
	file, err := fsx.CreateNew(path, 0o600)
	if err != nil {
		return fmt.Errorf("cirunner: make %s: %w", path, err)
	}
	defer func() { failure = errors.Join(failure, file.Close()) }()
	_, err = file.Write(data)
	return err
}

// ready waits, within limit, for the started service to answer on its pipe.
func ready(ctx context.Context, ask func(context.Context, Request, func(Reply)) error, sleep func(context.Context, time.Duration), limit time.Duration) error {
	waiting, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		err := ask(waiting, Request{Kind: AskStatus}, func(Reply) {})
		if err == nil {
			return nil
		}
		sleep(waiting, 5*time.Second)
		if waiting.Err() != nil {
			return fmt.Errorf("cirunner: the service did not answer within %v (%w), and ci-runner.log in its folder says why", limit, err)
		}
	}
}

// DownloadFile fetches url to path with client, refusing any answer but 200.
func DownloadFile(ctx context.Context, client *http.Client, url, path string) (failure error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered HTTP %d", url, response.StatusCode)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, file.Close()) }()
	_, err = io.Copy(file, response.Body)
	return err
}
