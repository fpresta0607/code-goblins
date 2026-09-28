package lifecycle

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOwnedProcessesRequireTaskEvidence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gb-task")
	started := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		processes []Process
		want      []int
	}{
		{"worktree server", []Process{{PID: 1, Name: "node.exe", Started: started, Directory: root}}, []int{1}},
		{"scratch test", []Process{{PID: 2, Name: "test.exe", Started: started, Directory: filepath.Join(root, "scratch")}}, []int{2}},
		{"sibling prefix is unrelated", []Process{{PID: 3, Name: "node.exe", Started: started, Directory: root + "-other"}}, nil},
		{"command argument names task script", []Process{{PID: 4, Name: "node.exe", Started: started, Arguments: []string{"node", filepath.Join(root, "server.js")}}}, []int{4}},
		{"prose mentioning path is not ownership", []Process{{PID: 5, Name: "node.exe", Started: started, Arguments: []string{"node", "talk about " + root}}}, nil},
		{"editor viewing a task file is unrelated", []Process{{PID: 15, Name: "Code.exe", Started: started, Arguments: []string{"code", filepath.Join(root, "server.js")}}}, nil},
		{"visible browser viewing a task file is unrelated", []Process{{PID: 16, Name: "chrome.exe", Started: started, Arguments: []string{"chrome", filepath.Join(root, "review.html")}}}, nil},
		{"unknown creation time cannot authorize a kill", []Process{{PID: 6, Name: "node.exe", Directory: root}}, nil},
		{"parent pid alone is never enough", []Process{{PID: 7, Name: "claude.exe", Started: started, Directory: root}, {PID: 8, ParentPID: 7, Name: "chrome.exe", Started: started.Add(-time.Hour)}}, []int{7}},
		{"new child without task evidence is not inferred", []Process{{PID: 9, Name: "claude.exe", Started: started, Directory: root}, {PID: 10, ParentPID: 9, Name: "chrome.exe", Started: started.Add(time.Second)}}, []int{9}},
		{"detached browser bridge still belongs to worktree", []Process{{PID: 11, ParentPID: 900, Name: "node.exe", Started: started, Directory: root, Arguments: []string{"node", "chrome-devtools-axi/bridge.js"}}, {PID: 12, ParentPID: 901, Name: "chrome.exe", Started: started, Arguments: []string{"chrome", "--headless", "--user-data-dir=" + filepath.Join(root, "browser")}}}, []int{11, 12}},
	} {
		t.Run(test.name, func(t *testing.T) {
			owned := OwnedProcesses(test.processes, []string{root}, nil)
			var got []int
			for _, process := range owned {
				got = append(got, process.PID)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selected %v, want %v", got, test.want)
			}
		})
	}
}

func TestOwnedProcessesAcceptVerifiedJobMembersButRejectReusedPIDs(t *testing.T) {
	started := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	processes := []Process{{PID: 42, Name: "node.exe", Started: started}, {PID: 43, Name: "chrome.exe", Started: started.Add(time.Hour)}}
	job := []Identity{{PID: 42, Started: started}, {PID: 43, Started: started}}
	owned := OwnedProcesses(processes, nil, job)
	if len(owned) != 1 || owned[0].PID != 42 {
		t.Fatalf("job membership authorized a reused PID: %+v", owned)
	}
}

func TestOwnedProcessesNeverTreatRelativeOrRootDirectoryAsOwnership(t *testing.T) {
	started := time.Now()
	root := t.TempDir()
	process := Process{PID: 42, Name: "node.exe", Started: started, Directory: root}
	for _, directory := range []string{"", ".", filepath.VolumeName(root) + string(filepath.Separator)} {
		if got := OwnedProcesses([]Process{process}, []string{directory}, nil); len(got) != 0 {
			t.Fatalf("unsafe ownership root %q selected %+v", directory, got)
		}
	}
}
