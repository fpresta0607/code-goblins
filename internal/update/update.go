// Package update installs a verified candidate build into a CFO home: it
// swaps the home's cfo.exe and goblins.exe for the candidate as one
// transaction whose progress is durable before anything live changes, so an
// update that fails, or a process that ends part way, always leaves a way back
// to the build that ran before. This package owns the files; cfo update owns
// the supervisor around them.
package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Aliases are the names the home's one program is installed under.
var Aliases = []string{"cfo.exe", "goblins.exe"}

// Phase is how far an update has gone.
type Phase string

const (
	// Prepared: the previous build is backed up and the candidate staged;
	// nothing live has changed.
	Prepared Phase = "prepared"
	// Stopped: the supervisor that ran the previous build was stopped.
	Stopped Phase = "stopped"
	// Swapped: the aliases name the candidate.
	Swapped Phase = "swapped"
	// Done: the candidate's supervisor answered as itself and holds the
	// watcher lock.
	Done Phase = "done"
	// RollingBack: the previous build is being put back.
	RollingBack Phase = "rolling-back"
	// RolledBack: the previous build is back and its supervisor answers.
	RolledBack Phase = "rolled-back"
	// Degraded: the previous build serves from its verified copy, but an
	// alias could not be put back yet; --recover repairs it.
	Degraded Phase = "degraded"
)

// Finished reports whether an update in phase p needs nothing more.
func (p Phase) Finished() bool {
	return p == Done || p == RolledBack
}

const schema = "cfo-update.v1"

// Alias is one installed name with the build it held before the update and
// the verified copy of that build kept for the way back.
type Alias struct {
	Name     string `json:"name"`
	Previous string `json:"previous_sha256"`
	Backup   string `json:"backup"`
	// Aside are the files this update moved out of the alias's way, the
	// only ones its clean-up removes.
	Aside []string `json:"aside,omitempty"`
}

// Attempt is a supervisor the update started, by pid and start time, and the
// program it runs.
type Attempt struct {
	PID     int       `json:"pid"`
	Start   time.Time `json:"start"`
	Program string    `json:"program"`
}

