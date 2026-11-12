package cutover

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// UndoFile is the file in an apply's folder that says how to undo it.
const UndoFile = "undo.json"

// undoRecord is everything an apply needs to be undone: each step in order,
// how many of them it made, and the folders it made for itself.
type undoRecord struct {
	Plan  string         `json:"plan"`
	Steps []recordedStep `json:"steps"`
	Done  int            `json:"done"`
	// Made are the folders above its own the apply made, such as the
	// state's backups folder, which the undo then removes.
	Made []string `json:"made,omitempty"`
}

// recordedStep is a step as the undo needs it: a file's content before is
// kept in the apply's folder under files, by the step's number.
type recordedStep struct {
	Step
	IsAbsent  bool      `json:"is_absent,omitempty"`
	IsRemoved bool      `json:"is_removed,omitempty"`
	ModTime   time.Time `json:"mod_time,omitzero"`
	// Made are the folders the step made, which its undo removes.
	Made []string `json:"made,omitempty"`
}

// Apply makes the plan, after first keeping in a new folder under the
// state's backups, which it returns, everything the undo needs. A step that
// fails removes the folders it made and undoes every step made before it.
func (m Machine) Apply(plan Plan, now time.Time) (string, error) {
	record := undoRecord{Plan: plan.Digest()}
	folder := backupFolder(m.Root, now)
	if exists(folder) {
		return "", fmt.Errorf("cutover: %s is already there", folder)
	}
	made, err := makeParents(keptFile(folder, 0))
	if err != nil {
		return "", errors.Join(err, removeMade(made))
	}
	record.Made = slices.DeleteFunc(made, func(path string) bool { return path == folder || filepath.Dir(path) == folder })
	for _, change := range plan.Changes {
		for _, step := range change.Steps {
			kept := recordedStep{Step: step, IsAbsent: step.isAbsent, IsRemoved: step.isRemoved}
			if step.Kind == "file" && !step.isAbsent {
				readFrom := step.Key
				if step.readFrom != "" {
					readFrom = step.readFrom
				}
				info, err := os.Stat(readFrom)
				if err != nil {
					return "", m.abandon(folder, record, err)
				}
				kept.ModTime = info.ModTime()
				if err := fsx.AtomicWriteFile(keptFile(folder, len(record.Steps)), step.before); err != nil {
					return "", m.abandon(folder, record, err)
				}
			}
			record.Steps = append(record.Steps, kept)
		}
	}
	if err := writeRecord(folder, record); err != nil {
		return "", m.abandon(folder, record, err)
	}
	for index := range record.Steps {
		made, err := m.do(&record.Steps[index].Step, record.Steps[index].IsRemoved, record.Steps[index].after)
		record.Steps[index].Made = made
		if err != nil {
			err = errors.Join(err, removeMade(made))
		} else {
			record.Done = index + 1
			err = writeRecord(folder, record)
		}
		if err != nil {
			err = fmt.Errorf("cutover: %s %s: %w", record.Steps[index].Kind, record.Steps[index].Key, err)
			if undoErr := m.undo(folder, record); undoErr != nil {
				return folder, errors.Join(err, fmt.Errorf("and undoing what it made before failed, so %s still says how to undo it: %w", folder, undoErr))
			}
			return "", errors.Join(err, errors.New("everything it made before was undone"))
		}
	}
	if slices.ContainsFunc(record.Steps, func(step recordedStep) bool { return step.Kind == "setting" }) {
		if err := m.Env.Broadcast(); err != nil {
			return folder, err
		}
	}
	return folder, nil
}

