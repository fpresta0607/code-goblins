package supervisor

// Items is the Command Center's part of a snapshot: every question, review
// item, run and credential request, and what the Overlord sent for them. It
// lives in the store's memory, so it is read at once, while the rest of a
// snapshot is read from the fleet's files and can take seconds.
type Items struct {
	Questions   []Question          `json:"questions"`
	Reviews     []Review            `json:"reviews"`
	Runs        []Run               `json:"runs"`
	Credentials []CredentialRequest `json:"credentials"`
	Actions     []Action            `json:"actions"`
}

// itemsEvent is the items alone as the board's stream sends them, between
// snapshots, the moment one changes.
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
	// A goblin's question asked while its review page is open is that page's
	// item, so the Command Center shows one card: each names the other, the
	// page its newest pending question.
	for i := range items.Reviews {
		r := &items.Reviews[i]
		for j := range items.Questions {
			if q := &items.Questions[j]; carriesQuestion(*r, *q) {
				q.Page, r.Question = r.ID, q.ID
			}
		}
	}
	// The board sees what runs and how it went, never the process or digest.
	items.Runs = make([]Run, len(d.Runs))
	for i, r := range d.Runs {
		r.ScriptSum, r.RunAction, r.PID, r.Started = "", "", 0, nil
		items.Runs[i] = r
	}
	items.Credentials = append([]CredentialRequest{}, d.Credentials...)
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
