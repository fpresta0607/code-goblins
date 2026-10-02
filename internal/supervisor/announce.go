package supervisor

import (
	"net/http"
	"strings"
	"time"
	"unicode"
)

// The board announces each Command Center item once: it shows the item's
// alert, sends its Windows notification and opens the Command Center on it
// only for a key it claimed here first. The record is the supervisor's, so a
// reload, a second tab, a reconnect or a supervisor restart never announces
// the same item again (the Overlord, 2026-10-01: "no double fire or display").
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

// claimAnnounced records the keys nobody announced yet and returns them, in
// the order given and each once; a key already announced is not returned.
// It saves only when it records something, and a save that fails records
// nothing.
func (s *Store) claimAnnounced(keys []string, now time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := make([]Announcement, 0, len(s.db.Announced)+len(keys))
	known := make(map[string]bool, len(s.db.Announced)+len(keys))
	for _, a := range s.db.Announced {
		if now.Sub(a.At) < announcedFor {
			record = append(record, a)
			known[a.Key] = true
		}
	}
	claimed := []string{}
	for _, key := range keys {
		if !known[key] {
			known[key] = true
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

// validAnnounceKey is a key the board makes: an item's or alert's key, short
// and printable.
func validAnnounceKey(key string) bool {
	return key != "" && len(key) <= maxAnnounceKey && strings.IndexFunc(key, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}

// announceItems serves POST /api/announce: the keys the board is about to
// announce, answered with those this request claimed, which only it
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
	for _, key := range input.Keys {
		if !validAnnounceKey(key) {
			apiError(w, http.StatusBadRequest, "An announce key is empty, too long or not printable")
			return
		}
	}
	claimed, err := h.Service.Store.claimAnnounced(input.Keys, time.Now().UTC())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		Claimed []string `json:"claimed"`
	}{claimed})
}
