//go:build !windows

package runtime

import "testing"

// Outside Windows a backslash is a character of a name: a key keeps it, and
// trims only the system's own separator.
func TestNormalizeKeepsABackslashOutsideWindows(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "/w/app/", want: "/w/app"},
		{path: `/w/app\`, want: `/w/app\`},
		{path: `/w/App\src`, want: `/w/App\src`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			// Act
			key := normalize(tc.path)

			// Assert
			if key != tc.want {
				t.Fatalf("normalize(%q) = %q, want %q", tc.path, key, tc.want)
			}
		})
	}
}
