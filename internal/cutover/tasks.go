package cutover

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// largestTextFile is the largest file the cutover reads as text; a larger one
// is no brief, memory or settings file.
const largestTextFile = 4 << 20

// historyFolders are folders whose files are recorded history, which keep
// every word they were written with.
var historyFolders = []string{"archive", "backups", ".git", "node_modules"}

// historyExtensions are the kinds of file that record what happened, which
// keep every word they were written with.
var historyExtensions = []string{".log", ".jsonl", ".status", ".audit", ".end", ".proofs"}

// prose are the kinds of file read as prose, in which the Supreme Overlord's
// quoted words are kept as he wrote them.
var prose = []string{".md", ".txt"}

// taskFiles rewrites the live task files that call the old command: the
// backlog, the memory, the project records, each live task's folder and
// instructions, the home's own scripts and its config.
func (p *planner) taskFiles() error {
	var live []string
	entries, err := os.ReadDir(filepath.Join(p.state(), "tasktmp"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			live = append(live, entry.Name())
		}
	}
	folders := []string{
		filepath.Join(p.data(), "memory"),
		filepath.Join(p.data(), "projects"),
		filepath.Join(p.state(), "tasktmp"),
		filepath.Join(p.state(), "ops"),
		filepath.Join(p.machine.Root, "config"),
	}
	for _, id := range live {
		folders = append(folders, filepath.Join(p.data(), id))
	}
	if err := p.rewriteTaskFile(filepath.Join(p.data(), "backlog.md")); err != nil {
		return err
	}
	for _, folder := range folders {
		err := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == folder {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if slices.Contains(historyFolders, strings.ToLower(entry.Name())) {
					return filepath.SkipDir
				}
				return nil
			}
			return p.rewriteTaskFile(path)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *planner) rewriteTaskFile(path string) error {
	name := strings.ToLower(filepath.Base(path))
	extension := filepath.Ext(name)
	if slices.Contains(historyExtensions, extension) || strings.Contains(name, "overlord") {
		return nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() > largestTextFile {
		return nil
	}
	var protected = quotedWords
	if !slices.Contains(prose, extension) {
		protected = nil
	}
	return p.rewrite(KindTask, path, path, protected)
}

// labels renames, in each repository a task's ticket was kept in, the
// labels the old build gave a ticket's state, such as "cfo: running".
func (p *planner) labels() error {
	records, err := tickets.ListRecords(p.state())
	if err != nil {
		return fmt.Errorf("cutover: read the tickets: %w", err)
	}
	var repositories []string
	for _, record := range records {
		if record.Repository != "" && !slices.Contains(repositories, record.Repository) {
			repositories = append(repositories, record.Repository)
		}
	}
	slices.Sort(repositories)
	for _, repository := range repositories {
		labels, err := p.machine.Labels.Labels(repository)
		if err != nil {
			return fmt.Errorf("cutover: read the labels of %s: %w", repository, err)
		}
		for _, label := range labels {
			state, isOld := strings.CutPrefix(label, oldName+": ")
			if !isOld {
				continue
			}
			renamed := newName + ": " + state
			if slices.Contains(labels, renamed) {
				p.add(KindListed, fmt.Sprintf("%s label %q: kept, since %q is already there", repository, label, renamed))
				continue
			}
			p.add(KindLabel, fmt.Sprintf("%s: %q becomes %q on every issue that carries it", repository, label, renamed), Step{Kind: "label", Key: repository, From: label, To: renamed})
		}
	}
	return nil
}

// elsewhere lists the files in the Overlord's other checkouts that name the
// old command, which the cutover never changes: their owners do, in their own
// work.
func (p *planner) elsewhere() error {
	if p.machine.ProjectsRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(p.machine.ProjectsRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		checkout := filepath.Join(p.machine.ProjectsRoot, entry.Name())
		if !entry.IsDir() || !exists(filepath.Join(checkout, ".git")) || exists(filepath.Join(checkout, "cmd", newName)) || exists(filepath.Join(checkout, "cmd", oldName)) {
			continue
		}
		out, err := p.machine.Git(checkout, "ls-files", "-z")
		if err != nil {
			p.add(KindListed, fmt.Sprintf("%s: its files could not be listed: %v", checkout, err))
			continue
		}
		for _, file := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
			if file == "" {
				continue
			}
			path := filepath.Join(checkout, filepath.FromSlash(file))
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() > largestTextFile {
				continue
			}
			content, err := os.ReadFile(path)
			if err != nil || isBinary(content) {
				continue
			}
			var numbers []string
			for number, line := range bytes.Split(content, []byte("\n")) {
				if p.rules.words(string(line)) != string(line) {
					numbers = append(numbers, fmt.Sprint(number+1))
				}
			}
			if len(numbers) > 0 {
				p.add(KindListed, fmt.Sprintf("%s: names the old command on line %s", path, strings.Join(numbers, ", ")))
			}
		}
	}
	return nil
}
