package verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const PreparedEvidenceFile = "prepared.json"

type PreparationState string

const (
	PreparationIncomplete  PreparationState = "incomplete"
	PreparationInterrupted PreparationState = "interrupted"
	PreparationComplete    PreparationState = "complete"
)

// PreparedEvidence belongs to one owned generation. Its preparer must record
// incomplete before changing files and complete only after preparation succeeds.
// A stopped preparer leaves incomplete or interrupted evidence, never complete.
type PreparedEvidence struct {
	Identity string           `json:"identity"`
	State    PreparationState `json:"state"`
}

// PreparedInventory is the caller's expected preparation identity and required
// runtime files, obtained independently of the generation's completion evidence.
// Identity must cover the preparation inputs and inventory, including revisions.
type PreparedInventory struct {
	Identity string
	Files    []PreparedFile
}

type PreparedFile struct {
	Path   string // Slash-separated relative path within the generation.
	Size   int64
	SHA256 string // Hex-encoded SHA-256 of the required bytes.
}

// ValidatePreparedGeneration reads only an explicitly owned generation and
// returns nil when its completion evidence and every required file match.
// It never prepares, repairs, executes or discovers artifacts in shared caches.
// Readiness is advisory for same-user writable directories: files and evidence
// can change during or after validation. Only a protected immutable generation
// with independently trusted expectations can support trusted reuse later.
func ValidatePreparedGeneration(root string, expected PreparedInventory) error {
	if strings.TrimSpace(expected.Identity) == "" || len(expected.Files) == 0 {
		return errors.New("verify: prepared generation requires an identity and file inventory")
	}
	paths := make(map[string]bool, len(expected.Files))
	for _, required := range expected.Files {
		if !fs.ValidPath(required.Path) || required.Path == "." || strings.ContainsAny(required.Path, "\\:\x00") || !filepath.IsLocal(filepath.FromSlash(required.Path)) {
			return fmt.Errorf("verify: unsafe prepared file path %q", required.Path)
		}
		for _, part := range strings.Split(required.Path, "/") {
			if strings.TrimRight(part, ". ") != part {
				return fmt.Errorf("verify: unsafe prepared file path %q", required.Path)
			}
		}
		key := strings.ToLower(required.Path)
		if paths[key] {
			return fmt.Errorf("verify: duplicate prepared file path %q", required.Path)
		}
		paths[key] = true
		digest, err := hex.DecodeString(required.SHA256)
		if required.Size < 0 || err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("verify: invalid size or SHA-256 for prepared file %q", required.Path)
		}
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("verify: prepared generation requires a root path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := preparedPathInfo(root)
	if err != nil {
		return fmt.Errorf("verify: prepared generation root: %w", err)
	}
	if !info.IsDir() {
		return errors.New("verify: prepared generation root is not a directory")
	}
	if err := validatePreparedEvidence(filepath.Join(root, PreparedEvidenceFile), expected.Identity); err != nil {
		return fmt.Errorf("verify: prepared evidence: %w", err)
	}
	for _, required := range expected.Files {
		if err := validatePreparedFile(filepath.Join(root, filepath.FromSlash(required.Path)), required); err != nil {
			return fmt.Errorf("verify: prepared file %q: %w", required.Path, err)
		}
	}
	return nil
}

func validatePreparedEvidence(path, identity string) error {
	file, err := openPreparedFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return errors.New("evidence exceeds 4096 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("evidence must be a JSON object")
	}
	fields := make(map[string]bool, 2)
	var evidence PreparedEvidence
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		field, isString := token.(string)
		if !isString || fields[field] {
			return errors.New("evidence contains an invalid or duplicate field")
		}
		fields[field] = true
		switch field {
		case "identity":
			err = decoder.Decode(&evidence.Identity)
		case "state":
			err = decoder.Decode(&evidence.State)
		default:
			return fmt.Errorf("unknown evidence field %q", field)
		}
		if err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("evidence contains trailing data")
	}
	if evidence.Identity != identity {
		return errors.New("preparation identity is missing or mismatched")
	}
	if evidence.State != PreparationComplete {
		return fmt.Errorf("preparation state %q is not complete", evidence.State)
	}
	return nil
}

func validatePreparedFile(path string, required PreparedFile) error {
	file, err := openPreparedFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != required.Size {
		return errors.New("size does not match inventory")
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(file, required.Size))
	if err != nil {
		return err
	}
	var extra [1]byte
	count, err := file.Read(extra[:])
	if err != nil && err != io.EOF {
		return err
	}
	if size != required.Size || count != 0 {
		return errors.New("size changed while reading")
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), required.SHA256) {
		return errors.New("SHA-256 does not match inventory")
	}
	return nil
}

func openPreparedFile(path string) (*os.File, error) {
	info, err := preparedPathInfo(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	file, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, fmt.Errorf("%q changed while opening", path)
	}
	return file, nil
}

// Check ancestors before the leaf so no existing link is followed to inspect it.
func preparedPathInfo(path string) (os.FileInfo, error) {
	parent := filepath.Dir(path)
	if parent != path {
		if _, err := preparedPathInfo(parent); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || isPreparedReparsePoint(info) {
		return nil, fmt.Errorf("prepared path %q is a symlink or reparse point", path)
	}
	return info, nil
}
