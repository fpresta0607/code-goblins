package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"slices"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// The uninstall's scripts, each of which leaves alone what is already gone,
// so an uninstall stopped part way can be run again. The service leaves
// Hyper-V Administrators by its SID while it still has its account, and
// without a listing of the group's members, which fails on a member whose
// account is gone.
const (
	removeServiceScript = `try { Remove-LocalGroupMember -SID 'S-1-5-32-578' -Member $env:CG_CI_SERVICE_SID }
catch { if ($_.FullyQualifiedErrorId -notlike 'MemberNotFound,*') { throw } }
if (Get-Service -Name '` + ServiceName + `' -ErrorAction SilentlyContinue) {
  Stop-Service -Name '` + ServiceName + `' -Force
  & (Join-Path $env:SystemRoot 'System32\sc.exe') delete ` + ServiceName + `
  if ($LASTEXITCODE -ne 0) { throw ('sc.exe delete failed with exit ' + $LASTEXITCODE) }
}`
	removeVMScript = `$vm = Get-VM -Name '` + VMName + `' -ErrorAction SilentlyContinue
if ($vm) {
  if ($vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force }
  Remove-VM -VM $vm -Force
}`
	removeFolderScript = `if (Test-Path -LiteralPath $env:CG_CI_DIR) { Remove-Item -LiteralPath $env:CG_CI_DIR -Recurse -Force }`
)

// UninstallOptions are what the uninstall is given: the service's folder,
// the commands that run Windows PowerShell, how to delete the runners the
// service registered at GitHub, with the settings and key in dir, and where
// to say what it could not do.
type UninstallOptions struct {
	Dir              string
	Commands         execx.Runner
	DeleteOwnRunners func(ctx context.Context, dir string) error
	Out              io.Writer
}

// tokenCall is GitHub's call that gives the App a token for its
// installation, and appGone the answers to it that say the App, its key or
// its installation is gone, or no longer reaches the repository.
var (
	tokenCall = regexp.MustCompile(`^POST /app/installations/\d+/access_tokens$`)
	appGone   = []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity}
)

// isAppGone says whether err is GitHub refusing the App a token because the
// App, its key or its installation is gone: then no runner it registered can
// be deleted with it, and only by hand.
func isAppGone(err error) bool {
	var refused statusError
	return errors.As(err, &refused) && tokenCall.MatchString(refused.call) && slices.Contains(appGone, refused.status)
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
			Says:  []string{"delete every runner named " + RunnerPrefix + "... with the App whose settings and key are in " + o.Dir + ", unless the folder is gone, or GitHub says the App is"},
			Run: func(ctx context.Context) error {
				err := o.DeleteOwnRunners(ctx, o.Dir)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					return nil
				case isAppGone(err):
					fmt.Fprintf(o.Out, "    %v, so the App is gone and the uninstall goes on: delete any runner named %s... by hand, in the repository's Settings, under Actions, Runners\n", err, RunnerPrefix)
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
