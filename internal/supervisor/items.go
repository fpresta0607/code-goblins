package supervisor

import (
	"encoding/json"
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
	items := Items{Actions: d.Actions}
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

// Items reads the Command Center's items as they are now, and the revision a
// snapshot taken now carries.
func (s *Service) Items() (Items, uint64) {
	items := boardItems(s.Store.Snapshot())
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
