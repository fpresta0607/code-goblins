package sandbox

import "testing"

func TestIsNeededByReadsTheSandboxLineOfAuthentication(t *testing.T) {
	for _, test := range []struct {
		name       string
		brief      string
		isNeeded   bool
		isRefusing bool
	}{
		{"no line", "## Authentication\n\ncredentials: none\nmcp: none\n", false, false},
		{"needed", "## Authentication\n\ncredentials: none\nsandbox: needed\n", true, false},
		{"needed in any case", "## Authentication\r\nSandbox: Needed\r\n", true, false},
		{"a line under another heading", "## Task\n\nsandbox: needed\n\n## Authentication\n\ncredentials: none\n", false, false},
		{"another word", "## Authentication\n\nsandbox: maybe\n", false, true},
		{"two lines", "## Authentication\n\nsandbox: needed\nsandbox: needed\n", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			isNeeded, err := IsNeededBy(test.brief)

			// Assert
			if (err != nil) != test.isRefusing || isNeeded != test.isNeeded {
				t.Fatalf("IsNeededBy = %t, %v, want %t and refused %t", isNeeded, err, test.isNeeded, test.isRefusing)
			}
		})
	}
}
