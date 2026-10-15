package cirunner

import (
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
