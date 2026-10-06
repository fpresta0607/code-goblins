package supervisor

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

const cfoQuietAfter = 10 * time.Minute

type CFOQuiet struct {
	Since     time.Time `json:"since"`
	Count     int       `json:"count"`
	OldestAge float64   `json:"oldest_age"`
}

func cfoQuietNotice(records []wake.Record, now time.Time) *CFOQuiet {
	count := 0
	var oldest time.Time
	for _, record := range records {
		// A goblin's question is its blocked or failed notify, or one the
		// monitor read in its last reply when it asked in prose instead.
		_, isNotify := wake.BlockingNotify(record)
		_, isProse := wake.ProseAsk(record, record.Key)
		if !isNotify && !isProse || record.AnsweredBy != "" || record.Time.IsZero() {
			continue
		}
		count++
		if oldest.IsZero() || record.Time.Before(oldest) {
			oldest = record.Time
		}
	}
	if oldest.IsZero() || now.Sub(oldest) < cfoQuietAfter {
		return nil
	}
	return &CFOQuiet{Count: count, OldestAge: now.Sub(oldest).Seconds()}
}

func (s *Store) keepCFOQuiet(now time.Time) error {
	records, err := wake.Pending(s.Home.State)
	if err != nil {
		return err
	}
	notice := cfoQuietNotice(records, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	since := s.db.CFOQuietSince
	if notice == nil {
		since = time.Time{}
	} else if since.IsZero() {
		since = now
	}
	if since.Equal(s.db.CFOQuietSince) {
		return nil
	}
	s.db.CFOQuietSince = since
	return s.save()
}
