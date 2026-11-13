package cirunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// The uninstall turns the VM off only when it is on, since Stop-VM refuses a
// VM that is off already, as the VM is between jobs, and removes it either
// way. With no VM it does nothing.
func TestRemoveVMStopsOnlyAVMThatRuns(t *testing.T) {
	for name, row := range map[string]struct{ state, want string }{
		"a VM that is off":  {state: "Off", want: "removed"},
		"a VM that runs":    {state: "Running", want: "stopped\r\nremoved"},
		"a VM that is gone": {},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			const stubs = `function Get-VM { [CmdletBinding()] param([string]$Name) if ($env:CG_TEST_STATE) { [pscustomobject]@{ Name = $Name; State = $env:CG_TEST_STATE } } }
function Stop-VM { [CmdletBinding()] param($VM, [switch]$TurnOff, [switch]$Force) if ($VM.State -eq 'Off') { throw 'Stop-VM cannot stop a VM that is off' } 'stopped' }
function Remove-VM { [CmdletBinding()] param($VM, [switch]$Force) 'removed' }`

			// Act
			output, err := runStubbed(t, stubs, removeVMScript, "CG_TEST_STATE="+row.state)

			// Assert
			if err != nil || output != row.want {
				t.Errorf("the VM's removal = %v, saying %q; want %q", err, output, row.want)
			}
		})
	}
}

// The service leaves Hyper-V Administrators by its SID, and an uninstall run
// again, after it left, goes on, while any other refusal stops it.
func TestRemoveServiceTakesItsSIDOutOfHyperVAdministrators(t *testing.T) {
	for name, row := range map[string]struct {
		refusal string
		isDone  bool
	}{
		"a member":           {isDone: true},
		"a member no longer": {refusal: "MemberNotFound", isDone: true},
		"access denied":      {refusal: "AccessDenied"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			const stubs = `function Remove-LocalGroupMember { [CmdletBinding()] param([string]$SID, [string]$Member)
    if ($env:CG_TEST_REFUSAL) { $PSCmdlet.WriteError((New-Object Management.Automation.ErrorRecord ([Exception]'refused'), $env:CG_TEST_REFUSAL, 'NotSpecified', $Member)); return }
    "left $SID as $Member"
}
function Get-Service { [CmdletBinding()] param([string]$Name) }`
			service := ServiceSID(ServiceName)

			// Act
			output, err := runStubbed(t, stubs, removeServiceScript, "CG_CI_SERVICE_SID="+service, "CG_TEST_REFUSAL="+row.refusal)

			// Assert
			if row.isDone && (err != nil || (row.refusal == "") != (output == "left S-1-5-32-578 as "+service)) {
				t.Errorf("the service's removal = %v, saying %q; want it done, the service's SID taken out of S-1-5-32-578", err, output)
			}
			if !row.isDone && (err == nil || !strings.Contains(err.Error(), "refused")) {
				t.Errorf("the service's removal = %v, saying %q; want it stopped", err, output)
			}
		})
	}
}

// The uninstall runs each script with its folder in the environment. A
// folder already gone means no runners to delete, and an App GitHub says is
// gone means none can be, which the uninstall says and goes on past. Any
// other failure to delete them stops the uninstall before the key goes.
func TestUninstallDeletesTheRunnersWhileTheKeyIsHere(t *testing.T) {
	const tokenCall = "POST /app/installations/2/access_tokens"
	for name, row := range map[string]struct {
		deleteErr error
		wantRan   int
		wantErr   bool
		isByHand  bool
	}{
		"deleted":                         {wantRan: 3},
		"the folder gone":                 {deleteErr: fmt.Errorf("read: %w", fs.ErrNotExist), wantRan: 3},
		"the App's key gone":              {deleteErr: fmt.Errorf("list: %w", statusError{call: tokenCall, status: 401}), wantRan: 3, isByHand: true},
		"the App's installation gone":     {deleteErr: statusError{call: tokenCall, status: 404}, wantRan: 3, isByHand: true},
		"the repository out of its reach": {deleteErr: statusError{call: tokenCall, status: 422}, wantRan: 3, isByHand: true},
		"GitHub failing":                  {deleteErr: statusError{call: tokenCall, status: 503}, wantRan: 1, wantErr: true},
		"a runner it may not delete":      {deleteErr: statusError{call: "DELETE /repos/o/r/actions/runners/7", status: 404}, wantRan: 1, wantErr: true},
		"GitHub out of reach":             {deleteErr: errors.New("dial tcp: no route to host"), wantRan: 1, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			commands := &recorder{}
			var deleted []string
			dir := `C:\ProgramData\Code Goblins\ci-runner`
			options := uninstallOptions(dir, commands, row.deleteErr, &deleted)
			var said bytes.Buffer
			options.Out = &said
			steps := UninstallSteps(options)

			// Act
			err := RunSteps(context.Background(), steps, false, &said)

			// Assert
			if (err != nil) != row.wantErr || len(commands.ran) != row.wantRan || !slices.Equal(deleted, []string{dir}) {
				t.Errorf("uninstall = %v, ran %d script(s), deleted in %q; want error %v and %d script(s)", err, len(commands.ran), deleted, row.wantErr, row.wantRan)
			}
			if isByHand := strings.Contains(said.String(), "delete any runner named "+RunnerPrefix+"... by hand"); isByHand != row.isByHand {
				t.Errorf("the uninstall said:\n%s\nwant the runners left to delete by hand said %v", said.String(), row.isByHand)
			}
			for _, request := range commands.ran {
				if strings.Contains(script(request), dir) {
					t.Errorf("the script %q holds the folder; want it in the environment", script(request))
				}
			}
			if row.wantRan == 3 && !slices.Contains(commands.ran[2].Env, "CG_CI_DIR="+dir) {
				t.Errorf("the folder's removal does not name the folder in its environment")
			}
		})
	}
}
