package supervisor

import (
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// RunsUnderRegisteredCFO reports whether this process runs under the harness
// primary.json registers as the primary CFO, the one session the board
// delivers questions and answers to. It proves that as CallerIdentity does, by
// ancestry to the registered process, but asks Herdr nothing, so a pre-tool
// hook can afford it. Anything it cannot prove is false.
func RunsUnderRegisteredCFO(stateDir string) bool {
	file, err := openPrimary(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		return false
	}
	defer file.Close()
	primary, _, err := decodePrimary(file)
	if err != nil || !primary.Process.VerifiedAlive() {
		return false
	}
	entries, err := proc.Ancestry(os.Getpid(), 32)
	return err == nil && descendsFrom(entries, primary.Process)
}
