package supervisor

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/waiting"
)

// Items is the Command Center's part of a snapshot: every question, review
// item, run and credential request, and what the Overlord sent for them. It
// lives in the store's memory, so it is read at once, while the rest of a
// snapshot is read from the fleet's files and can take seconds.
type Items struct {
	Questions      []Question          `json:"questions"`
	Reviews        []Review            `json:"reviews"`
	Runs           []Run               `json:"runs"`
	Credentials    []CredentialRequest `json:"credentials"`
	BrowserSignIns []BrowserSignIn     `json:"browser_signins"`
	Actions        []Action            `json:"actions"`
	// HistoryCount is how many closed items the board's History lists; it
	// reads them when it opens.
	HistoryCount int `json:"history_count"`
}

// itemsEvent is the items with the revision they were read at, which the
// board's stream lays over what the board holds and sends as a change the
// moment one changes, between snapshots.
type itemsEvent struct {
	Instance string `json:"instance"`
	Revision uint64 `json:"revision"`
	Items
}

// boardItems is what the board sees of the store's items.
func boardItems(d Database) Items {
	// A task's progress checks are listed only in its Details, which reads
	// them when it opens: they were 234 of the 256 actions the store keeps,
	// 110 KB of every board's first snapshot, and each one queued sent every
	// action's id again (#1611).
	items := Items{Actions: slices.DeleteFunc(slices.Clone(d.Actions), func(action Action) bool { return action.Kind == "evaluate" })}
	// The board sees how many images a question has, never where they are.
	items.Questions = make([]Question, len(d.Questions))
	for i, q := range d.Questions {
		q.ImageCount, q.Images = len(q.Images), nil
		items.Questions[i] = q
	}
	// The board sees how many images a review has and what its document is,
	// never their digests.
	items.Reviews = make([]Review, len(d.Reviews))
	for i, r := range d.Reviews {
		r.ImageCount, r.ImageSums = len(r.ImageSums), nil
		if r.Document != nil {
			document := *r.Document
			document.Sum = ""
			r.Document = &document
		}
		items.Reviews[i] = r
	}
	// The board sees what runs and how it went, never the process or digest.
	items.Runs = make([]Run, len(d.Runs))
	for i, r := range d.Runs {
		r.ScriptSum, r.RunAction, r.PID, r.Started = "", "", 0, nil
		items.Runs[i] = r
	}
	// The board sees which plain values arrived, never the values, which
	// stay in the record for the goblin's wait.
	items.Credentials = make([]CredentialRequest, len(d.Credentials))
	for i, r := range d.Credentials {
		r.Values = nil
		items.Credentials[i] = r
	}
	items.BrowserSignIns = append([]BrowserSignIn{}, d.BrowserSignIns...)
	for i := range items.BrowserSignIns {
		items.BrowserSignIns[i].TaskGeneration = ""
		items.BrowserSignIns[i].VerifiedAt = nil
	}
	return items
}

// historyRows is how many closed items the board's History lists.
const historyRows = 20

// recentlyClosed is how long after an item closed the board is still sent it:
// its card finishes as answered or closed, an update that installed reloads
// the board, and History lists it at once. History reads the items closed
// before when it opens: they were 362 KB of a 501 KB first snapshot on the
// live fleet, all but 1.6 KB of them closed more than an hour before (#1611).
const recentlyClosed = time.Hour

// boardItem is what the board reads of every kind of item, as its
// commandQueue.ts does: whether it still waits, and when it closed.
type boardItem interface {
	isOpen() bool
	closedAt() time.Time
}

func (q Question) isOpen() bool { return q.Status == "pending" }

func (q Question) closedAt() time.Time { return *cmp.Or(q.AnsweredAt, &q.CreatedAt) }

func (r Review) isOpen() bool { return r.State == "open" }

func (r Review) closedAt() time.Time { return r.UpdatedAt }

func (r Run) isOpen() bool { return r.State == "ready" || r.State == "running" }

func (r Run) closedAt() time.Time { return *cmp.Or(r.FinishedAt, r.RanAt, &r.CreatedAt) }

func (r CredentialRequest) isOpen() bool { return r.State == "open" }

func (r CredentialRequest) closedAt() time.Time { return *cmp.Or(r.ClosedAt, &r.CreatedAt) }

