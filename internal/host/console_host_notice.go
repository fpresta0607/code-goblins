package host

import (
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// consoleHostNotice is the identity of the one wake that says this home's
// terminals run on the system conhost.
const consoleHostNotice = "conpty/console-host"

// reportConsoleHostProblem tells the CFO, once for the home whose state is
// stateDir, that its terminals run on the system conhost and why, through
// the wake queue the supervisor delivers from.
func reportConsoleHostProblem(stateDir string, problem error) error {
	detail := "console_host: " + problem.Error() + "; the system conhost can strand a typed key or crash (microsoft/terminal#18816)"
	if _, _, err := wake.AppendFirst(stateDir, consoleHostNotice, "check", "conpty", detail); err != nil {
		return err
	}
	_, err := wake.PublishEpisode(stateDir)
	return err
}
