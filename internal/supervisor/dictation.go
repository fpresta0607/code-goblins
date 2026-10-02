package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/voice"
)

// Dictation is the speech engine on this machine the board's dictation runs
// on: a model that is fetched once and then recognises a sound with no
// network.
type Dictation interface {
	// Name is the model's name, which the board shows as what is listening.
	Name() string
	// Ready says what is missing, or nil when the engine can recognise.
	Ready() error
	// Fetch downloads what is missing, telling progress as it arrives.
	Fetch(ctx context.Context, progress func(part string, done, total int64)) error
	// Recognize returns the words in a WAV sound.
	Recognize(ctx context.Context, sound []byte) (string, error)
}

// dictationLimit bounds one dictation's sound: about four minutes of the 16
// kHz mono sound the board records.
const dictationLimit = 8 << 20

// dictationPatience is how long the engine's download may take before it is
// given up, so one that stalls does not keep every later dictation waiting
// on it.
const dictationPatience = 30 * time.Minute

// dictationFetch is the one download the first dictation starts, and what
// the board is told about it.
type dictationFetch struct {
	mu      sync.Mutex
	running bool
	done    int64
	total   int64
	// problem is why the last download failed, until the next one starts.
	problem string
}

// note says how far the download is.
func (f *dictationFetch) note() string {
	if f.total <= 0 {
		return "The speech model is being downloaded, once. Dictate again when it is there."
	}
	return fmt.Sprintf("The speech model is being downloaded, once: %d of %d MB. Dictate again when it is there.", f.done>>20, f.total>>20)
}

// fetchDictation starts the engine's download unless one runs, and returns
// what to tell the Overlord meanwhile: why the last one failed, when it did,
// else how far this one is.
func (h *HTTP) fetchDictation(speech Dictation) string {
	fetch := &h.dictation
	fetch.mu.Lock()
	defer fetch.mu.Unlock()
	if fetch.running {
		return fetch.note()
	}
	note := fetch.problem
	fetch.running, fetch.done, fetch.total, fetch.problem = true, 0, 0, ""
	if note == "" {
		note = fetch.note()
	}
	h.dictationWork.Add(1)
	go func() {
		defer h.dictationWork.Done()
		// The download outlives the request that started it.
		ctx, cancel := context.WithTimeout(context.Background(), h.dictationPatience)
		defer cancel()
		err := speech.Fetch(ctx, func(_ string, done, total int64) {
			fetch.mu.Lock()
			fetch.done, fetch.total = done, total
			fetch.mu.Unlock()
		})
		fetch.mu.Lock()
		defer fetch.mu.Unlock()
		fetch.running = false
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			fetch.problem = "The speech model's download did not finish in time and was given up. Dictate again to start it over."
		case err != nil:
			fetch.problem = sentence(err.Error())
		}
	}()
	return note
}

// sentence is text as one sentence the board can show: capitalized and ended.
func sentence(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// dictationStatus tells the board which engine listens and whether it is
// there: none, missing, fetching or ready.
func (h *HTTP) dictationStatus(w http.ResponseWriter) {
	status := struct {
		Engine string `json:"engine"`
		State  string `json:"state"`
		Note   string `json:"note,omitempty"`
	}{State: "none"}
	if speech := h.Service.Options.Dictation; speech != nil {
		status.Engine = speech.Name()
		h.dictation.mu.Lock()
		switch {
		case h.dictation.running:
			status.State, status.Note = "fetching", h.dictation.note()
		case speech.Ready() != nil:
			status.State, status.Note = "missing", h.dictation.problem
		default:
			status.State = "ready"
		}
		h.dictation.mu.Unlock()
	}
	respond(w, 200, status)
}

// dictate returns the words in the sound the board's page recorded. It takes
// a sound only from the board's own page on this machine, so what is said
// never leaves it, and runs one engine at a time. A body that is not a WAV
// sound is a bad request before anything of it is read, as every board
// endpoint refuses a body it does not take.
func (h *HTTP) dictate(w http.ResponseWriter, r *http.Request) {
	if offMachine(r, h.Host) != "" {
		apiError(w, http.StatusForbidden, "The board dictates only on the PC it runs on, from its own page at 127.0.0.1, so what you say never leaves that PC.")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "audio/wav") {
		apiError(w, http.StatusBadRequest, "A WAV sound is required")
		return
	}
	speech := h.Service.Options.Dictation
	if speech == nil {
		apiError(w, http.StatusServiceUnavailable, "Dictation is not set up in this supervisor; cfo doctor says why.")
		return
	}
	sound, err := io.ReadAll(http.MaxBytesReader(w, r.Body, dictationLimit))
	if err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "That dictation is too long to recognise at once; say it in shorter parts.")
		return
	}
	if speech.Ready() != nil {
		apiError(w, http.StatusServiceUnavailable, h.fetchDictation(speech))
		return
	}
	select {
	case h.dictationSlot <- struct{}{}:
		defer func() { <-h.dictationSlot }()
	case <-r.Context().Done():
		return
	}
	text, err := speech.Recognize(r.Context(), sound)
	var room voice.NoRoom
	switch {
	case errors.As(err, &room):
		apiError(w, http.StatusServiceUnavailable, sentence(room.Error()))
	case err != nil:
		apiError(w, http.StatusInternalServerError, sentence(err.Error()))
	default:
		respond(w, 200, struct {
			Text   string `json:"text"`
			Engine string `json:"engine"`
		}{text, speech.Name()})
	}
}
