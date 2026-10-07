package state

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// HelpersOf lists the live helpers of task parent: the tasks whose records
// name it as their parent. A record that cannot be read is an error, never a
// helper that is not there, since the cap it counts toward is the fleet's.
func HelpersOf(stateDir, parent string) ([]TaskMeta, error) {
	scan, err := ScanIDs(stateDir)
	if err != nil {
		return nil, err
	}
	var helpers []TaskMeta
	for _, id := range scan.MetaIDs {
		if ValidTaskID(id) != nil {
			continue
		}
		meta, err := ReadTaskMeta(stateDir, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if meta.Parent != "" && strings.EqualFold(meta.Parent, parent) {
			helpers = append(helpers, meta)
		}
	}
	return helpers, nil
}

// HelperParent reads task parent's record when it may have a helper started
// now, and says why not otherwise: it must be a live task, not a helper
// itself, and have no live helper, a paused one included. Each refusal says
// when to ask again.
func HelperParent(stateDir, parent string) (TaskMeta, error) {
	meta, err := ReadTaskMeta(stateDir, parent)
	if errors.Is(err, os.ErrNotExist) {
		return TaskMeta{}, fmt.Errorf("%s is not a live task; only a running goblin can ask for a helper", parent)
	}
	if err != nil {
		return TaskMeta{}, err
	}
	if meta.Parent != "" {
		return TaskMeta{}, fmt.Errorf("%s is a helper of %s, and a helper cannot start helpers; ask %s to start another one once you are done", parent, meta.Parent, meta.Parent)
	}
	helpers, err := HelpersOf(stateDir, parent)
	if err != nil {
		return TaskMeta{}, err
	}
	if len(helpers) > 0 {
		helper := helpers[0].ID
		return TaskMeta{}, fmt.Errorf("%s already has a helper, %s, and a goblin has one helper at a time; ask again once you have merged it with cfo helper merge %s or stopped it with cfo kill %s", parent, helper, parent, helper)
	}
	return meta, nil
}

// NextHelperID names task parent's next helper <parent>-h<n>, n one more
// than the highest any record or status log of a helper of parent holds.
// Status logs outlive cleanup, so an id is never given twice and a finished
// helper's history never mixes with the next one's.
func NextHelperID(stateDir, parent string) (string, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	highest := 0
	prefix := strings.ToLower(parent) + "-h"
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		id, isMeta := strings.CutSuffix(name, ".meta")
		if !isMeta {
			id, _ = strings.CutSuffix(name, ".status")
		}
		digits, isHelper := strings.CutPrefix(id, prefix)
		if entry.IsDir() || id == name || !isHelper {
			continue
		}
		if n, err := strconv.Atoi(digits); err == nil && n > highest && strconv.Itoa(n) == digits {
			highest = n
		}
	}
	id := parent + "-h" + strconv.Itoa(highest+1)
	if err := ValidTaskID(id); err != nil {
		return "", fmt.Errorf("%s is too long a task ID to name its helper %s: %w", parent, id, err)
	}
	return id, nil
}
