package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// tallySchema versions the tally, which only builds that count suppressed
// wakes read. The heartbeat and observations stay as older builds decode them.
const tallySchema = "cfo-monitor-tally.v1"

// Tally counts the task wakes the monitor raised and the stale wakes it held
// back, by reason, since Since. A stale wake needs evidence the goblin is not
// working, and a wake held back on the evidence that it is - a pane showing
// work running, or one failed screen read waiting to be read again - is
// counted here for cfo doctor. A detector that stops seeing then shows as
// nothing raised and nothing held back, never as a healthy fleet.
type Tally struct {
	Schema  string                  `json:"schema"`
	Since   time.Time               `json:"since"`
	Reasons map[Reason]*ReasonTally `json:"reasons,omitempty"`
	// Episodes holds, for each goblin and reason, the episode last held back,
	// so a stretch held back on every scan counts once.
	Episodes map[string]string `json:"episodes,omitempty"`
}

// ReasonTally is one reason's count: wakes raised, wakes held back, and the
// last one held back, with the goblin and the evidence that held it.
type ReasonTally struct {
	Raised     int       `json:"raised"`
	Suppressed int       `json:"suppressed"`
	Last       time.Time `json:"last,omitzero"`
	LastTask   string    `json:"last_task,omitempty"`
	LastWhy    string    `json:"last_why,omitempty"`
}

func tallyPath(stateDir string) string {
	return filepath.Join(stateDir, "monitor", "tally.json")
}

// ReadTally reads the tally. A missing one is empty; an unreadable one is an
// error, since cfo doctor must not show a detector's silence as health.
func ReadTally(stateDir string) (Tally, error) {
	var tally Tally
	err := readStrictJSON(tallyPath(stateDir), &tally)
	if errors.Is(err, os.ErrNotExist) {
		return Tally{}, nil
	}
	if err != nil {
		return Tally{}, err
	}
	if tally.Schema != tallySchema {
		return Tally{}, errors.New("monitor: unsupported tally schema " + tally.Schema)
	}
	return tally, nil
}

// readTallyForScan reads the tally a scan adds to. One that cannot be read
// starts again from now: the scan's own wakes matter more than the count.
func readTallyForScan(stateDir string, now time.Time) Tally {
	tally, err := ReadTally(stateDir)
	if err != nil || tally.Since.IsZero() {
		tally = Tally{Since: now}
	}
	return tally
}

func writeTally(stateDir string, tally Tally) error {
	tally.Schema = tallySchema
	return writeJSON(tallyPath(stateDir), tally)
}

func (t *Tally) reason(reason Reason) *ReasonTally {
	if t.Reasons == nil {
		t.Reasons = map[Reason]*ReasonTally{}
	}
	if t.Reasons[reason] == nil {
		t.Reasons[reason] = &ReasonTally{}
	}
	return t.Reasons[reason]
}

// suppress counts a wake held back for task, once per episode.
func (t *Tally) suppress(task string, reason Reason, episode, why string, now time.Time) {
	if t == nil {
		return
	}
	key := task + "/" + string(reason)
	if t.Episodes == nil {
		t.Episodes = map[string]string{}
	}
	if t.Episodes[key] == episode {
		return
	}
	t.Episodes[key] = episode
	counted := t.reason(reason)
	counted.Suppressed++
	counted.Last, counted.LastTask, counted.LastWhy = now, task, bounded(why, 300)
}

// raise counts a task wake raised, by the reason its detail starts with.
func (t *Tally) raise(event Event) {
	if t == nil || event.Source != TaskEvent {
		return
	}
	reason, _, _ := strings.Cut(event.Detail, ":")
	t.reason(Reason(reason)).Raised++
}

// forget drops the episodes of goblins no longer supervised.
func (t *Tally) forget(live map[string]bool) {
	for key := range t.Episodes {
		task, _, _ := strings.Cut(key, "/")
		if !live[task] {
			delete(t.Episodes, key)
		}
	}
}

// bounded cuts text to at most limit bytes on a rune boundary.
func bounded(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit] + "…"
}
