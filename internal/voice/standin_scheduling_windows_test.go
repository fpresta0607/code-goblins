package voice

import "github.com/fpresta0607/code-goblins/internal/conpty"

// The live tests' worker asks for the scheduling `cfo voice-worker` asks for:
// started hidden, Windows throttles it, and a three-minute dictation took
// minutes rather than seconds.
func init() { standInScheduling = conpty.KeepCurrentProcessInteractive }
