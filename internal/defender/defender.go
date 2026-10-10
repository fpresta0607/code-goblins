// Package defender reads what Microsoft Defender recorded of the fleet's own
// work: its detections and the samples it sent to Microsoft. It only reads,
// with Get-MpThreatDetection, Get-MpThreat and the log
// Microsoft-Windows-Windows Defender/Operational. Nothing in the fleet scans
// with Defender, restores a file, or changes a setting, an exclusion or the
// allow list: those are the Supreme Overlord's alone.
package defender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Detection is one detection as Defender recorded it. It holds names, paths
// and a time, never a command line's text.
type Detection struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Threat  string    `json:"threat"`
	Process string    `json:"process"`
	// Files are the files the detection names.
	Files []string `json:"files"`
	// CommandLines is how many command lines the detection names.
	CommandLines int `json:"command_lines"`
}

// Upload is one sample Defender sent to Microsoft (event 2050).
type Upload struct {
	Time   time.Time `json:"time"`
	File   string    `json:"file"`
	SHA256 string    `json:"sha256"`
}

// Report is what Defender recorded since a time.
type Report struct {
	Detections []Detection `json:"detections"`
	Uploads    []Upload    `json:"uploads"`
	// Elsewhere counts the detections and uploads Under left out.
	Elsewhere int `json:"elsewhere"`
}

// readScript prints, as one JSON object, every detection first seen at or
// after the time it is given and every sample uploaded since. A detection's
// command lines are counted and never printed: one held a gate run's whole
// intent.
const readScript = `$ErrorActionPreference = 'Stop'
$from = [datetime]::Parse('%s', [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AdjustToUniversal)
$names = @{}
Get-MpThreat | ForEach-Object { $names[[string]$_.ThreatID] = [string]$_.ThreatName }
$detections = @(Get-MpThreatDetection | Where-Object { $_.InitialDetectionTime.ToUniversalTime() -ge $from } | Sort-Object InitialDetectionTime | ForEach-Object {
    [pscustomobject]@{
        id            = [string]$_.DetectionID
        time          = $_.InitialDetectionTime.ToUniversalTime().ToString('o')
        threat        = [string]$names[[string]$_.ThreatID]
        process       = [string]$_.ProcessName
        files         = @($_.Resources | Where-Object { $_ -like 'file:_*' } | ForEach-Object { $_.Substring(6) })
        command_lines = @($_.Resources | Where-Object { $_ -like 'CmdLine:_*' }).Count
    }
})
$events = @()
try {
    $events = @(Get-WinEvent -FilterHashtable @{ LogName = 'Microsoft-Windows-Windows Defender/Operational'; Id = 2050; StartTime = $from.ToLocalTime() } -ErrorAction Stop)
}
catch {
    if ($_.FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*') { throw }
}
$uploads = @($events | Sort-Object TimeCreated | ForEach-Object {
    $data = @{}
    ([xml]$_.ToXml()).Event.EventData.Data | ForEach-Object { $data[$_.Name] = $_.'#text' }
    [pscustomobject]@{ time = $_.TimeCreated.ToUniversalTime().ToString('o'); file = [string]$data['Filename']; sha256 = [string]$data['Sha256'] }
})
[pscustomobject]@{ detections = $detections; uploads = $uploads } | ConvertTo-Json -Depth 5 -Compress
`

