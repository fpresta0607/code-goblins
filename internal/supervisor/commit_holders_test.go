package supervisor

import (
	"reflect"
	"testing"
)

const megabyte = 1 << 20

func TestTopCommitHoldersNamesTheAppsHoldingTheMostCommit(t *testing.T) {
	tests := []struct {
		name      string
		processes []processCommit
		count     int
		want      []CommitHolder
	}{
		{
			name: "an app's processes count as the app, under the shell and service hosts",
			processes: []processCommit{
				{pid: 0, name: "", commit: 0},
				{pid: 4, parent: 0, name: "System", created: 1},
				{pid: 700, parent: 600, name: "services.exe", created: 2, commit: 10 * megabyte},
				{pid: 800, parent: 700, name: "svchost.exe", created: 3, commit: 50 * megabyte},
				{pid: 801, parent: 700, name: "svchost.exe", created: 3, commit: 30 * megabyte},
				{pid: 900, parent: 850, name: "explorer.exe", created: 4, commit: 200 * megabyte},
				{pid: 9200, parent: 900, name: "ChatGPT.exe", created: 5, commit: 400 * megabyte},
				{pid: 10840, parent: 9200, name: "codex.exe", created: 6, commit: 300 * megabyte},
				{pid: 35152, parent: 10840, name: "powershell.exe", created: 7, commit: 59 * megabyte},
				{pid: 38804, parent: 35152, name: "uv.exe", created: 8, commit: 241 * megabyte},
				{pid: 43256, parent: 38804, name: "python.exe", created: 9, commit: 709 * megabyte},
				{pid: 16900, parent: 4960, name: "claude.exe", created: 5, commit: 150 * megabyte},
				{pid: 4960, parent: 800, name: "sihost.exe", created: 4, commit: 20 * megabyte},
			},
			count: 10,
			want: []CommitHolder{
				{Name: "ChatGPT", Commit: (400 + 300 + 59 + 241 + 709) * megabyte, Processes: 5},
				{Name: "explorer", Commit: 200 * megabyte, Processes: 1},
				{Name: "claude", Commit: 150 * megabyte, Processes: 1},
				{Name: "svchost", Commit: 80 * megabyte, Processes: 2},
				{Name: "sihost", Commit: 20 * megabyte, Processes: 1},
				{Name: "services", Commit: 10 * megabyte, Processes: 1},
				{Name: "System", Commit: 0, Processes: 1},
			},
		},
		{
			name: "a process whose parent is gone is its own app, and apps of one name count as one",
			processes: []processCommit{
				{pid: 48440, parent: 26096, name: "cfo.exe", created: 10, commit: 65 * megabyte},
				{pid: 47352, parent: 48440, name: "claude.exe", created: 11, commit: 389 * megabyte},
				{pid: 50000, parent: 26097, name: "CFO.EXE", created: 12, commit: 60 * megabyte},
			},
			count: 3,
			want:  []CommitHolder{{Name: "cfo", Commit: (65 + 389 + 60) * megabyte, Processes: 3}},
		},
		{
			name: "a parent created after the process took a reused ID and is not its parent",
			processes: []processCommit{
				{pid: 1000, parent: 2000, name: "node.exe", created: 5, commit: 100 * megabyte},
				{pid: 2000, parent: 3000, name: "Code.exe", created: 9, commit: 300 * megabyte},
			},
			count: 3,
			want:  []CommitHolder{{Name: "Code", Commit: 300 * megabyte, Processes: 1}, {Name: "node", Commit: 100 * megabyte, Processes: 1}},
		},
		{
			name: "parents that loop still end",
			processes: []processCommit{
				{pid: 1, parent: 2, name: "a.exe", created: 5, commit: 1 * megabyte},
				{pid: 2, parent: 1, name: "b.exe", created: 5, commit: 2 * megabyte},
			},
			count: 3,
			want:  nil,
		},
		{
			name: "only the count holding the most, ties by name",
			processes: []processCommit{
				{pid: 1, name: "b.exe", commit: 5 * megabyte},
				{pid: 2, name: "a.exe", commit: 5 * megabyte},
				{pid: 3, name: "c.exe", commit: 9 * megabyte},
			},
			count: 2,
			want:  []CommitHolder{{Name: "c", Commit: 9 * megabyte, Processes: 1}, {Name: "a", Commit: 5 * megabyte, Processes: 1}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			holders := topCommitHolders(test.processes, test.count)

			// Assert
			if test.want == nil {
				total := 0
				for _, holder := range holders {
					total += holder.Processes
				}
				if total != len(test.processes) {
					t.Fatalf("holders = %+v, want every process counted once", holders)
				}
				return
			}
			if !reflect.DeepEqual(holders, test.want) {
				t.Fatalf("holders = %+v, want %+v", holders, test.want)
			}
		})
	}
}
