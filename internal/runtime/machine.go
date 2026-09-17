package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// utf8OutputPrelude forces the child's console output encoding to UTF-8, for
// the same reason internal/reap does: Go spawns powershell with a pipe, so
// PowerShell encodes stdout with the OEM code page, and a command line
// holding any character outside it comes back mangled into bytes that
// encoding/json rejects outright.
const utf8OutputPrelude = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; `

// listenerPortFloor skips the ephemeral and system ranges. Below 1024 is
// Windows' own; at or above 60000 is the dynamic range a client port lands
// in, and neither is a dev server somebody left running.
const (
	listenerPortFloor   = 1024
	listenerPortCeiling = 60000
)

// listenerScript reads every listening TCP socket with the identity of the
// process behind it, in one query. Get-NetTCPConnection reports the same port
// once per address family, so the rows are keyed by port and pid.
const listenerScript = utf8OutputPrelude + `
$rows = @{}
foreach ($c in Get-NetTCPConnection -State Listen) {
  if ($c.LocalPort -lt %d -or $c.LocalPort -ge %d) { continue }
  $key = "$($c.LocalPort)/$($c.OwningProcess)"
  if ($rows.ContainsKey($key)) { continue }
  $rows[$key] = [pscustomobject]@{ port = [int]$c.LocalPort; address = [string]$c.LocalAddress; pid = [int]$c.OwningProcess }
}
$procs = @{}
foreach ($p in Get-CimInstance Win32_Process) { $procs[[int]$p.ProcessId] = $p }
$out = foreach ($r in $rows.Values) {
  $p = $procs[[int]$r.pid]
  [pscustomobject]@{ port = $r.port; address = $r.address; pid = $r.pid; name = $(if ($p) { [string]$p.Name } else { '' }); cmd = $(if ($p) { [string]$p.CommandLine } else { '' }) }
}
ConvertTo-Json -InputObject @($out) -Compress -Depth 3`

// machineScript reads memory, the disk holding the CFO home, and the WSL
// virtual machine's footprint, in one query.
//
// WSL's memory never appears in any Windows per-process accounting a
// container or a harness shows up in: it is one opaque vmmem process holding
// everything running inside the Linux VM, Docker's containers included. It
// has reached eleven gigabytes on this machine while every Windows-side
// reading looked fine, which is exactly why it is read separately here.
const machineScript = utf8OutputPrelude + `
$os = Get-CimInstance Win32_OperatingSystem
$disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='%s'"
$wsl = Get-Process -Name vmmem,vmmemWSL -ErrorAction SilentlyContinue | Measure-Object -Property WorkingSet64 -Sum
[pscustomobject]@{
  memory_total = [int64]$os.TotalVisibleMemorySize * 1024
  memory_available = [int64]$os.FreePhysicalMemory * 1024
  disk_name = [string]$disk.DeviceID
  disk_total = [int64]$disk.Size
  disk_free = [int64]$disk.FreeSpace
  wsl = [int64]$wsl.Sum
} | ConvertTo-Json -Compress`

// System reads the machine's own state through PowerShell.
type System struct {
	Commands execx.Runner
}

// Listeners returns every listening socket worth attributing, each already
// paired with the directory its process is running in.
//
// The working directory is read from each process's own parameter block
// rather than parsed out of its command line, because the command line does
// not have it. A server started as `next start -p 3300` names no path at all,
// and that is not a corner case: it is how every dev server started from
// inside its own directory looks, which is every dev server a goblin starts.
func (s System) Listeners(ctx context.Context) ([]Listener, error) {
	raw, err := s.run(ctx, fmt.Sprintf(listenerScript, listenerPortFloor, listenerPortCeiling))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Port    int    `json:"port"`
		Address string `json:"address"`
		PID     int    `json:"pid"`
		Name    string `json:"name"`
		Cmd     string `json:"cmd"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("runtime: decode listeners: %w", err)
	}
	listeners := make([]Listener, 0, len(rows))
	for _, row := range rows {
		listener := Listener{
			Port:    row.Port,
			Address: row.Address,
			PID:     row.PID,
			Process: row.Name,
			Command: row.Cmd,
		}
		directory, err := proc.WorkingDirectory(row.PID)
		if err != nil {
			listener.WorkDirError = workDirReason(err)
		} else {
			listener.WorkDir = strings.TrimRight(directory, `\`)
		}
		listeners = append(listeners, listener)
	}
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].Port != listeners[j].Port {
			return listeners[i].Port < listeners[j].Port
		}
		return listeners[i].PID < listeners[j].PID
	})
	return listeners, nil
}

// workDirReason reduces a read failure to the short phrase the report prints
// in place of a directory. The failure is almost always one of two things: a
// process running at a higher privilege, or one that exited between the
// socket listing and the read.
func workDirReason(err error) string {
	if strings.Contains(err.Error(), "Access is denied") {
		return "not readable at this privilege"
	}
	if errors.Is(err, proc.ErrDirectoryUnreadable) {
		return "unreadable"
	}
	return "unreadable: " + err.Error()
}

// Machine reads memory, disk and the WSL footprint. disk is the drive the
// report accounts for, as a DeviceID such as "C:".
func (s System) Machine(ctx context.Context, disk string) (Machine, error) {
	raw, err := s.run(ctx, fmt.Sprintf(machineScript, disk))
	if err != nil {
		return Machine{}, err
	}
	var row struct {
		MemoryTotal     int64  `json:"memory_total"`
		MemoryAvailable int64  `json:"memory_available"`
		DiskName        string `json:"disk_name"`
		DiskTotal       int64  `json:"disk_total"`
		DiskFree        int64  `json:"disk_free"`
		WSL             int64  `json:"wsl"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return Machine{}, fmt.Errorf("runtime: decode machine reading: %w", err)
	}
	return Machine{
		MemoryTotal:     row.MemoryTotal,
		MemoryAvailable: row.MemoryAvailable,
		DiskName:        row.DiskName,
		DiskTotal:       row.DiskTotal,
		DiskFree:        row.DiskFree,
		WSL:             row.WSL,
	}, nil
}

func (s System) run(ctx context.Context, script string) ([]byte, error) {
	if s.Commands == nil {
		return nil, errors.New("runtime: command runner is required")
	}
	result, err := s.Commands.Run(ctx, execx.Request{
		Name: "powershell",
		Args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script},
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: read machine state: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("runtime: read machine state exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	trimmed := strings.TrimSpace(string(result.Stdout))
	if trimmed == "" {
		return nil, errors.New("runtime: machine state query returned no output")
	}
	return []byte(trimmed), nil
}
