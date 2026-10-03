package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func isNativeInboxReadFailure(stateDir string, err error) bool {
	var pathError *os.PathError
	return errors.As(err, &pathError) && filepath.Clean(pathError.Path) == nativehook.SpoolDir(stateDir)
}

func recoverNativeInbox(stateDir string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		if repairErr := os.MkdirAll(nativehook.SpoolDir(stateDir), 0700); repairErr == nil {
			return fmt.Errorf("State folder native-inbox was missing and has been recreated; earlier native hook events may be missing: %w", err)
		} else {
			err = errors.Join(err, repairErr)
		}
	}
	return fmt.Errorf("State folder native-inbox cannot be read; native hook events are delayed while other board updates continue: %w", err)
}

func (s *Service) boardSnapshot() (Snapshot, error) {
	snapshot, err := s.Snapshot()
	if !isNativeInboxReadFailure(s.Store.Home.State, err) {
		return snapshot, err
	}
	problem := recoverNativeInbox(s.Store.Home.State, err).Error()
	if snapshot.Error == "" {
		snapshot.Error = problem
	} else {
		snapshot.Error += "\n" + problem
	}
	return snapshot, nil
}
