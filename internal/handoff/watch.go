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
	// Commits is how many commits it holds at Head, and Body how its body
	// begins there, on one line, which says what the cloud session ran.
	Commits int    `json:"commits,omitempty"`
	Body    string `json:"body,omitempty"`
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
// supervisor's watch lists it, with its commits and body, which the watch
// reads only for one the record has not seen at its head.
type OpenPullRequest struct {
	URL, Title, Base, Head, Author string
	IsCrossRepository              bool
	Commits                        int
	Body                           string
}

// bodyRunes is how much of a cloud pull request's body a sighting keeps.
const bodyRunes = 280

// isCloud says pr is one of the cloud's into the handoff's branch: from the
// same repository and not the one the packet is in.
func (record Record) isCloud(pr OpenPullRequest) bool {
	return pr.Base == record.Branch && !pr.IsCrossRepository && pr.URL != record.PullRequest
}

// sightingOf is the index of the record's sighting of pr at its head, or -1.
func (record Record) sightingOf(pr OpenPullRequest) int {
	return slices.IndexFunc(record.Cloud, func(seen CloudSighting) bool { return seen.PullRequest == pr.URL && seen.Head == pr.Head })
}

// IsUnseen says pr is one of the cloud's the record has not seen at its
// head, or saw there without its commits, whose commits and body the watch
// reads before it sights it.
func (record Record) IsUnseen(pr OpenPullRequest) bool {
	index := record.sightingOf(pr)
	return record.isCloud(pr) && (index < 0 || record.Cloud[index].Commits == 0)
}

// excerpt is how body begins, its words on one line, cut at bodyRunes.
func excerpt(body string) string {
	words := []rune(strings.Join(strings.Fields(body), " "))
	if len(words) <= bodyRunes {
		return string(words)
	}
	return string(words[:bodyRunes]) + "..."
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
// Overlord left only while its head stays the same. One seen at its head
// without its commits takes what was read, staying left if it was. It says
// whether a pull request, a head or what one holds is new. A task not handed off is left as it is, and so
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
		if !record.isCloud(pr) {
			continue
		}
		index := record.sightingOf(pr)
		if !record.IsUnseen(pr) {
			sightings = append(sightings, record.Cloud[index])
			continue
		}
		sighting := CloudSighting{PullRequest: pr.URL, Title: pr.Title, Author: pr.Author, Head: pr.Head, Commits: pr.Commits, Body: excerpt(pr.Body), IsTeammates: !strings.EqualFold(pr.Author, account), SeenAt: now.UTC()}
		if index >= 0 {
			sighting.SeenAt, sighting.IsLeft = record.Cloud[index].SeenAt, record.Cloud[index].IsLeft
		}
		isNew = isNew || index < 0 || sighting != record.Cloud[index]
		sightings = append(sightings, sighting)
	}
	if slices.Equal(sightings, record.Cloud) {
		return false, nil
	}
	record.Cloud = sightings
	return isNew, WriteRecord(stateDir, record)
}

// CheckSighting refuses the cloud pull request url at head unless the
// watch sees it there, the one the board showed him, and he has not left
// it.
func CheckSighting(stateDir string, meta state.TaskMeta, url, head string) error {
	record, index, err := findSighting(stateDir, meta, url, head)
	if err != nil {
		return err
	}
	return refuseLeft(record.Cloud[index])
}

// RecordPress records the Overlord's Bring back on the cloud pull request url at
// head, the one the board showed him, and refuses a head the watch no
// longer sees there or one he left.
func RecordPress(stateDir string, meta state.TaskMeta, url, head string, now time.Time) error {
	return changeSighting(stateDir, meta, url, head, func(record *Record, index int) error {
		if err := refuseLeft(record.Cloud[index]); err != nil {
			return err
		}
		record.Press = &Press{PullRequest: url, Head: head, At: now.UTC()}
		return nil
	})
}

// Leave records the Overlord's Leave it on the cloud pull request url at
// head, so its item closes until its head moves, and takes back his press
// on it.
func Leave(stateDir string, meta state.TaskMeta, url, head string) error {
	return changeSighting(stateDir, meta, url, head, func(record *Record, index int) error {
		record.Cloud[index].IsLeft = true
		if record.Press != nil && record.Press.PullRequest == url {
			record.Press = nil
		}
		return nil
	})
}

func changeSighting(stateDir string, meta state.TaskMeta, url, head string, change func(*Record, int) error) (err error) {
	unlock, err := Service{StateDir: stateDir}.lock(meta.ID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	record, index, err := findSighting(stateDir, meta, url, head)
	if err != nil {
		return err
	}
	if err := change(&record, index); err != nil {
		return err
	}
	return WriteRecord(stateDir, record)
}

func refuseLeft(seen CloudSighting) error {
	if seen.IsLeft {
		return fmt.Errorf("%s was left at %s, so it is not brought back", seen.PullRequest, seen.Head)
	}
	return nil
}

func findSighting(stateDir string, meta state.TaskMeta, url, head string) (Record, int, error) {
	record, isHandedOff, err := HandedOff(stateDir, meta)
	if err != nil {
		return record, -1, err
	}
	if !isHandedOff {
		return record, -1, fmt.Errorf("%s is not handed off", meta.ID)
	}
	index := slices.IndexFunc(record.Cloud, func(seen CloudSighting) bool { return seen.PullRequest == url && seen.Head == head })
	if index < 0 {
		return record, -1, fmt.Errorf("%s is not open into %s at %s, or its head moved; refresh the board", url, record.Branch, head)
	}
	return record, index, nil
}
