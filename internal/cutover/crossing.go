package cutover

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// CrossingFile is the record, in the home's state, of the crossing an update
// or an install made: the cutover made while the fleet works, which the
// supervisor then finishes.
const CrossingFile = "crossing.json"

// Crossing is the record of one crossing.
type Crossing struct {
	// Folder is the crossing's undo folder in state\backups, which
	// goblins cutover --undo names.
	Folder string `json:"folder"`
	// At is when it was made: a terminal whose host started before it holds
	// the old settings until it is restarted.
	At time.Time `json:"at"`
	// Bridges are the old programs left in the home's bin as copies of the
	// build that crossed, for the update that ran it and the terminals from
	// before it, which the supervisor removes once nothing runs them.
	Bridges []string `json:"bridges,omitempty"`
	// IsRelabelled says the GitHub labels of the old name were renamed,
	// which the supervisor does after it starts, since that needs the
	// network and the person's GitHub sign-in.
	IsRelabelled bool `json:"is_relabelled,omitempty"`
	// Relabelling says why the last renaming of the labels failed.
	Relabelling string `json:"relabelling,omitempty"`
}

// PreRename says whether the home at root is one a build before the rename
// set up and no crossing has moved: its bin holds the old program, and its
// state no record of a crossing. An undone crossing removes its record, so
// the home is one again.
func PreRename(root string) bool {
	return exists(filepath.Join(root, "bin", oldName+".exe")) && !exists(filepath.Join(root, "state", CrossingFile))
}

// FinishedOldUpdate says whether the last update of the home whose state is
// stateDir was one a build before the rename made, and it finished. This
// build does not read that journal, which names the old program among its
// aliases, and it is history: nothing it kept is a way back any more.
func FinishedOldUpdate(stateDir string) bool {
	data, err := fsx.ReadFile(filepath.Join(update.Dir(stateDir), "journal.json"))
	if err != nil {
		return false
	}
	var journal update.Journal
	return json.Unmarshal(data, &journal) == nil && journal.Schema == oldName+"-update.v1" && journal.Phase.Finished()
}

// oldWakeLine is the line a board from before the rename typed into a Codex
// or pi CEO's terminal to wake it, the same in every release up to v0.5.18,
// as a prompt line reads with its spaces taken out: its count and the wakes
// it names vary, and one longer than 300 characters was cut to end in "...".
var oldWakeLine = regexp.MustCompile(`^` + oldName + `watcherwake:\d+queuedwakes?\(.*(\);run` + oldName + `drain,handlewhatitshowsandackitwiththeWAKE_ACK_REQUIREDcommanditprints|\.\.\.)$`)

// IsOldWakeLine says whether draft, a prompt line as harness.Screens.Draft
// reads it, holds a wake line a board from before the rename typed there and
// nothing else.
func IsOldWakeLine(draft string) bool {
	return oldWakeLine.MatchString(draft)
}

// PlanCrossing reads the machine and plans the crossing an apply at now
// makes. It changes nothing.
func (m Machine) PlanCrossing(now time.Time) (Plan, error) {
	return m.plan(&Crossing{Folder: BackupFolder(m.Root, now), At: now.UTC()})
}

// record writes the crossing's record, the plan's last change, so the undo
// removes it first.
func (p *planner) record() error {
	path := filepath.Join(p.state(), CrossingFile)
	before, isAbsent, err := readFile(path)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p.crossing, "", "  ")
	if err != nil {
		return err
	}
	p.add(KindState, path+": the record of this crossing, which the supervisor finishes", writeStep(path, before, isAbsent, append(data, '\n')))
	return nil
}

// ReadCrossing reads the record of the crossing that moved the home whose
// state is state, and says whether there is one.
func ReadCrossing(state string) (Crossing, bool, error) {
	data, err := fsx.ReadFile(filepath.Join(state, CrossingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Crossing{}, false, nil
	}
	if err != nil {
		return Crossing{}, false, err
	}
	var crossing Crossing
	if err := json.Unmarshal(data, &crossing); err != nil {
		return Crossing{}, false, fmt.Errorf("cutover: read %s: %w", filepath.Join(state, CrossingFile), err)
	}
	return crossing, true, nil
}

// WriteCrossing writes the record of the crossing that moved the home whose
// state is state.
func WriteCrossing(state string, crossing Crossing) error {
	data, err := json.MarshalIndent(crossing, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(state, CrossingFile), append(data, '\n'))
}

// Relabel renames the GitHub labels of the old name, the part of a crossing
// the supervisor makes once it serves, as steps of the crossing whose undo
// folder is folder, so its undo renames them back.
func (m Machine) Relabel(folder string) error {
	p := &planner{machine: m, claimed: map[string]bool{}}
	if err := p.labels(); err != nil {
		return err
	}
	return m.extend(folder, Plan{Changes: p.changes})
}

// RemoveBridges removes the old programs a crossing left in the home's bin
// at root, once nothing runs them, and the old programs an update or the
// crossing set aside there, which the janitor does not know by name. A copy
// something still runs is left, and the next look removes it.
func RemoveBridges(root string, bridges []string) error {
	var failed error
	for _, bridge := range bridges {
		if err := setAside(bridge); err != nil {
			failed = errors.Join(failed, err)
		}
	}
	bin := filepath.Join(root, "bin")
	entries, err := os.ReadDir(bin)
	if err != nil {
		return errors.Join(failed, err)
	}
	for _, entry := range entries {
		parts := aside.FindStringSubmatch(entry.Name())
		if entry.IsDir() || parts == nil || !strings.HasPrefix(strings.ToLower(parts[1]), oldName) {
			continue
		}
		_ = os.Remove(filepath.Join(bin, entry.Name()))
	}
	return failed
}