func (b BrowserSignIn) isOpen() bool {
	return b.State == "ready" || b.State == "opening" || b.State == "open"
}

func (b BrowserSignIn) closedAt() time.Time { return *cmp.Or(b.ClosedAt, &b.CreatedAt) }

// recentItems are the items the board is sent: the open ones and those closed
// in the last recentlyClosed before now, with how many History lists.
func recentItems(items Items, now time.Time) Items {
	items.HistoryCount = min(len(closedTimes(items)), historyRows)
	since := now.Add(-recentlyClosed)
	items.Questions = closedSince(items.Questions, since, true)
	items.Reviews = closedSince(items.Reviews, since, true)
	items.Runs = closedSince(items.Runs, since, true)
	items.Credentials = closedSince(items.Credentials, since, true)
	items.BrowserSignIns = closedSince(items.BrowserSignIns, since, true)
	return items
}

// history is what the board's History lists: the newest closed items of
// every kind, historyRows of them unless several closed at the same moment.
func history(items Items) Items {
	closed := closedTimes(items)
	if len(closed) == 0 {
		return Items{}
	}
	slices.SortFunc(closed, func(one, other time.Time) int { return other.Compare(one) })
	oldest := closed[min(len(closed), historyRows)-1]
	return Items{
		Questions:      closedSince(items.Questions, oldest, false),
		Reviews:        closedSince(items.Reviews, oldest, false),
		Runs:           closedSince(listedRuns(items.Runs), oldest, false),
		Credentials:    closedSince(items.Credentials, oldest, false),
		BrowserSignIns: closedSince(items.BrowserSignIns, oldest, false),
	}
}

// closedTimes are when each closed item History lists closed.
func closedTimes(items Items) []time.Time {
	var closed []time.Time
	closed = appendClosed(closed, items.Questions)
	closed = appendClosed(closed, items.Reviews)
	closed = appendClosed(closed, listedRuns(items.Runs))
	closed = appendClosed(closed, items.Credentials)
	return appendClosed(closed, items.BrowserSignIns)
}

func appendClosed[T boardItem](closed []time.Time, items []T) []time.Time {
	for _, item := range items {
		if !item.isOpen() {
			closed = append(closed, item.closedAt())
		}
	}
	return closed
}

// closedSince keeps of items those closed at since or later, and the open
// ones when isOpenKept. It keeps them in a list of its own.
func closedSince[T boardItem](items []T, since time.Time, isOpenKept bool) []T {
	return slices.DeleteFunc(slices.Clone(items), func(item T) bool {
		return item.isOpen() && !isOpenKept || !item.isOpen() && item.closedAt().Before(since)
	})
}

// listedRuns are the runs History lists: a terminal a credential card or the
// Remote access setting opened shows there, not as a run of its own.
func listedRuns(runs []Run) []Run {
	return slices.DeleteFunc(slices.Clone(runs), func(run Run) bool { return run.CredentialRequest != "" || run.RemoteFix != "" })
}

// Items reads the Command Center's items as they are now, and the revision a
// snapshot taken now carries.
func (s *Service) Items() (Items, uint64) {
	items := recentItems(boardItems(s.Store.Snapshot()), time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	return items, s.revision
}

// Waiting lists what waits on the Overlord as the board's page counts it,
// read from the store's memory, with each goblin named as the newest snapshot
// built names it, so a look builds none. The desktop window looks every three
// seconds, and it read the whole snapshot, a megabyte, at each look (#1716).
func (s *Service) Waiting() (string, []waiting.Item, error) {
	snapshot, err := s.SnapshotSince(0)
	if err != nil {
		return "", nil, err
	}
	type title struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	titles := make([]title, len(snapshot.Tasks))
	for i, task := range snapshot.Tasks {
		titles[i] = title{task.ID, task.Title}
	}
	items, _ := s.Items()
	items.Actions = nil
	data, err := json.Marshal(struct {
		Instance string    `json:"instance"`
		At       time.Time `json:"at"`
		Tasks    []title   `json:"tasks"`
		Items
	}{s.Instance, time.Now().UTC(), titles, items})
	if err != nil {
		return "", nil, err
	}
	return waiting.Items(data)
}
