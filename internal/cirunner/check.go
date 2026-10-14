package cirunner

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Finding is one thing the check looked at, and what is wrong with it, or
// nothing when it is as the install left it.
type Finding struct {
	Checked string
	Problem string
}

// appPermissions are the App's permissions, all of them: what the service
// needs, and the metadata every App reads.
var appPermissions = map[string]string{"administration": "write", "actions": "read", "metadata": "read"}

// machineScript reads, as JSON, what the install made on this machine.
const machineScript = `$service = Get-CimInstance -ClassName Win32_Service -Filter "Name='` + ServiceName + `'"
$vm = Get-VM -Name '` + VMName + `' -ErrorAction SilentlyContinue
$members = @(Get-LocalGroupMember -SID 'S-1-5-32-578' | ForEach-Object { $_.SID.Value })
$acl = Get-Acl -LiteralPath $env:CG_CI_DIR
[pscustomobject]@{
  service_state = [string]$service.State
  service_account = [string]$service.StartName
  is_service_in_hyperv_administrators = ($members -contains $env:CG_CI_SERVICE_SID)
  is_vm = [bool]$vm
  vm_generation = [int]$vm.Generation
  vm_processors = [int]$vm.ProcessorCount
  vm_memory_maximum = [int64]$vm.MemoryMaximum
  is_checkpoint = [bool](Get-VMSnapshot -VMName '` + VMName + `' -Name '` + CleanCheckpoint + `' -ErrorAction SilentlyContinue)
  is_folder_inheriting = -not $acl.AreAccessRulesProtected
  folder_access = @($acl.Access | ForEach-Object { $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value })
} | ConvertTo-Json -Compress`

// machine is what machineScript reads.
type machine struct {
	ServiceState                    string   `json:"service_state"`
	ServiceAccount                  string   `json:"service_account"`
	IsServiceInHyperVAdministrators bool     `json:"is_service_in_hyperv_administrators"`
	IsVM                            bool     `json:"is_vm"`
	VMGeneration                    int      `json:"vm_generation"`
	VMProcessors                    int      `json:"vm_processors"`
	VMMemoryMaximum                 int64    `json:"vm_memory_maximum"`
	IsCheckpoint                    bool     `json:"is_checkpoint"`
	IsFolderInheriting              bool     `json:"is_folder_inheriting"`
	FolderAccess                    []string `json:"folder_access"`
}

// CheckOptions are what the check reads: the service's folder and its one
// repository, the commands that run Windows PowerShell, the App as
// installed, and the service's pipe.
type CheckOptions struct {
	Dir          string
	Repo         string
	Commands     execx.Runner
	Installation func(ctx context.Context) (Installation, error)
	Ask          func(ctx context.Context, request Request, reply func(Reply)) error
}

// Check reads what the install made and says what is not as it left it.
func Check(ctx context.Context, o CheckOptions) []Finding {
	var findings []Finding
	output, err := powerShell(ctx, o.Commands, installStepLimit, "read what the install made", machineScript, "CG_CI_DIR="+o.Dir, "CG_CI_SERVICE_SID="+ServiceSID(ServiceName))
	var found machine
	if err == nil {
		err = json.Unmarshal([]byte(output), &found)
	}
	if err != nil {
		findings = append(findings, Finding{Checked: "this machine", Problem: err.Error()})
	} else {
		findings = append(findings, checkMachine(found)...)
	}
	installation, err := o.Installation(ctx)
	if err != nil {
		findings = append(findings, Finding{Checked: "the App", Problem: err.Error()})
	} else {
		findings = append(findings, checkApp(installation, o.Repo))
	}
	var status string
	err = o.Ask(ctx, Request{Kind: AskStatus}, func(reply Reply) { status = reply.Line })
	if err != nil {
		findings = append(findings, Finding{Checked: "the service's pipe", Problem: err.Error()})
	} else {
		findings = append(findings, Finding{Checked: "the service's pipe, which says " + status})
	}
	return findings
}

// checkMachine says what of the service, its account, its folder and its VM
// is not as the install left them.
func checkMachine(found machine) []Finding {
	serviceSID := ServiceSID(ServiceName)
	finding := func(checked string, isRight bool, problem string) Finding {
		if isRight {
			return Finding{Checked: checked}
		}
		return Finding{Checked: checked, Problem: problem}
	}
	access := slices.Clone(found.FolderAccess)
	sort.Strings(access)
	access = slices.Compact(access)
	wantAccess := []string{"S-1-5-18", "S-1-5-32-544", serviceSID}
	sort.Strings(wantAccess)
	return []Finding{
		finding("the service runs", found.ServiceState == "Running", "it is "+orNothing(found.ServiceState)),
		finding("the service runs as NT SERVICE\\"+ServiceName, strings.EqualFold(found.ServiceAccount, "NT SERVICE\\"+ServiceName), "it runs as "+orNothing(found.ServiceAccount)),
		finding("the service's account is in Hyper-V Administrators", found.IsServiceInHyperVAdministrators, "it is not"),
		finding("the service's folder is open to SYSTEM, Administrators and the service alone", !found.IsFolderInheriting && slices.Equal(access, wantAccess), fmt.Sprintf("it is open to %s, inheriting %v", strings.Join(access, ", "), found.IsFolderInheriting)),
		finding("the VM "+VMName+" is here, generation 2, with 4 processors and 6 GB at most", found.IsVM && found.VMGeneration == 2 && found.VMProcessors == 4 && found.VMMemoryMaximum == 6<<30, fmt.Sprintf("is here %v, generation %d, %d processors, %d bytes at most", found.IsVM, found.VMGeneration, found.VMProcessors, found.VMMemoryMaximum)),
		finding("the VM's "+CleanCheckpoint+" checkpoint is here", found.IsCheckpoint, "it is not"),
	}
}

// checkApp says whether the App may do more than the service needs: any
// permission past its own, or any repository but repo.
func checkApp(installation Installation, repo string) Finding {
	const checked = "the App may administer runners and read Actions on its one repository, and nothing more"
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(installation.Permissions)) {
		if appPermissions[name] != installation.Permissions[name] {
			problems = append(problems, fmt.Sprintf("it has %s %s", name, installation.Permissions[name]))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(appPermissions)) {
		if _, has := installation.Permissions[name]; !has {
			problems = append(problems, "it lacks "+name+" "+appPermissions[name])
		}
	}
	if installation.Selection != "selected" || !slices.Equal(installation.Repositories, []string{repo}) {
		problems = append(problems, fmt.Sprintf("it reaches %s repositories: %s", orNothing(installation.Selection), strings.Join(installation.Repositories, ", ")))
	}
	return Finding{Checked: checked, Problem: strings.Join(problems, ", and ")}
}

func orNothing(text string) string {
	if text == "" {
		return "nothing"
	}
	return text
}
