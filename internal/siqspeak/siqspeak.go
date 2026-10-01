package siqspeak

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Snapshot struct {
	State string `json:"state"`
	History
	Problem string `json:"problem,omitempty"`
}

func Locate(projectsRoot, override string) (string, error) {
	if override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("CFO_SIQSPEAK_DIR must be an absolute directory")
		}
		return override, nil
	}
	if projectsRoot == "" {
		return "", nil
	}
	folders, err := os.ReadDir(projectsRoot)
	if err != nil {
		return "", errors.New("SIQspeak installation folder could not be located")
	}
	var directory string
	for _, folder := range folders {
		if !strings.EqualFold(folder.Name(), "SIQspeak") && !strings.EqualFold(folder.Name(), "SIQspeak-main") {
			continue
		}
		candidate := filepath.Join(projectsRoot, folder.Name())
		isInstalled, err := installed(candidate)
		if err != nil {
			return "", err
		}
		if !isInstalled {
			continue
		}
		if directory != "" {
			return "", errors.New("More than one SIQspeak installation was found; set CFO_SIQSPEAK_DIR to the one you use")
		}
		directory = candidate
	}
	return directory, nil
}

func installed(directory string) (bool, error) {
	if directory == "" {
		return false, nil
	}
	for _, name := range []string{"dictate.py", "SIQspeak.exe"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, errors.New("SIQspeak installation could not be read")
		}
		if info.Mode().IsRegular() {
			return true, nil
		}
	}
	return false, nil
}

func Read(directory string) (Snapshot, error) {
	return snapshot(directory, running)
}

func snapshot(directory string, probe func() (bool, error)) (Snapshot, error) {
	result := Snapshot{State: "missing", History: History{Entries: []Entry{}}}
	isInstalled, err := installed(directory)
	if err != nil || !isInstalled {
		return result, err
	}
	isRunning, err := probe()
	if err != nil {
		return result, errors.New("SIQspeak running status could not be read")
	}
	result.State = "stopped"
	if isRunning {
		result.State = "running"
	}
	result.History, err = readHistory(filepath.Join(directory, "transcriptions.jsonl"))
	if err != nil {
		result.Problem = err.Error()
	}
	return result, nil
}
