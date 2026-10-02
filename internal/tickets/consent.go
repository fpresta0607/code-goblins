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

// An issue in a public repository is public, so tickets are kept there only
// once the CFO has allowed it for that repository. The consent is one file
// under the state directory, and it is asked for once per repository.
const consentFile = "ticket-consent.json"

type consent struct {
	Public []string `json:"public"`
}

// AllowPublic records that tickets may be kept in a public repository.
func AllowPublic(directory, repository string) error {
	allowed, err := readConsent(directory)
	if err != nil {
		return err
	}
	name := strings.ToLower(repository)
	if slices.Contains(allowed.Public, name) {
		return nil
	}
	allowed.Public = append(allowed.Public, name)
	slices.Sort(allowed.Public)
	data, err := json.Marshal(allowed)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(directory, consentFile), data)
}

// IsPublicAllowed reports whether the CFO allowed tickets in a public
// repository. GitHub does not tell a repository's names apart by case.
func IsPublicAllowed(directory, repository string) (bool, error) {
	allowed, err := readConsent(directory)
	if err != nil {
		return false, err
	}
	return slices.Contains(allowed.Public, strings.ToLower(repository)), nil
}

func readConsent(directory string) (consent, error) {
	data, err := fsx.ReadFile(filepath.Join(directory, consentFile))
	if errors.Is(err, os.ErrNotExist) {
		return consent{}, nil
	}
	if err != nil {
		return consent{}, err
	}
	var allowed consent
	if err := json.Unmarshal(data, &allowed); err != nil {
		return consent{}, fmt.Errorf("read %s: %w", consentFile, err)
	}
	return allowed, nil
}
