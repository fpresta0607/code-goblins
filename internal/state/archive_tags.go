package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// archiveTagsFile is where the home records each tag cleanup made in a
// person's checkout to keep a goblin's work that had not landed, one JSON
// object to a line, so an uninstall keeps that work and removes exactly
// those tags, never one of the person's own.
const archiveTagsFile = "archive-tags.jsonl"

// ArchiveTag is one tag cleanup made in a checkout.
type ArchiveTag struct {
	Checkout string `json:"checkout"`
	Tag      string `json:"tag"`
}

// RecordArchiveTag adds tag to the home's record. A cleanup appends one line,
// which two cleanups at once cannot interleave.
func RecordArchiveTag(stateDir string, tag ArchiveTag) error {
	line, err := json.Marshal(tag)
	if err != nil {
		return err
	}
	file, err := fsx.OpenAppend(filepath.Join(stateDir, archiveTagsFile), 0o644)
	if err != nil {
		return fmt.Errorf("state: record the archive tag %s: %w", tag.Tag, err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("state: record the archive tag %s: %w", tag.Tag, err)
	}
	return nil
}

// ArchiveTags reads the home's record of the tags cleanup made, each once.
func ArchiveTags(stateDir string) ([]ArchiveTag, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, archiveTagsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tags []ArchiveTag
	seen := map[ArchiveTag]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var tag ArchiveTag
		if err := json.Unmarshal([]byte(line), &tag); err != nil {
			return nil, fmt.Errorf("state: read %s: %w", archiveTagsFile, err)
		}
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags, nil
}
