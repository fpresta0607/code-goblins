package supervisor

import (
	"errors"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/goblinname"
)

// nameQueued gives each queued task among tasks the goblin it starts as, so
// its card in Tasks names its goblin as a running goblin's card does: the
// pair it was queued with, with the title its brief gives once it gives one,
// and a new one for a task queued since the last look. The pair of a task no
// longer queued is forgotten.
func (s *Service) nameQueued(tasks []Task) error {
	directory := s.Store.Home.State
	paths := []string{goblinname.QueuedPath(directory)}
	read := func() (map[string]goblinname.Pair, error) { return goblinname.ReadQueued(directory) }
	pairs, err := kept(&s.reads, "queued-names", paths, read)
	if err != nil {
		return err
	}
	briefOf := func(id string) string { return filepath.Join(s.Store.Home.Data, id, "brief.md") }
	queued := 0
	isChanged := false
	for _, task := range tasks {
		if task.Phase == "queued" {
			pair, isNamed := pairs[task.ID]
			// A brief not written yet gives no title.
			given, _ := kept(&s.reads, "goblin-title", []string{briefOf(task.ID)}, func() (string, error) {
				brief, err := fsx.ReadFile(briefOf(task.ID))
				return goblinname.WorkOf(task.ID, task.Title, string(brief)).GivenTitle, err
			})
			queued, isChanged = queued+1, isChanged || !isNamed || given != "" && given != pair.Title
		}
	}
	if isChanged || queued != len(pairs) {
		var works []goblinname.Work
		for _, task := range tasks {
			if task.Phase == "queued" {
				// A brief not written yet leaves the title to fit the task's
				// title and id alone.
				brief, _ := fsx.ReadFile(briefOf(task.ID))
				works = append(works, goblinname.WorkOf(task.ID, task.Title, string(brief)))
			}
		}
		_, reserveErr := goblinname.Reserve(directory, works)
		pairs, err = keptWritten(&s.reads, "queued-names", paths, read)
		err = errors.Join(reserveErr, err)
	}
	for i := range tasks {
		if pair, isNamed := pairs[tasks[i].ID]; isNamed && tasks[i].Phase == "queued" {
			tasks[i].GoblinName, tasks[i].GoblinTitle = pair.Name, pair.Title
		}
	}
	return err
}
