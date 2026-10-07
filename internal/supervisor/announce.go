package supervisor

import (
	"net/http"
	"slices"
	"time"
)

// The board announces each Command Center item that asks the Overlord
// something once, and nothing else: it sends the item's Windows notification
// only for a key it claimed here first. The record is the supervisor's, so a
// reload, a second tab, a reconnect or a supervisor restart never announces
// the same item again (the Overlord, 2026-10-01: "no double fire or display").
// While AFK mode is on the board is handed nothing at all, and what it asked
// about then stays recorded, so it is not announced once he is back either.
const (
	// announcedFor is how long an announcement is remembered; an item open
	// that long was announced long ago.
	announcedFor = 30 * 24 * time.Hour
	// maxAnnounced bounds the record; the oldest announcement leaves first.
	maxAnnounced    = 2048
	maxAnnounceKeys = 256
	maxAnnounceKey  = 512
)

// Announcement is a key the board announced and when.
type Announcement struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// claimAnnounced records the keys nobody announced yet and returns them, each
// once. It saves only when it records something, and a save that fails
// records nothing.
func (s *Store) claimAnnounced(keys []string, now time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := slices.DeleteFunc(slices.Clone(s.db.Announced), func(a Announcement) bool { return now.Sub(a.At) >= announcedFor })
	at := func(key string) int {
		return slices.IndexFunc(record, func(a Announcement) bool { return a.Key == key })
	}
	claimed := []string{}
	for _, key := range keys {
		if at(key) < 0 {
			claimed = append(claimed, key)
			record = append(record, Announcement{Key: key, At: now})
		}
	}
	if len(claimed) == 0 {
		return claimed, nil
	}
	if len(record) > maxAnnounced {
		record = record[len(record)-maxAnnounced:]
	}
	s.db.Announced = record
	return claimed, s.save()
}

// validAnnounceKey is a key the board makes: an item's alert's key, not empty
// and short. It is data, kept as JSON.
func validAnnounceKey(key string) bool {
	return key != "" && len(key) <= maxAnnounceKey
}

// announceItems serves POST /api/announce: what the board is about to
// announce, answered with what this request claimed, which only it
// announces.
func (h *HTTP) announceItems(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Keys []string `json:"keys"`
	}
	if err := decodeBody(w, r, &input, 256<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(input.Keys) > maxAnnounceKeys {
		apiError(w, http.StatusBadRequest, "Too many keys to announce at once")
		return
	}
	if slices.ContainsFunc(input.Keys, func(key string) bool { return !validAnnounceKey(key) }) {
		apiError(w, http.StatusBadRequest, "An announce key is empty or too long")
		return
	}
	// While AFK mode is on every key is recorded as announced and none is
	// handed back: nothing alerts the Overlord while he is away, and what the
	// board asked about then is not announced once he is back either. What
	// waits on him is in the report.
	away := h.Service.afkOn()
	claimed, err := h.Service.Store.claimAnnounced(input.Keys, time.Now().UTC())
	switch {
	case away:
		// The board reads a refusal as leave to announce, so a record that
		// could not be saved still hands it nothing while he is away. A key
		// that was not recorded is asked about again, and the store reports
		// its own failed saves.
		claimed = []string{}
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		Claimed []string `json:"claimed"`
	}{claimed})
}
