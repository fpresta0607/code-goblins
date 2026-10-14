package cirunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// VMName is the one VM the service runs jobs in, and CleanCheckpoint the
// checkpoint every job starts from.
const (
	VMName          = "cg-ci-precisiondocs"
	CleanCheckpoint = "clean"
)

// Hypervisor is the VM as a job drives it.
type Hypervisor interface {
	TurnOff(ctx context.Context) error
	Restore(ctx context.Context) error
	AttachDVD(ctx context.Context, iso string) error
	Start(ctx context.Context) error
	IsRunning(ctx context.Context) (bool, error)
}

// dvdVariable carries the DVD's path to the script that attaches it, so no
// path is ever part of a script's text.
const dvdVariable = "CG_CI_DVD"

// hyperVLimit bounds each Hyper-V call.
const hyperVLimit = 2 * time.Minute

// HyperV drives the VM through fixed Windows PowerShell scripts, run with
// Commands, that name only VMName and CleanCheckpoint.
type HyperV struct {
	Commands execx.Runner
}

// TurnOff turns the VM off at once, as pulling its power would, unless it is
// off already.
func (h HyperV) TurnOff(ctx context.Context) error {
	_, err := h.run(ctx, "turn the VM off", "$vm = Get-VM -Name '"+VMName+"'; if ($vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force }")
	return err
}

// Restore puts the VM back to its clean checkpoint.
func (h HyperV) Restore(ctx context.Context) error {
	_, err := h.run(ctx, "put the VM back to clean", "Restore-VMCheckpoint -VMName '"+VMName+"' -Name '"+CleanCheckpoint+"' -Confirm:$false")
	return err
}

// AttachDVD puts the image at iso in the VM's DVD drive.
func (h HyperV) AttachDVD(ctx context.Context, iso string) error {
	_, err := h.run(ctx, "attach the job's DVD", "Set-VMDvdDrive -VMName '"+VMName+"' -Path $env:"+dvdVariable, dvdVariable+"="+iso)
	return err
}

// Start starts the VM.
func (h HyperV) Start(ctx context.Context) error {
	_, err := h.run(ctx, "start the VM", "Start-VM -Name '"+VMName+"'")
	return err
}

// IsRunning says whether the VM is in any state but off.
func (h HyperV) IsRunning(ctx context.Context) (bool, error) {
	state, err := h.run(ctx, "read the VM's state", "(Get-VM -Name '"+VMName+"').State.ToString()")
	if err != nil {
		return false, err
	}
	return state != "Off", nil
}

// run runs script in Windows PowerShell, stopping at its first error, with
// extra added to the environment, and returns what it wrote, trimmed. Windows
// PowerShell is named by its full path in Windows' own folder, as Windows
// gives it, and runs with powerShellEnvironment alone. A script that runs past hyperVLimit is ended with
// all it started, so a Hyper-V call that hangs never outlasts a job's limits.
func (h HyperV) run(ctx context.Context, doing, script string, extra ...string) (string, error) {
	windowsFolder, err := systemFolder()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, hyperVLimit)
	defer cancel()
	result, err := h.Commands.Run(ctx, execx.Request{
		Name:     filepath.Join(windowsFolder, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		Args:     []string{"-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference = 'Stop'; " + script},
		Env:      append(powerShellEnvironment(windowsFolder), extra...),
		KillTree: true,
	})
	if err != nil {
		return "", fmt.Errorf("cirunner: %s: %w", doing, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("cirunner: %s: exit %d: %s", doing, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// keptVariables are the variables of this process's environment Windows
// PowerShell is given: what it runs with, naming no folder it loads code
// from.
var keptVariables = []string{"TEMP", "TMP", "SystemDrive", "COMPUTERNAME", "USERNAME", "USERDOMAIN", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "OS"}

// powerShellEnvironment is Windows PowerShell's whole environment: Windows'
// own folder, search path and module path, all from windowsFolder as Windows
// gives it, and keptVariables. A user can set any variable for their own
// session, and an elevated command runs in it, so nothing a user set may
// choose what runs: the module path above all, since one left unset puts
// the user's own modules folder first.
func powerShellEnvironment(windowsFolder string) []string {
	system := filepath.Join(windowsFolder, "System32")
	environment := []string{
		"SystemRoot=" + windowsFolder,
		"windir=" + windowsFolder,
		"ComSpec=" + filepath.Join(system, "cmd.exe"),
		"Path=" + strings.Join([]string{system, windowsFolder, filepath.Join(system, "Wbem"), filepath.Join(system, "WindowsPowerShell", "v1.0")}, ";"),
		"PATHEXT=.COM;.EXE",
		"PSModulePath=" + filepath.Join(system, "WindowsPowerShell", "v1.0", "Modules"),
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if slices.ContainsFunc(keptVariables, func(kept string) bool { return strings.EqualFold(kept, name) }) {
			environment = append(environment, entry)
		}
	}
	return environment
}
