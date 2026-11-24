//go:build !windows

package fleettree

import (
	"encoding/json"
	"testing"
	"time"
)

// Outside Windows a backslash is a character of a name and no separator, so
// a file whose name holds one is not under the folder its name begins with,
// and an output file named after one is not a shell's.
func TestABackslashIsNoSeparatorOutsideWindows(t *testing.T) {
	// Arrange
	started := at.Add(-time.Minute)
	children := []Node{{ID: "shell:b5", Kind: KindShell, State: Working, Started: started}}
	input, err := json.Marshal(map[string]string{"file_path": `/w/repo\main.go`})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		find func() string
		want string
	}{
		{name: "callSubject of a name after a backslash", find: func() string { return callSubject(input, "/w/repo") }, want: `/w/repo\main.go`},
		{name: "shellOf an output file after a backslash", find: func() string {
			return shellOf(job{root: Process{Started: started}}, `sh -c "pwsh > "/tmp/tasks\b5.output" 2>&1"`, nil, children)
		}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			found := tc.find()

			// Assert
			if found != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, found, tc.want)
			}
		})
	}
}
