package runtime

import "testing"

// On Windows a key is lowercase with every separator a backslash and none
// trailing, so a path written any of those ways shares it.
func TestNormalizeFoldsAPathAsWindowsDoes(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: `C:\Dev\App\`, want: `c:\dev\app`},
		{path: `C:/Dev/App/`, want: `c:\dev\app`},
		{path: `c:\dev\app`, want: `c:\dev\app`},
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
