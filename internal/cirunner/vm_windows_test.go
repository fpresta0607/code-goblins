package cirunner

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every script HyperV runs is one Windows PowerShell parses without error, so
// none fails first on the machine that installs the service.
func TestHyperVScriptsParse(t *testing.T) {
	// Arrange
	commands := &recorder{result: execx.Result{Stdout: []byte("Off")}}
	eachHyperVCall(HyperV{Commands: commands})
	texts := make([]string, len(commands.ran))
	for i, request := range commands.ran {
		texts[i] = script(request)
	}

	// Act
	said := parseErrors(t, texts)

	// Assert
	for i, text := range texts {
		if said[i] != "" {
			t.Errorf("%q does not parse: %s", text, said[i])
		}
	}
}
