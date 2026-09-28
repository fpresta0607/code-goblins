package herdr

import (
	"slices"
	"testing"
)

// A process started from a Herdr pane carries the pane's variables, in any
// letter case Windows accepts; each is dropped, and HERDR_SESSION, which names
// the fleet's session rather than a pane, and HERDR_CONFIG_PATH, the user's
// Herdr configuration, stay with everything else.
func TestWithoutPaneDropsEveryPaneVariable(t *testing.T) {
	// Arrange
	env := []string{
		"PATH=C:\\bin",
		"HERDR_ENV=1",
		"HERDR_PANE_ID=w1:p1",
		"herdr_tab_id=w1:t1",
		"HERDR_WORKSPACE_ID=w1",
		"HERDR_STARTUP_CWD=C:\\dev",
		"HERDR_SOCKET_PATH=\\\\.\\pipe\\herdr",
		"HERDR_BIN_PATH=C:\\herdr\\herdr.exe",
		"HERDR_SESSION=fleet",
		"HERDR_CONFIG_PATH=C:\\herdr\\herdr.toml",
		"HERDRLIKE=kept",
	}

	// Act
	kept := WithoutPane(env)

	// Assert
	if want := []string{"PATH=C:\\bin", "HERDR_SESSION=fleet", "HERDR_CONFIG_PATH=C:\\herdr\\herdr.toml", "HERDRLIKE=kept"}; !slices.Equal(kept, want) {
		t.Errorf("WithoutPane = %q, want %q", kept, want)
	}
}
