package cirunner

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
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
const machineScript = `function Get-Owner([string]$Path) {
    if ($Path -and (Test-Path -LiteralPath $Path)) { (Get-Acl -LiteralPath $Path).GetOwner([Security.Principal.SecurityIdentifier]).Value } else { '' }
}
$service = Get-CimInstance -ClassName Win32_Service -Filter "Name='` + ServiceName + `'"
$program = ''
if ([string]$service.PathName -match '^"([^"]+)"') { $program = $Matches[1] }
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
  vm_guards = @(if ($vm) { Get-VMNetworkAdapter -VM $vm | ForEach-Object { [string]$_.DhcpGuard + ' ' + [string]$_.RouterGuard } })
  vm_rules = @(if ($vm) { Get-VMNetworkAdapterExtendedAcl -VM $vm | ForEach-Object { [pscustomobject]@{ action = [string]$_.Action; direction = [string]$_.Direction; remote = [string]$_.RemoteIPAddress; port = [string]$_.RemotePort } } })
  vm_exchange = @(if ($vm) { Get-VMIntegrationService -VM $vm | Where-Object { $_.Id -like '*` + exchangeID + `' } | ForEach-Object { [string]$_.Enabled } })
  is_folder_inheriting = -not $acl.AreAccessRulesProtected
  folder_access = @($acl.Access | ForEach-Object { $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value })
  service_program = $program
  owners = [ordered]@{
    "the service folder's parent" = Get-Owner (Split-Path -Parent $env:CG_CI_DIR)
    "the service folder" = Get-Owner $env:CG_CI_DIR
    "the App's settings" = Get-Owner (Join-Path $env:CG_CI_DIR 'app.json')
    "the App's key" = Get-Owner (Join-Path $env:CG_CI_DIR 'app-key.pem')
    "the VM's folder" = Get-Owner (Join-Path $env:CG_CI_DIR 'vm')
    "the program's folder" = Get-Owner $(if ($program) { Split-Path -Parent $program } else { '' })
  }
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
	// ServiceProgram is the program the service runs, and Owners the owner of
	// each of the folders and files the service trusts, by what it is.
	ServiceProgram string            `json:"service_program"`
	Owners         map[string]string `json:"owners"`
	// VMGuards are each of the VM's adapters' DHCP and router guards, VMRules
	// its network rules, and VMExchange whether its Key-Value Pair Exchange
	// is on, each as Hyper-V lists them.
	VMGuards   []string `json:"vm_guards"`
	VMRules    []vmRule `json:"vm_rules"`
	VMExchange []string `json:"vm_exchange"`
}

// vmRule is one of the VM's network rules.
type vmRule struct {
	Action    string `json:"action"`
	Direction string `json:"direction"`
	Remote    string `json:"remote"`
	Port      string `json:"port"`
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
	programFiles, folderErr := programFilesFolder()
	if err == nil {
		err = folderErr
	}
	if err != nil {
		findings = append(findings, Finding{Checked: "this machine", Problem: err.Error()})
	} else {
		findings = append(findings, checkMachine(found, programFiles)...)
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
// is not as the install left them, with programFiles the folder the service's
// program must be in.
func checkMachine(found machine, programFiles string) []Finding {
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
	var owned []string
	for _, what := range slices.Sorted(maps.Keys(found.Owners)) {
		if owner := found.Owners[what]; !slices.Contains([]string{"S-1-5-18", "S-1-5-32-544", serviceSID}, owner) {
			owned = append(owned, what+" is owned by "+orNothing(owner))
		}
	}
	if len(found.Owners) != 6 {
		owned = append(owned, fmt.Sprintf("%d of its 6 owners were read", len(found.Owners)))
	}
	relative, relErr := filepath.Rel(programFiles, found.ServiceProgram)
	var closed, opened []string
	for _, rule := range found.VMRules {
		switch {
		case rule.Direction != "Outbound":
		case rule.Action == "Deny":
			closed = append(closed, rule.Remote)
		case rule.Action == "Allow":
			opened = append(opened, rule.Port)
		}
	}
	isNetworkClosed := slices.Equal(found.VMGuards, []string{"On On"}) && slices.Equal(slices.Sorted(slices.Values(closed)), slices.Sorted(strings.SplitSeq(closedRanges, ","))) && slices.Equal(slices.Sorted(slices.Values(opened)), []string{"53", "53", "67"})
	return []Finding{
		finding("the service runs", found.ServiceState == "Running", "it is "+orNothing(found.ServiceState)),
		finding("the service runs as NT SERVICE\\"+ServiceName, strings.EqualFold(found.ServiceAccount, "NT SERVICE\\"+ServiceName), "it runs as "+orNothing(found.ServiceAccount)),
		finding("the service's account is in Hyper-V Administrators", found.IsServiceInHyperVAdministrators, "it is not"),
		finding("the service's folder is open to SYSTEM, Administrators and the service alone", !found.IsFolderInheriting && slices.Equal(access, wantAccess), fmt.Sprintf("it is open to %s, inheriting %v", strings.Join(access, ", "), found.IsFolderInheriting)),
		finding("what the service trusts is owned by SYSTEM, Administrators or the service", len(owned) == 0, strings.Join(owned, ", and ")),
		finding("the service runs its program from "+programFiles, found.ServiceProgram != "" && relErr == nil && filepath.IsLocal(relative), "it runs "+orNothing(found.ServiceProgram)),
		finding("the VM "+VMName+" is here, generation 2, with 4 processors and 6 GB at most", found.IsVM && found.VMGeneration == 2 && found.VMProcessors == 4 && found.VMMemoryMaximum == 6<<30, fmt.Sprintf("is here %v, generation %d, %d processors, %d bytes at most", found.IsVM, found.VMGeneration, found.VMProcessors, found.VMMemoryMaximum)),
		finding("the VM's "+CleanCheckpoint+" checkpoint is here", found.IsCheckpoint, "it is not"),
		finding("the VM's jobs reach the host and the LAN by DNS and DHCP alone, and the VM may not answer as a DHCP server or a router", isNetworkClosed, fmt.Sprintf("its guards are %s, it closes %s, and opens %s", orNothing(strings.Join(found.VMGuards, ", ")), orNothing(strings.Join(closed, ", ")), orNothing(strings.Join(opened, ", ")))),
		finding("the VM's Key-Value Pair Exchange, which tells the VM the host's name, is off", slices.Equal(found.VMExchange, []string{"False"}), "its Enabled reads "+orNothing(strings.Join(found.VMExchange, ", "))),
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
