package starthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// acceptedFile holds, in the state directory, the start hooks the CFO
// accepted.
const acceptedFile = "accepted-start-hooks.json"

// Acceptance is a start hook the CFO judged safe and accepted for the goblins
// of one project or of the fleet: the file named File whose content has the
// SHA-256 SHA256, from the distribution whose RECORD installs it. It passes in
// every environment of those goblins at every install, while a file that
// differs from it in any of the three, as one a later release or another
// package writes does, is still stopped. It pins that file alone, never a
// module one of its lines imports, which stays the distribution's to change.
type Acceptance struct {
	// Project names the project whose goblins it passes for, by its
	// checkout's folder, or is empty for every project's.
	Project string `json:"project,omitempty"`
	// File is the name of its file.
	File string `json:"file"`
	// SHA256 is the SHA-256 of its file's content, in hex.
	SHA256 string `json:"sha256"`
	// Distribution is the distribution whose RECORD installs it, as its
	// .dist-info folder names it, such as zope.interface-6.4.
	Distribution string `json:"distribution"`
	// Task is the goblin whose stop the CFO judged it from, and At is when
	// he accepted it.
	Task string    `json:"task"`
	At   time.Time `json:"at"`
}

// accepted is what acceptedFile holds.
type accepted struct {
	Hooks []Acceptance `json:"accepted"`
}

// Accept records acceptance in stateDir beside the ones recorded before.
func Accept(stateDir string, acceptance Acceptance) error {
	hooks, err := readAccepted(stateDir)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(accepted{Hooks: append(hooks, acceptance)}, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, acceptedFile), append(data, '\n'))
}

// Unaccepted are the hooks among hooks that the CFO accepted, as stateDir
// records, neither for the goblins of project nor for the fleet's.
func Unaccepted(stateDir, project string, hooks []Hook) ([]Hook, error) {
	acceptances, err := readAccepted(stateDir)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(hooks), func(hook Hook) bool {
		return slices.ContainsFunc(acceptances, func(acceptance Acceptance) bool { return acceptance.passes(project, hook) })
	}), nil
}

// passes reports whether a lets hook through in an environment of a goblin of
// project. A hook no installed distribution names never passes, nor does a
// folder, which has no one content. Windows tells neither a project's folder
// nor a file's name apart by case.
func (a Acceptance) passes(project string, hook Hook) bool {
	return hook.Package != "" && hook.Digest != "" &&
		(a.Project == "" || strings.EqualFold(a.Project, project)) &&
		strings.EqualFold(a.File, filepath.Base(hook.Path)) &&
		strings.EqualFold(a.SHA256, hook.Digest) &&
		strings.EqualFold(a.Distribution, hook.Package)
}

func readAccepted(stateDir string) ([]Acceptance, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, acceptedFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var kept accepted
	if err := json.Unmarshal(data, &kept); err != nil {
		return nil, fmt.Errorf("read %s: %w", acceptedFile, err)
	}
	return kept.Hooks, nil
}
