package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// Controllers reads local controller identities from the queue, held slots
// and worker custody together. Callers must match their PID and creation
// time against the processes they observed; an old record protects nothing.
func Controllers(ctx context.Context, dir string) (controllers []lock.Info, failure error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	unlock, err := custodyMutationContext(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() { failure = errors.Join(failure, unlock()) }()
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	for _, folder := range []string{dir, filepath.Join(dir, "line")} {
		entries, err := os.ReadDir(folder)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || strings.HasSuffix(name, ".run") || folder == dir && name != "slot-1" && name != "slot-2" {
				continue
			}
			record, err := lock.ReadNamedStrictContext(ctx, folder, name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if record.Hostname == hostname && record.PID > 0 && !record.Start.IsZero() {
				controllers = append(controllers, *record)
			}
		}
	}
	guards, err := (Admission{Dir: dir}).workerCustodyContext(ctx)
	if err != nil {
		return nil, err
	}
	for _, guard := range guards {
		if guard.Controller.PID <= 0 || guard.Controller.Start.IsZero() || guard.Controller.Hostname == "" {
			return nil, fmt.Errorf("verify: incomplete controller custody for %s", guard.RunID)
		}
		if guard.Controller.Hostname == hostname {
			controllers = append(controllers, guard.Controller)
		}
	}
	return controllers, nil
}
