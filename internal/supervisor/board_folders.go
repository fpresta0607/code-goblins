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
		repairErr := os.Mkdir(nativehook.SpoolDir(stateDir), 0700)
		if repairErr == nil {
			return fmt.Errorf("%w: %w", errNativeInboxRecreated, err)
		}
		if _, readErr := os.ReadDir(nativehook.SpoolDir(stateDir)); errors.Is(repairErr, os.ErrExist) && readErr == nil {
			return nil
		}
		err = errors.Join(err, repairErr)
	}
	return fmt.Errorf("State folder native-inbox cannot be read; native hook events are delayed while other board updates continue: %w", err)
}

func (s *Service) boardSnapshot() (Snapshot, error) {
	return s.SnapshotSince(s.Revision())
}

func (s *Service) buildBoardSnapshot() (Snapshot, error) {
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
	if problem == nil || strings.Contains(snapshot.Error, problem.Error()) {
		return snapshot, nil
	}
	if snapshot.Error == "" {
		snapshot.Error = problem.Error()
	} else {
		snapshot.Error += "\n" + problem.Error()
	}
	return snapshot, nil
}
