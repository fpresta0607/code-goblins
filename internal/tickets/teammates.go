package tickets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// teammatesFile keeps, in the state folder, the login and the name of every
// teammate the supervisor has read in a collaborative repository, so a public
// issue can be screened for them. It only grows: someone who stops working
// in a repository is still nobody a public issue names.
const teammatesFile = "teammates.json"

// RememberTeammates adds people's logins and names to those kept, and writes
// only when one is new.
func RememberTeammates(stateDir string, people []Actor) error {
	known, err := Teammates(stateDir)
	if err != nil {
		return err
	}
	next := slices.Clone(known)
	for _, person := range people {
		for _, name := range []string{person.Login, strings.TrimSpace(person.Name)} {
			if name != "" && !slices.ContainsFunc(next, func(kept string) bool { return strings.EqualFold(kept, name) }) {
				next = append(next, name)
			}
		}
	}
	if len(next) == len(known) {
		return nil
	}
	slices.Sort(next)
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, teammatesFile), data)
}

// Teammates are the logins and names RememberTeammates kept, none before it
// first wrote.
func Teammates(stateDir string) ([]string, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, teammatesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("read %s: %w", teammatesFile, err)
	}
	return names, nil
}
