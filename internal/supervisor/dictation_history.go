package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The board's recent dictations are one list for every window that shows the
// board, the desktop window and a browser tab alike, which keep no storage in
// common: the home keeps it, in its state folder, so it outlasts a restart
// and never leaves this PC. Only the board's own page on this PC reads or
// adds to it, and nothing about its words is ever logged.

const (
	// dictationsFile is the home's record of the recent dictations.
	dictationsFile = "dictations.json"
	// dictationsKept bounds the record, newest first.
	dictationsKept = 10
	// dictationBody bounds what one dictation the page keeps may be.
	dictationBody = 64 << 10
)

// dictated is one dictation as the home records it: its words, and when it
// was kept, in milliseconds since 1970, as the page tells time.
type dictated struct {
	Text string `json:"text"`
	At   int64  `json:"at"`
}

// readDictations reads the recent dictations. A home with none has no record.
func readDictations(stateDir string) ([]dictated, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, dictationsFile))
	if errors.Is(err, os.ErrNotExist) {
		return []dictated{}, nil
	}
	if err != nil {
		return nil, err
	}
	var messages []dictated
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("state/%s cannot be read: %w", dictationsFile, err)
	}
	return messages, nil
}

func respondDictations(w http.ResponseWriter, messages []dictated) {
	respond(w, http.StatusOK, struct {
		Messages []dictated `json:"messages"`
	}{messages})
}

func dictationsOffMachine(w http.ResponseWriter, r *http.Request, board string) bool {
	if offMachine(r, board) == "" {
		return false
	}
	apiError(w, http.StatusForbidden, "The board shows its recent dictations only on the PC it runs on, from its own page at 127.0.0.1.")
	return true
}

func (h *HTTP) recentDictations(w http.ResponseWriter, r *http.Request) {
	if dictationsOffMachine(w, r, h.Host) {
		return
	}
	h.Service.dictations.Lock()
	defer h.Service.dictations.Unlock()
	messages, err := readDictations(h.Service.Store.Home.State)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondDictations(w, messages)
}

// keepDictation puts one dictation first in the list and answers the list. A
// record that cannot be read holds nothing anyone can use, so the new one
// replaces it.
func (h *HTTP) keepDictation(w http.ResponseWriter, r *http.Request) {
	if dictationsOffMachine(w, r, h.Host) {
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, dictationBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Text) == "" {
		apiError(w, http.StatusBadRequest, "A dictation's words are required")
		return
	}
	h.Service.dictations.Lock()
	defer h.Service.dictations.Unlock()
	prior, _ := readDictations(h.Service.Store.Home.State)
	messages := append([]dictated{{Text: strings.TrimSpace(input.Text), At: time.Now().UnixMilli()}}, prior...)
	messages = messages[:min(len(messages), dictationsKept)]
	data, err := json.Marshal(messages)
	if err == nil {
		err = fsx.AtomicWriteFile(filepath.Join(h.Service.Store.Home.State, dictationsFile), data)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The dictation could not be kept.")
		return
	}
	respondDictations(w, messages)
}
