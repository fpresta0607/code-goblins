package state

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// What a restart or sign-out left for the supervisor to bring back is, for
// the CFO and each goblin, waiting until its turn comes, back in its own
// session, or stopped where it could not come back, with the reason.
const (
	ComebackWaiting = "waiting"
	ComebackBack    = "back"
	ComebackStopped = "stopped"
)

// Comeback is what the supervisor brings back after the restart or sign-out
// that ended the sign-in before SignedIn: the CFO first, then each goblin that
// was working, in state/comeback.json. A record for the sign-in under way
// says the comeback was planned, so a supervisor that starts again in it,
// as an update restarts it, carries on rather than plans again.
type Comeback struct {
	SignedIn time.Time       `json:"signed_in"`
	Planned  time.Time       `json:"planned"`
	CFO      *ComebackEntry  `json:"cfo,omitempty"`
	Goblins  []ComebackEntry `json:"goblins,omitempty"`
}

// ComebackEntry is the CFO or one goblin: for a goblin, its id and the spawn
// generation the restart ended.
type ComebackEntry struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation,omitempty"`
	State      string    `json:"state"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at,omitzero"`
}

// ComebackPath is where the comeback record is kept.
func ComebackPath(directory string) string {
	return filepath.Join(directory, "comeback.json")
}

// ReadComeback reads the comeback record; a home with none returns an error
// that is os.ErrNotExist.
func ReadComeback(directory string) (Comeback, error) {
	data, err := fsx.ReadFile(ComebackPath(directory))
	if err != nil {
		return Comeback{}, err
	}
	var record Comeback
	if err := json.Unmarshal(data, &record); err != nil {
		return Comeback{}, fmt.Errorf("read the comeback record: %w", err)
	}
	return record, nil
}

// WriteComeback replaces the comeback record.
func WriteComeback(directory string, record Comeback) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(ComebackPath(directory), data)
}

// Waiting reports whether the goblin id, as generation, waits to come back.
func (c Comeback) Waiting(id, generation string) bool {
	for _, entry := range c.Goblins {
		if entry.ID == id && entry.Generation == generation {
			return entry.State == ComebackWaiting
		}
	}
	return false
}
