package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type OpenItem struct {
	Kind string
	ID   string
	Task string
	Text string
}

// OpenItems reads the Command Center without opening or recovering its store.
func OpenItems(stateDir string) ([]OpenItem, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > maxStateBytes {
		return nil, errors.New("supervisor state exceeds its bound")
	}
	var database Database
	if err := json.Unmarshal(data, &database); err != nil {
		return nil, fmt.Errorf("supervisor state is corrupt: %w", err)
	}
	if database.Schema != 1 {
		return nil, errors.New("invalid supervisor state schema")
	}
	var items []OpenItem
	for _, item := range waitingOnOverlord(database) {
		items = append(items, OpenItem{Kind: item.kind, ID: item.id, Task: item.task, Text: item.what})
	}
	return items, nil
}
