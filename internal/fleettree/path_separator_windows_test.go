package fleettree

import (
	"encoding/json"
	"testing"
)

// On Windows a forward slash is a separator as a backslash is, so a file
// written under the folder with either is named relative to it.
func TestAForwardSlashIsASeparatorOnWindows(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{name: "after a backslash", file: `C:\w\repo\main.go`},
		{name: "after a forward slash", file: `C:\w\repo/main.go`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			input, err := json.Marshal(map[string]string{"file_path": tc.file})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			subject := callSubject(input, `C:\w\repo`)

			// Assert
			if subject != "main.go" {
				t.Fatalf("callSubject(%s) = %s, want main.go", tc.file, subject)
			}
		})
	}
}
