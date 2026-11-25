package handoff

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// CloudSighting is an open pull request into a handed-off task's branch as
// the supervisor's watch last saw it, which the Command Center shows as
// Cloud work ready until the Overlord leaves it or its head moves.
type CloudSighting struct {
	PullRequest string `json:"pull_request"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Head        string `json:"head"`
	// IsTeammates says its author is not the account gh works as, so only
	// the Overlord's press brings it back.
	IsTeammates bool      `json:"is_teammates,omitempty"`
	SeenAt      time.Time `json:"seen_at"`
	IsLeft      bool      `json:"is_left,omitempty"`
}

// Press is the Overlord's Bring back on a cloud pull request at the head
// the board showed him. Only the board writes it, so a teammate's pull
// request is brought back by his press alone, and a push after it needs a
// new one.
type Press struct {
	PullRequest string    `json:"pull_request"`
	Head        string    `json:"head"`
	At          time.Time `json:"at"`
}

// OpenPullRequest is an open pull request of the task's repository as the
// supervisor's watch lists it.
type OpenPullRequest struct {
	URL, Title, Base, Head, Author string
	IsCrossRepository              bool
}

// HandedOff reads the handoff record of the task meta names and says
// whether the task is handed off in its current session.
func HandedOff(stateDir string, meta state.TaskMeta) (Record, bool, error) {
	record, err := ReadRecord(stateDir, meta.ID)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil || !record.IsHandedOff() {
		return record, false, err
	}
	isPaused, err := isPausedForHandoff(stateDir, meta)
	return record, isPaused, err
}

// Sight records, for a task handed off in its current session, the open
// pull requests into its branch from its own repository other than the
// handoff's own: one sighting each at its newest head, keeping one the
// Overlord left only while its head stays the same. It says whether a pull
// request or a head is new. A task not handed off is left as it is, and so
// is one whose handoff lock a command holds, for the next watch to see.
func Sight(stateDir string, meta state.TaskMeta, open []OpenPullRequest, account string, now time.Time) (bool, error) {
	unlock, err := Service{StateDir: stateDir}.lock(meta.ID)
	if errors.Is(err, lock.ErrHeld) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	isNew, err := sight(stateDir, meta, open, account, now)
	return isNew, errors.Join(err, unlock())
}

func sight(stateDir string, meta state.TaskMeta, open []OpenPullRequest, account string, now time.Time) (bool, error) {
	record, isHandedOff, err := HandedOff(stateDir, meta)
	if err != nil || !isHandedOff {
		return false, err
	}
	var sightings []CloudSighting
	isNew := false
	for _, pr := range open {
		if pr.Base != record.Branch || pr.IsCrossRepository || pr.URL == record.PullRequest {
			continue
		}
		index := slices.IndexFunc(record.Cloud, func(seen CloudSighting) bool { return seen.PullRequest == pr.URL && seen.Head == pr.Head })
		if index >= 0 {
			sightings = append(sightings, record.Cloud[index])
			continue
		}
		isNew = true
		sightings = append(sightings, CloudSighting{PullRequest: pr.URL, Title: pr.Title, Author: pr.Author, Head: pr.Head, IsTeammates: !strings.EqualFold(pr.Author, account), SeenAt: now.UTC()})
	}
	if slices.Equal(sightings, record.Cloud) {
		return false, nil
	}
	record.Cloud = sightings
	return isNew, WriteRecord(stateDir, record)
}

// RecordPress records the Overlord's Bring back on the cloud pull request url at
// head, the one the board showed him, and refuses a head the watch no
// longer sees there.
func RecordPress(stateDir string, meta state.TaskMeta, url, head string, now time.Time) error {
	return changeSighting(stateDir, meta, url, head, func(record *Record, _ int) {
		record.Press = &Press{PullRequest: url, Head: head, At: now.UTC()}
	})
}

// Leave records the Overlord's Leave it on the cloud pull request url at
// head, so its item closes until its head moves.
func Leave(stateDir string, meta state.TaskMeta, url, head string) error {
	return changeSighting(stateDir, meta, url, head, func(record *Record, index int) {
		record.Cloud[index].IsLeft = true
	})
}

func changeSighting(stateDir string, meta state.TaskMeta, url, head string, change func(*Record, int)) (err error) {
	unlock, err := Service{StateDir: stateDir}.lock(meta.ID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	record, isHandedOff, err := HandedOff(stateDir, meta)
	if err != nil {
		return err
	}
	if !isHandedOff {
		return fmt.Errorf("%s is not handed off", meta.ID)
	}
	index := slices.IndexFunc(record.Cloud, func(seen CloudSighting) bool { return seen.PullRequest == url && seen.Head == head })
	if index < 0 {
		return fmt.Errorf("%s is not open into %s at %s, or its head moved; refresh the board", url, record.Branch, head)
	}
	change(&record, index)
	return WriteRecord(stateDir, record)
}
