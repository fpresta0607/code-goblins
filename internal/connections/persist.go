package connections

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	machineFile      = "machine.json"
	projectsFolder   = "projects"
	goblinsFolder    = "goblins"
	snapshotFileSize = 64 << 20
)

func projectFile(name string) string { return filepath.Join(projectsFolder, name+".json") }

func goblinPath(task string) string { return filepath.Join(goblinsFolder, task+".json") }

// validFileName is whether a project's name can name its snapshot file.
func validFileName(name string) bool {
	return name != "" && len(name) <= 128 && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\:*?"<>|`) && strings.IndexFunc(name, unicode.IsControl) < 0
}

// load reads back the snapshots the folder keeps. A file that is missing or
// cannot be read is left out, and so is rechecked as one never made.
func (s *Snapshots) load() {
	var machine Level
	if s.read(machineFile, &machine) {
		machine.Checking = false
		s.machine = machine
	}
	for _, name := range s.listFolder(projectsFolder) {
		var project Project
		if s.read(projectFile(name), &project) && project.Name == name && validFileName(name) {
			project.Checking = false
			s.projects[name] = project
		}
	}
	for _, task := range s.listFolder(goblinsFolder) {
		var kept goblinFile
		if s.read(goblinPath(task), &kept) && kept.Task == task && state.ValidTaskID(task) == nil {
			kept.Checking = false
			s.goblins[task] = kept
		}
	}
}

// listFolder names the snapshots a folder keeps, by their file names without
// the extension.
func (s *Snapshots) listFolder(folder string) []string {
	entries, err := os.ReadDir(filepath.Join(s.dir, folder))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if name, isSnapshot := strings.CutSuffix(entry.Name(), ".json"); isSnapshot && !entry.IsDir() {
			names = append(names, name)
		}
	}
	return names
}

func (s *Snapshots) read(name string, into any) bool {
	data, err := readBounded(filepath.Join(s.dir, name), snapshotFileSize)
	return err == nil && json.Unmarshal(data, into) == nil
}

// save keeps one snapshot in its file, and tells Failed when it cannot.
func (s *Snapshots) save(name string, value any) {
	path := filepath.Join(s.dir, name)
	data, err := json.Marshal(value)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = fsx.AtomicWriteFile(path, data)
	}
	if err != nil && s.Failed != nil {
		s.Failed(fmt.Errorf("connector snapshot %s could not be kept: %w", name, err))
	}
}

// remove lets go of a snapshot's file.
func (s *Snapshots) remove(name string) {
	if err := fsx.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) && s.Failed != nil {
		s.Failed(fmt.Errorf("connector snapshot %s could not be removed: %w", name, err))
	}
}
