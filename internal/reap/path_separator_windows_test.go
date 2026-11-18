package reap

import "testing"

// On Windows a forward slash is a separator as a backslash is, so a path
// written with either is the same path.
func TestAForwardSlashIsASeparatorOnWindows(t *testing.T) {
	cases := []struct {
		name string
		find func() bool
	}{
		{name: "pathWithin a folder written with forward slashes", find: func() bool { return pathWithin(`C:/w/gb-old/src`, `C:\w\gb-old`) }},
		{name: "namesPath ending at a forward slash", find: func() bool { return namesPath(`node C:\w\gb-old/server.js`, `C:\w\gb-old`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isFound := tc.find()

			// Assert
			if !isFound {
				t.Fatalf("%s = false, want true", tc.name)
			}
		})
	}
}
