package cirunner

import (
	"context"
	"errors"
	"io/fs"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// The uninstall's scripts, each of which leaves alone what is already gone,
// so an uninstall stopped part way can be run again.
const (
	removeServiceScript = `if (Get-Service -Name '` + ServiceName + `' -ErrorAction SilentlyContinue) {
  Stop-Service -Name '` + ServiceName + `' -Force
  & sc.exe delete ` + ServiceName + `
  if ($LASTEXITCODE -ne 0) { throw ('sc.exe delete failed with exit ' + $LASTEXITCODE) }
}
$member = Get-LocalGroupMember -SID 'S-1-5-32-578' | Where-Object { $_.SID.Value -eq $env:CG_CI_SERVICE_SID }
if ($member) { Remove-LocalGroupMember -SID 'S-1-5-32-578' -Member $member }`
	removeVMScript = `$vm = Get-VM -Name '` + VMName + `' -ErrorAction SilentlyContinue
if ($vm) {
  Stop-VM -VM $vm -TurnOff -Force
  Remove-VM -VM $vm -Force
}`
	removeFolderScript = `if (Test-Path -LiteralPath $env:CG_CI_DIR) { Remove-Item -LiteralPath $env:CG_CI_DIR -Recurse -Force }`
)

// UninstallOptions are what the uninstall is given: the service's folder,
// the commands that run Windows PowerShell, and how to delete the runners
// the service registered at GitHub, with the settings and key in dir.
type UninstallOptions struct {
	Dir              string
	Commands         execx.Runner
	DeleteOwnRunners func(ctx context.Context, dir string) error
}

// UninstallSteps undo the install: the service first, so nothing starts a
// job meanwhile, then its runners at GitHub, while its key is still here,
// then the VM, then the service's folder, with the key and the VM's disk.
func UninstallSteps(o UninstallOptions) []Step {
	serviceEnv := []string{"CG_CI_SERVICE_SID=" + ServiceSID(ServiceName)}
	folderEnv := []string{"CG_CI_DIR=" + o.Dir}
	return []Step{
		{
			Title: "remove the service and its account from Hyper-V Administrators",
			Says:  scriptSays(removeServiceScript, serviceEnv...),
			Run:   runScript(o.Commands, "remove the service", removeServiceScript, serviceEnv...),
		},
		{
			Title: "delete the service's runners at GitHub",
			Says:  []string{"delete every runner named " + RunnerPrefix + "... with the App whose settings and key are in " + o.Dir + ", unless the folder is gone"},
			Run: func(ctx context.Context) error {
				err := o.DeleteOwnRunners(ctx, o.Dir)
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			},
		},
		{
			Title: "remove the VM and its checkpoint",
			Says:  scriptSays(removeVMScript),
			Run:   runScript(o.Commands, "remove the VM", removeVMScript),
		},
		{
			Title: "remove the service's folder, with the App's key and the VM's disk",
			Says:  scriptSays(removeFolderScript, folderEnv...),
			Run:   runScript(o.Commands, "remove the service's folder", removeFolderScript, folderEnv...),
		},
	}
}

// DeleteOwnRunnersIn deletes, at GitHub, every runner the service in dir
// registered, with the App whose settings and key are there. A folder or
// settings already gone is fs.ErrNotExist.
func DeleteOwnRunnersIn(ctx context.Context, dir string) error {
	service, _, err := NewService(dir, func(string) {})
	if err != nil {
		return err
	}
	return service.Deps.GitHub.DeleteOwnRunners(ctx)
}