// Journal is an update's durable progress, written before anything live
// changes and on every step after.
type Journal struct {
	Schema    string    `json:"schema"`
	Root      string    `json:"root"`
	Phase     Phase     `json:"phase"`
	Candidate string    `json:"candidate"`
	Copy      string    `json:"candidate_copy"`
	Hash      string    `json:"candidate_sha256"`
	Aliases   []Alias   `json:"aliases"`
	Attempts  []Attempt `json:"attempts,omitempty"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
	Outcome   string    `json:"outcome,omitempty"`
}

// Dir is where an update keeps its journal, the verified copies and its lock.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "update")
}

func journalPath(stateDir string) string {
	return filepath.Join(Dir(stateDir), "journal.json")
}

// ReadJournal reads the home's last update, or fs.ErrNotExist when none ran.
func ReadJournal(stateDir string) (Journal, error) {
	data, err := os.ReadFile(journalPath(stateDir))
	if err != nil {
		return Journal{}, err
	}
	var journal Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		return Journal{}, fmt.Errorf("update: read journal: %w", err)
	}
	if journal.Schema != schema {
		return Journal{}, fmt.Errorf("update: journal schema %q is not %q", journal.Schema, schema)
	}
	return journal, nil
}

// Record writes the journal at phase, durably, before the step it names
// changes anything.
func Record(stateDir string, journal *Journal, phase Phase, outcome string) error {
	journal.Phase, journal.Updated = phase, time.Now().UTC()
	if outcome != "" {
		journal.Outcome = outcome
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeSynced(journalPath(stateDir), data)
}

// Validate refuses a journal that is not this home's: its root, its aliases
// and the paths of its verified copies must be exactly what an update of this
// home writes, so restoring from it can only put this home's previous build
// back in this home.
func Validate(journal Journal, root, stateDir string) error {
	if !strings.EqualFold(filepath.Clean(journal.Root), filepath.Clean(root)) {
		return fmt.Errorf("update: the journal is for %s, not this home %s", journal.Root, root)
	}
	if !strings.EqualFold(filepath.Clean(journal.Copy), filepath.Join(Dir(stateDir), "candidate.exe")) {
		return fmt.Errorf("update: the journal's candidate copy %s is not this home's", journal.Copy)
	}
	if len(journal.Aliases) != len(Aliases) {
		return fmt.Errorf("update: the journal names %d aliases, not %d", len(journal.Aliases), len(Aliases))
	}
	for i, alias := range journal.Aliases {
		if alias.Name != Aliases[i] || !strings.EqualFold(filepath.Clean(alias.Backup), filepath.Join(Dir(stateDir), "previous-"+Aliases[i])) || len(alias.Previous) != 64 {
			return fmt.Errorf("update: the journal's alias %q is not this home's", alias.Name)
		}
		for _, aside := range alias.Aside {
			if !isAside(aside, root, alias.Name) {
				return fmt.Errorf("update: the journal's file %s is not one an update moved out of this home's %s", aside, alias.Name)
			}
		}
	}
	return nil
}

// isAside reports whether path is exactly a name moveAside gives the alias
// name in root: <root>\<name>.<digits>.update-old.
func isAside(path, root, name string) bool {
	if filepath.Clean(path) != path || !strings.EqualFold(filepath.Dir(path), filepath.Clean(root)) {
		return false
	}
	number, ok := strings.CutPrefix(filepath.Base(path), name+".")
	if !ok {
		return false
	}
	number, ok = strings.CutSuffix(number, ".update-old")
	return ok && number != "" && strings.Trim(number, "0123456789") == ""
}

// HashFile is the SHA-256 of the file at path, in hex.
func HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return hashOf(file)
}

func hashOf(content io.Reader) (string, error) {
	sum := sha256.New()
	if _, err := io.Copy(sum, content); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// copyVerified copies from to to, durably, only once what it copies hashes
// to want.
func copyVerified(from, to, want string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("update: %s hashes to %s, not %s", from, got, want)
	}
	if current, err := os.ReadFile(to); err == nil && bytes.Equal(current, data) {
		return nil
	}
	return writeSynced(to, data)
}

// Prepare backs up the home's installed build as verified copies, keeps a
// copy of the candidate as a second way to run the recovery, stages the
// candidate beside each alias, and records it all, so the journal names
// everything the way back needs before anything live changes.
func Prepare(root, stateDir, candidate string) (*Journal, error) {
	hash, err := HashFile(candidate)
	if err != nil {
		return nil, fmt.Errorf("update: read the candidate %s: %w", candidate, err)
	}
	if err := os.MkdirAll(Dir(stateDir), 0o700); err != nil {
		return nil, err
	}
	journal := &Journal{Schema: schema, Root: root, Candidate: candidate, Copy: filepath.Join(Dir(stateDir), "candidate.exe"), Hash: hash, Started: time.Now().UTC()}
	if err := copyVerified(candidate, journal.Copy, hash); err != nil {
		return nil, fmt.Errorf("update: keep a copy of the candidate: %w", err)
	}
	for _, name := range Aliases {
		installed := filepath.Join(root, name)
		previous, err := HashFile(installed)
		if err != nil {
			return nil, fmt.Errorf("update: read the installed %s: %w", installed, err)
		}
		if previous == hash {
			return nil, fmt.Errorf("update: %s is already this build", installed)
		}
		backup := filepath.Join(Dir(stateDir), "previous-"+name)
		if err := copyVerified(installed, backup, previous); err != nil {
			return nil, fmt.Errorf("update: back up %s: %w", installed, err)
		}
		if err := copyVerified(journal.Copy, staged(root, name), hash); err != nil {
			return nil, fmt.Errorf("update: stage the candidate as %s: %w", name, err)
		}
		journal.Aliases = append(journal.Aliases, Alias{Name: name, Previous: previous, Backup: backup})
	}
	if err := Record(stateDir, journal, Prepared, ""); err != nil {
		return nil, err
	}
	return journal, nil
}

func staged(root, name string) string {
	return filepath.Join(root, name+".update-new")
}

// moveAside renames path out of the way under a name of its own, and
// records the name on alias: a build still running, such as a terminal's
// host, cannot be overwritten or removed, but can be renamed.
func moveAside(path string, alias *Alias) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	aside := fmt.Sprintf("%s.%d.update-old", path, time.Now().UnixNano())
	if err := os.Rename(path, aside); err != nil {
		return err
	}
	alias.Aside = append(alias.Aside, aside)
	return nil
}

// Swap puts the staged candidate in place under every alias. Two renames are
// not one atomic step, so an interrupted swap is recognised afterwards by
// what each alias hashes to, never by a flag.
func Swap(journal *Journal) error {
	for i := range journal.Aliases {
		alias := &journal.Aliases[i]
		installed := filepath.Join(journal.Root, alias.Name)
		if hash, err := HashFile(installed); err == nil && hash == journal.Hash {
			continue
		}
		if err := moveAside(installed, alias); err != nil {
			return fmt.Errorf("update: move the previous %s aside: %w", installed, err)
		}
		if err := os.Rename(staged(journal.Root, alias.Name), installed); err != nil {
			return fmt.Errorf("update: put the candidate in place as %s: %w", installed, err)
		}
	}
	return VerifyInstalled(journal, false)
}

// Restore puts the previous build back under every alias that does not hold
// it, from the verified copy, which it never consumes: the copy stays for
// another try.
func Restore(journal *Journal) error {
	var failed error
	for i := range journal.Aliases {
		alias := &journal.Aliases[i]
		installed := filepath.Join(journal.Root, alias.Name)
		if hash, err := HashFile(installed); err == nil && hash == alias.Previous {
			continue
		}
		restoring := installed + ".update-restore"
		if err := copyVerified(alias.Backup, restoring, alias.Previous); err != nil {
			failed = errors.Join(failed, fmt.Errorf("update: copy the previous %s back: %w", alias.Name, err))
			continue
		}
		if err := moveAside(installed, alias); err != nil {
			failed = errors.Join(failed, fmt.Errorf("update: move %s aside: %w", installed, err))
			continue
		}
		if err := os.Rename(restoring, installed); err != nil {
			failed = errors.Join(failed, fmt.Errorf("update: put the previous %s back: %w", installed, err))
		}
	}
	if failed != nil {
		return failed
	}
	return VerifyInstalled(journal, true)
}

// VerifyInstalled checks every alias holds the previous build, when previous
// is true, or the candidate.
func VerifyInstalled(journal *Journal, previous bool) error {
	for _, alias := range journal.Aliases {
		want := journal.Hash
		if previous {
			want = alias.Previous
		}
		installed := filepath.Join(journal.Root, alias.Name)
		got, err := HashFile(installed)
		if err != nil {
			return fmt.Errorf("update: read %s: %w", installed, err)
		}
		if got != want {
			return fmt.Errorf("update: %s hashes to %s, not %s", installed, got, want)
		}
	}
	return nil
}

// CleanUp removes what this update staged and the files it moved aside that
// nothing runs any more, and nothing else; the verified backups stay until
// the next update.
func CleanUp(journal *Journal) {
	for _, alias := range journal.Aliases {
		installed := filepath.Join(journal.Root, alias.Name)
		_ = os.Remove(staged(journal.Root, alias.Name))
		_ = os.Remove(installed + ".update-restore")
		for _, path := range alias.Aside {
			_ = os.Remove(path)
		}
	}
}
