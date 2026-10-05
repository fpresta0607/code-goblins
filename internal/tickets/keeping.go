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

// keepingFile is what the supervisor remembers about keeping tickets on this
// machine, in one file under the state directory.
const keepingFile = "ticket-keeping.json"

type keeping struct {
	// Public are the public repositories the CFO allowed tickets in. An
	// issue in a public repository is public, so tickets are kept there only
	// once he has allowed it, and he is asked once per repository.
	Public []string `json:"public"`
	// QueuedBefore are the tasks that were already queued when tickets were
	// first kept, and nil until then. Each gets its ticket when it starts
	// rather than at once, so old queued work does not arrive in a teammate's
	// repository as a burst of issues.
	QueuedBefore *[]string `json:"queued_before,omitempty"`
}

// AllowPublic records that tickets may be kept in a public repository.
func AllowPublic(directory, repository string) error {
	kept, err := readKeeping(directory)
	if err != nil {
		return err
	}
	name := strings.ToLower(repository)
	if slices.Contains(kept.Public, name) {
		return nil
	}
	kept.Public = append(kept.Public, name)
	slices.Sort(kept.Public)
	return writeKeeping(directory, kept)
}

// IsPublicAllowed reports whether the CFO allowed tickets in a public
// repository. GitHub does not tell a repository's names apart by case.
func IsPublicAllowed(directory, repository string) (bool, error) {
	kept, err := readKeeping(directory)
	if err != nil {
		return false, err
	}
	return slices.Contains(kept.Public, strings.ToLower(repository)), nil
}

// QueuedBefore names the tasks that were already queued when tickets were
// first kept on this machine. The first call records queued as that set,
// empty or not; every later call returns what the first recorded.
func QueuedBefore(directory string, queued []string) ([]string, error) {
	kept, err := readKeeping(directory)
	if err != nil {
		return nil, err
	}
	if kept.QueuedBefore == nil {
		first := append([]string{}, queued...)
		slices.Sort(first)
		kept.QueuedBefore = &first
		if err := writeKeeping(directory, kept); err != nil {
			return nil, err
		}
	}
	return *kept.QueuedBefore, nil
}

func readKeeping(directory string) (keeping, error) {
	data, err := fsx.ReadFile(filepath.Join(directory, keepingFile))
	if errors.Is(err, os.ErrNotExist) {
		return keeping{}, nil
	}
	if err != nil {
		return keeping{}, err
	}
	var kept keeping
	if err := json.Unmarshal(data, &kept); err != nil {
		return keeping{}, fmt.Errorf("read %s: %w", keepingFile, err)
	}
	return kept, nil
}

func writeKeeping(directory string, kept keeping) error {
	data, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(directory, keepingFile), data)
}