// extend makes plan's steps as further steps of the apply whose folder is
// folder, so its undo undoes them too. A step that fails leaves the steps
// before it made, and the record says how far it got.
func (m Machine) extend(folder string, plan Plan) error {
	data, err := fsx.ReadFile(filepath.Join(folder, UndoFile))
	if err != nil {
		return fmt.Errorf("cutover: read how to undo: %w", err)
	}
	var record undoRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("cutover: read %s: %w", filepath.Join(folder, UndoFile), err)
	}
	record.Steps = record.Steps[:record.Done]
	for _, change := range plan.Changes {
		for _, step := range change.Steps {
			if step.Kind == "file" {
				return fmt.Errorf("cutover: a later step of an apply changes no file, but %s would", step.Key)
			}
			record.Steps = append(record.Steps, recordedStep{Step: step})
		}
	}
	for index := record.Done; index < len(record.Steps); index++ {
		if _, err := m.do(&record.Steps[index].Step, false, nil); err != nil {
			failed := record.Steps[index]
			record.Steps = record.Steps[:index]
			return errors.Join(fmt.Errorf("cutover: %s %s: %w", failed.Kind, failed.Key, err), writeRecord(folder, record))
		}
		record.Done = index + 1
		if err := writeRecord(folder, record); err != nil {
			return err
		}
	}
	return nil
}

// backupFolder is the folder an apply at now keeps its undo in.
func backupFolder(root string, now time.Time) string {
	return filepath.Join(root, "state", "backups", "cutover-"+now.UTC().Format("20060102T150405Z"))
}

// isProgram says whether path is a program in the home's bin, which a
// process may run: Windows refuses to write over or remove a program while
// it runs, but renames it, so each is set aside before it is written or
// removed.
func (m Machine) isProgram(path string) bool {
	rel, err := filepath.Rel(filepath.Join(m.Root, "bin"), path)
	return err == nil && filepath.IsLocal(rel) && strings.EqualFold(filepath.Ext(path), ".exe")
}

// setAside renames the program at path out of the way, under the name an
// update gives a program it set aside, and removes it there unless something
// still runs it, which leaves it for the janitor.
func setAside(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	aside := fmt.Sprintf("%s.%d.update-old", path, time.Now().UnixNano())
	if err := fsx.WaitOut(func() error { return os.Rename(path, aside) }); err != nil {
		return err
	}
	_ = os.Remove(aside)
	return nil
}

// abandon removes the folder of an apply that changed nothing yet.
func (m Machine) abandon(folder string, record undoRecord, err error) error {
	if removeErr := removeFolder(folder, record); removeErr != nil {
		return errors.Join(err, removeErr)
	}
	return err
}

// Undo undoes the apply whose folder is folder, every step it made in the
// reverse order, and removes the folder.
func (m Machine) Undo(folder string) error {
	data, err := fsx.ReadFile(filepath.Join(folder, UndoFile))
	if err != nil {
		return fmt.Errorf("cutover: read how to undo: %w", err)
	}
	var record undoRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("cutover: read %s: %w", filepath.Join(folder, UndoFile), err)
	}
	return m.undo(folder, record)
}

func (m Machine) undo(folder string, record undoRecord) error {
	for index := record.Done - 1; index >= 0; index-- {
		if err := m.undoStep(folder, index, record.Steps[index]); err != nil {
			record.Done = index + 1
			if writeErr := writeRecord(folder, record); writeErr != nil {
				err = errors.Join(err, writeErr)
			}
			return fmt.Errorf("cutover: undo %s %s: %w", record.Steps[index].Kind, record.Steps[index].Key, err)
		}
	}
	if slices.ContainsFunc(record.Steps[:record.Done], func(step recordedStep) bool { return step.Kind == "setting" }) {
		if err := m.Env.Broadcast(); err != nil {
			return err
		}
	}
	return removeFolder(folder, record)
}

func removeFolder(folder string, record undoRecord) error {
	if err := fsx.RemoveAll(folder); err != nil {
		return err
	}
	return removeMade(record.Made)
}

func keptFile(folder string, index int) string {
	return filepath.Join(folder, "files", strconv.Itoa(index))
}

func writeRecord(folder string, record undoRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(folder, UndoFile), append(data, '\n'))
}

// makeParents makes the folders above path that are not there, and returns
// the ones it made, deepest last, even when it fails.
func makeParents(path string) ([]string, error) {
	var missing []string
	for folder := filepath.Dir(path); !exists(folder); folder = filepath.Dir(folder) {
		missing = append([]string{folder}, missing...)
	}
	for index, folder := range missing {
		if err := os.Mkdir(folder, 0o755); err != nil {
			return missing[:index], err
		}
	}
	return missing, nil
}

