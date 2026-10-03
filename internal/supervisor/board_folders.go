package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

var errNativeInboxRecreated = errors.New("State folder native-inbox was missing and has been recreated; earlier native hook events may be missing")

func isNativeInboxReadFailure(stateDir string, err error) bool {
	var pathError *os.PathError
	if !errors.As(err, &pathError) || filepath.Clean(pathError.Path) != nativehook.SpoolDir(stateDir) {
		return false
	}
	info, statErr := os.Stat(stateDir)
	return statErr == nil && info.IsDir()
}

func recoverNativeInbox(stateDir string, err error) error {
	if !isNativeInboxReadFailure(stateDir, err) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		if repairErr := os.Mkdir(nativehook.SpoolDir(stateDir), 0700); repairErr == nil {
			return fmt.Errorf("%w: %w", errNativeInboxRecreated, err)
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
	problem := recoverNativeInbox(s.Store.Home.State, err)
	if errors.Is(problem, errNativeInboxRecreated) {
		s.mu.Lock()
		s.nativeInboxRepair = problem
		s.mu.Unlock()
	}
	if strings.Contains(snapshot.Error, problem.Error()) {
		return snapshot, nil
	}
	if snapshot.Error == "" {
		snapshot.Error = problem.Error()
	} else {
		snapshot.Error += "\n" + problem.Error()
	}
	return snapshot, nil
}
