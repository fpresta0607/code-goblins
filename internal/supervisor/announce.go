package supervisor

import (
	"net/http"
	"slices"
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
	// sameEventFor is how long the same news from a goblin is one event.
	sameEventFor = 5 * time.Minute
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

// claimAnnounced records what nobody announced yet and returns it, keys then
// news, each once. A key is an item's and is announced once. News is a
// goblin's, which has no id of its own, so its key is what it says: the same
// words are one event for sameEventFor and a new event after it, such as a
// gate asking for a second decision. It saves only when it records something,
// and a save that fails records nothing.
func (s *Store) claimAnnounced(keys, news []string, now time.Time) ([]string, error) {
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
	for _, key := range news {
		switch i := at(key); {
		case i < 0:
			claimed = append(claimed, key)
			record = append(record, Announcement{Key: key, At: now})
		case now.Sub(record[i].At) >= sameEventFor:
			claimed = append(claimed, key)
			record = append(slices.Delete(record, i, i+1), Announcement{Key: key, At: now})
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

// announceItems serves POST /api/announce: what the board is about to
// announce, answered with what this request claimed, which only it
// announces.
func (h *HTTP) announceItems(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Keys []string `json:"keys"`
		News []string `json:"news"`
	}
	if err := decodeBody(w, r, &input, 256<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(input.Keys)+len(input.News) > maxAnnounceKeys {
		apiError(w, http.StatusBadRequest, "Too many keys to announce at once")
		return
	}
	if slices.ContainsFunc(input.Keys, func(key string) bool { return !validAnnounceKey(key) }) || slices.ContainsFunc(input.News, func(key string) bool { return !validAnnounceKey(key) }) {
		apiError(w, http.StatusBadRequest, "An announce key is empty, too long or not printable")
		return
	}
	claimed, err := h.Service.Store.claimAnnounced(input.Keys, input.News, time.Now().UTC())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		Claimed []string `json:"claimed"`
	}{claimed})
}