// removeAbsent removes the file at path, which a program set aside first
// no longer is.
func removeAbsent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func removeMade(made []string) error {
	for index := len(made) - 1; index >= 0; index-- {
		if err := os.Remove(made[index]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// do makes one step and returns the folders it made.
func (m Machine) do(step *Step, isRemoved bool, after []byte) ([]string, error) {
	switch step.Kind {
	case "file":
		if m.isProgram(step.Key) {
			if err := setAside(step.Key); err != nil {
				return nil, err
			}
		}
		if isRemoved {
			return nil, removeAbsent(step.Key)
		}
		made, err := makeParents(step.Key)
		if err != nil {
			return made, err
		}
		return made, fsx.AtomicWriteFile(step.Key, after)
	case "move":
		made, err := makeParents(step.To)
		if err != nil {
			return made, err
		}
		return made, os.Rename(step.Key, step.To)
	case "link":
		if step.After == nil {
			return nil, os.Remove(step.Key)
		}
		made, err := makeParents(step.Key)
		if err != nil {
			return made, err
		}
		return made, m.Link(step.Key, *step.After)
	case "setting":
		if step.After == nil {
			return nil, m.Env.Unset(step.Key)
		}
		return nil, m.Env.Set(step.Key, *step.After)
	case "job":
		if step.After == nil {
			return nil, m.Jobs.Delete(step.Key)
		}
		return nil, m.Jobs.Register(Job{Name: step.Key, XML: *step.After})
	case "credential":
		return nil, m.moveCredential(step.Key, step.To, auth.VaultUser)
	case "label":
		return nil, m.Labels.Rename(step.Key, step.From, step.To)
	}
	return nil, fmt.Errorf("unknown step %q", step.Kind)
}

func (m Machine) undoStep(folder string, index int, step recordedStep) error {
	switch step.Kind {
	case "file":
		if m.isProgram(step.Key) {
			if err := setAside(step.Key); err != nil {
				return err
			}
		}
		if step.IsAbsent {
			if err := removeAbsent(step.Key); err != nil {
				return err
			}
			return removeMade(step.Made)
		}
		content, err := fsx.ReadFile(keptFile(folder, index))
		if err != nil {
			return err
		}
		if err := fsx.AtomicWriteFile(step.Key, content); err != nil {
			return err
		}
		return os.Chtimes(step.Key, step.ModTime, step.ModTime)
	case "move":
		if err := os.Rename(step.To, step.Key); err != nil {
			return err
		}
		return removeMade(step.Made)
	case "link":
		if step.Before == nil {
			if err := os.Remove(step.Key); err != nil {
				return err
			}
			return removeMade(step.Made)
		}
		return m.Link(step.Key, *step.Before)
	case "setting":
		if step.Before == nil {
			return m.Env.Unset(step.Key)
		}
		return m.Env.Set(step.Key, *step.Before)
	case "job":
		if step.Before == nil {
			return m.Jobs.Delete(step.Key)
		}
		return m.Jobs.Register(Job{Name: step.Key, XML: *step.Before})
	case "credential":
		return m.moveCredential(step.To, step.Key, step.User)
	case "label":
		return m.Labels.Rename(step.Key, step.To, step.From)
	}
	return fmt.Errorf("unknown step %q", step.Kind)
}

// moveCredential writes the credential at from as to, for user, and then
// removes from, so its secret never leaves Credential Manager. When from
// cannot be removed, to is removed again, so the secret is under one target.
func (m Machine) moveCredential(from, to, user string) error {
	entries, err := m.Vault.Entries(from)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(entries, func(entry auth.VaultEntry) bool { return entry.Target == from })
	if index < 0 {
		return fmt.Errorf("Credential Manager holds no %s", from)
	}
	if err := m.Vault.Write(auth.VaultEntry{Target: to, User: user, Value: entries[index].Value}); err != nil {
		return err
	}
	if err := m.Vault.Delete(from); err != nil {
		return errors.Join(err, m.Vault.Delete(to))
	}
	return nil
}
