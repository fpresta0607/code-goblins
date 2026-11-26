package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// DeliveryException is a narrower delivery mode the registered primary CEO
// approved for one task, kept beside its brief so that the supervisor's
// automatic start, the board's Start and an explicit goblins spawn all select
// from the same approval, and a start that comes first never freezes another
// mode (issue 2146). ApprovedBy is the primary registration that approved it,
// and Checks the digest of the project's verification it was approved under:
// once those checks change, it approves nothing.
type DeliveryException struct {
	Mode       string `json:"mode"`
	Authority  string `json:"authority"`
	ApprovedBy string `json:"approved_by"`
	Checks     string `json:"checks"`
}

// DeliveryExceptionPath is where the approved exception of task id is kept.
func DeliveryExceptionPath(dataDir, id string) string {
	return filepath.Join(dataDir, id, "delivery-exception.json")
}

// RecordDeliveryException keeps authority as the exception that approves mode
// for task id under the manifest's current checks. Its caller has proven it is
// the registered primary CEO whose registration approvedBy names.
func RecordDeliveryException(dataDir, id, mode, authority, approvedBy string, manifest Manifest) error {
	checks, err := checksDigest(manifest.Verification)
	if err != nil {
		return err
	}
	data, err := json.Marshal(DeliveryException{Mode: mode, Authority: authority, ApprovedBy: approvedBy, Checks: checks})
	if err != nil {
		return err
	}
	path := DeliveryExceptionPath(dataDir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

// ClearDeliveryException removes the exception kept for task id, if any.
func ClearDeliveryException(dataDir, id string) error {
	if err := os.Remove(DeliveryExceptionPath(dataDir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ApprovedDeliveryException is the authority the exception kept for task id
// gives mode under the manifest's current checks. It is empty with no error
// when none is kept, and empty with an error saying why when the one kept
// approves another mode or other checks.
func ApprovedDeliveryException(dataDir, id, mode string, manifest Manifest) (string, error) {
	data, err := fsx.ReadFile(DeliveryExceptionPath(dataDir, id))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var kept DeliveryException
	if err := json.Unmarshal(data, &kept); err != nil {
		return "", fmt.Errorf("the delivery exception kept for %s is unreadable: %w", id, err)
	}
	checks, err := checksDigest(manifest.Verification)
	if err != nil {
		return "", err
	}
	switch {
	case kept.Mode != mode:
		return "", fmt.Errorf("the delivery exception kept for %s approves %s, not %s", id, kept.Mode, mode)
	case kept.Checks != checks:
		return "", fmt.Errorf("the project's checks changed since the delivery exception kept for %s was approved", id)
	}
	return kept.Authority, nil
}

func checksDigest(verification Verification) (string, error) {
	data, err := json.Marshal(verification)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
