package installtest

import (
	"slices"
	"strings"
	"testing"
)

// A stripped session reads the module cache the machine names, since without
// one Windows PowerShell reads every module before its first command, and it
// names none where the machine names none.
func TestAStrippedSessionReadsTheModuleCacheTheMachineNames(t *testing.T) {
	for name, test := range map[string]struct {
		machine string
		want    []string
	}{
		"a machine that names a cache": {`C:\cache\ModuleAnalysisCache`, []string{moduleCacheVariable + `=C:\cache\ModuleAnalysisCache`}},
		"a machine that names none":    {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv(moduleCacheVariable, test.machine)

			// Act
			cmd, _, _ := StrippedCommand(t, "", nil, WindowsPowerShell())

			// Assert
			var got []string
			for _, variable := range cmd.Env {
				if strings.HasPrefix(variable, moduleCacheVariable+"=") {
					got = append(got, variable)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("the session's module cache = %q, want %q", got, test.want)
			}
		})
	}
}