// Read returns what Defender recorded since a time, read with commands. A
// read Defender did not answer is an error, never an empty report.
func Read(ctx context.Context, commands execx.Runner, since time.Time) (Report, error) {
	script := fmt.Sprintf(readScript, since.UTC().Format(time.RFC3339))
	result, err := commands.Run(ctx, execx.Request{Name: "powershell.exe", Args: []string{"-NoProfile", "-NonInteractive", "-Command", script}})
	if err != nil {
		return Report{}, fmt.Errorf("read Microsoft Defender's records: %w", err)
	}
	if result.ExitCode != 0 {
		return Report{}, fmt.Errorf("read Microsoft Defender's records: exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var read struct {
		Detections *[]Detection `json:"detections"`
		Uploads    *[]Upload    `json:"uploads"`
	}
	if err := json.Unmarshal(result.Stdout, &read); err != nil {
		return Report{}, fmt.Errorf("read Microsoft Defender's records: %w", err)
	}
	if read.Detections == nil || read.Uploads == nil {
		return Report{}, errors.New("read Microsoft Defender's records: the read printed no report")
	}
	return Report{Detections: *read.Detections, Uploads: *read.Uploads}, nil
}

// Under returns the detections and uploads that name a file in one of
// folders or in a Go test's temporary folder, which a gate's test step makes
// in the user's own temporary folder, and each detection that names no file
// at all, which is one of command lines. It counts the rest in Elsewhere and
// does not name them: what Defender found elsewhere on the machine is not
// the fleet's to list.
func (report Report) Under(folders []string) Report {
	under := Report{}
	for _, detection := range report.Detections {
		if len(detection.Files) == 0 || isFleets(folders, append([]string{detection.Process}, detection.Files...)...) {
			under.Detections = append(under.Detections, detection)
			continue
		}
		under.Elsewhere++
	}
	for _, upload := range report.Uploads {
		if isFleets(folders, upload.File) {
			under.Uploads = append(under.Uploads, upload)
			continue
		}
		under.Elsewhere++
	}
	return under
}

// Folders are the folders the fleet's own work writes in on this machine:
// the home at root, the folder on a Dev Drive that holds its worktrees and
// scratch, the fleet's folders in the user's local application data, and
// no-mistakes' own, where a gate keeps its worktrees.
func Folders(root, devDrive string) []string {
	folders := []string{root, devDrive}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		folders = append(folders, filepath.Join(local, "cfo"), filepath.Join(local, "CodeGoblins"))
	}
	if profile, err := os.UserHomeDir(); err == nil {
		folders = append(folders, filepath.Join(profile, ".no-mistakes"))
	}
	return folders
}

// isFleets reports whether one of paths is in one of folders or in a Go
// test's temporary folder.
func isFleets(folders []string, paths ...string) bool {
	if within(folders, paths...) {
		return true
	}
	for _, path := range paths {
		if OriginOf(path).Test != "" {
			return true
		}
	}
	return false
}

// within reports whether one of paths is in one of folders, whatever the
// case of either.
func within(folders []string, paths ...string) bool {
	for _, path := range paths {
		path = strings.ToLower(filepath.Clean(path))
		for _, folder := range folders {
			if folder == "" {
				continue
			}
			folder = strings.ToLower(filepath.Clean(folder))
			if path == folder || strings.HasPrefix(path, folder+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// Origin is what a path says of who made the file: the task whose folder it
// is in, the test whose temporary folder it is in, or the gate run whose
// worktree it is in.
type Origin struct {
	Task string
	Test string
	Gate string
}

// String says the origin in words, or nothing when the path says none.
func (origin Origin) String() string {
	switch {
	case origin.Gate != "":
		return "gate run " + origin.Gate
	case origin.Test != "" && origin.Task != "":
		return "test " + origin.Test + " of task " + origin.Task
	case origin.Test != "":
		return "test " + origin.Test
	case origin.Task != "":
		return "task " + origin.Task
	}
	return ""
}

// testFolder is a folder go test's t.TempDir makes: the test's name and a
// number.
var testFolder = regexp.MustCompile(`^(Test[A-Z][A-Za-z0-9_]*?)_?[0-9]{4,}$`)

// taskFolders are the folders whose next element is a task's own folder,
// each with how many elements lie between it and that folder.
var taskFolders = map[string]int{"scratch": 0, "tasktmp": 0, "gotmp": 1, "worktrees": 1}

// notTasks are the elements of a scratch folder that are no task's.
var notTasks = map[string]bool{".tmp": true, "candidates": true}

// OriginOf reads the origin of the file at path from its folders: a goblin's
// scratch, temporary and worktree folders hold its task's id, a test's
// temporary folder holds the test's name, and a gate's worktree holds its
// run's id.
func OriginOf(path string) Origin {
	elements := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	var origin Origin
	for index, element := range elements[:len(elements)-1] {
		if match := testFolder.FindStringSubmatch(element); match != nil && origin.Test == "" {
			origin.Test = match[1]
		}
		if element == ".no-mistakes" && index+3 < len(elements) && elements[index+1] == "worktrees" {
			return Origin{Gate: elements[index+3]}
		}
		skip, isTaskFolder := taskFolders[strings.ToLower(element)]
		if at := index + 1 + skip; isTaskFolder && origin.Task == "" && at < len(elements)-1 && !notTasks[elements[at]] && !testFolder.MatchString(elements[at]) {
			origin.Task = elements[at]
		}
	}
	return origin
}
