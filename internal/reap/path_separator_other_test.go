//go:build !windows

package reap

import "testing"

// Outside Windows a backslash is a character of a name and no separator, so
// a folder whose name holds one is neither inside the folder its name begins
// with nor a boundary of it.
func TestABackslashIsNoSeparatorOutsideWindows(t *testing.T) {
	cases := []struct {
		name      string
		find      func() bool
		wantFound bool
	}{
		{name: "pathWithin a folder named with a backslash", find: func() bool { return pathWithin(`/w/gb-old\src`, "/w/gb-old") }},
		{name: "namesPath ending at a backslash", find: func() bool { return namesPath(`node /w/gb-old\server.js`, "/w/gb-old") }},
		{name: "pathWithin a folder inside it", find: func() bool { return pathWithin("/w/gb-old/src", "/w/gb-old") }, wantFound: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isFound := tc.find()

			// Assert
			if isFound != tc.wantFound {
				t.Fatalf("%s = %t, want %t", tc.name, isFound, tc.wantFound)
			}
		})
	}
}
