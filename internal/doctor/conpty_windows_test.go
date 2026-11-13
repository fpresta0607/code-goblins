package doctor

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Without a pseudo console no native terminal can run, so its absence makes
// the environment unhealthy with what Windows it needs.
func TestRunWithoutAPseudoConsoleIsUnhealthy(t *testing.T) {
	original := pseudoConsole
	pseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("NoSuchPseudoConsoleProcedure")
	t.Cleanup(func() { pseudoConsole = original })

	check := checkConPTY()

	if check.Name != "conpty" || check.Err == "" || !strings.Contains(check.Hint, "1809") {
		t.Errorf("conpty check = %+v, want it missing with the Windows it needs", check)
	}
	if Healthy([]Check{check}) {
		t.Error("Healthy = true without a pseudo console")
	}
}
