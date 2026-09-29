package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/onboarding"
)

type CFOResume struct {
	Harness  string    `json:"harness"`
	Session  string    `json:"session"`
	Terminal string    `json:"terminal"`
	Identity string    `json:"identity"`
	Process  lock.Info `json:"-"`
}

type CFORecovery struct {
	State   string
	Memory  func() (uint64, uint64, error)
	Restart func(context.Context, CFOResume) error
	mu      sync.Mutex
}

// Registration stays stable while a process changes conversations, so pending
// questions keep their recipient. Recovery separately follows its latest session.
func saveCFOConversation(stateDir string, primary primaryRegistration, session string) error {
	if session == "" {
		return nil
	}
	primary.Process.Session = session
	data, err := json.Marshal(primary)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, "cfo-conversation.json"), data)
}

func (r *CFORecovery) Preview() (CFOResume, error) {
	file, err := os.Open(filepath.Join(r.State, "primary.json"))
	if err != nil {
		return CFOResume{}, errors.New("no recorded CFO conversation; run goblins to start the CFO")
	}
	defer file.Close()
	primary, identity, err := decodePrimary(file)
	if err != nil {
		return CFOResume{}, err
	}
	if primary.Host == "" {
		return CFOResume{}, errors.New("the recorded CFO used Herdr; resume that conversation in Herdr before moving it to a native terminal")
	}
	conversationFile, err := os.Open(filepath.Join(r.State, "cfo-conversation.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CFOResume{}, err
	}
	if err == nil {
		defer conversationFile.Close()
		conversation, conversationIdentity, err := decodePrimary(conversationFile)
		if err != nil {
			return CFOResume{}, err
		}
		if conversation.Host == primary.Host && conversation.Agent == primary.Agent && conversation.Process.PID == primary.Process.PID && conversation.Process.Start.Equal(primary.Process.Start) && conversation.Process.Hostname == primary.Process.Hostname {
			primary.Process.Session = conversation.Process.Session
			digest := sha256.Sum256([]byte(identity + conversationIdentity))
			identity = hex.EncodeToString(digest[:])
		}
	}
	if _, err := onboarding.ResumeArgs(primary.Agent, primary.Process.Session); err != nil {
		return CFOResume{}, err
	}
	return CFOResume{Harness: primary.Agent, Session: primary.Process.Session, Terminal: primary.Host, Identity: identity, Process: primary.Process}, nil
}

func (r *CFORecovery) Run(ctx context.Context, identity string) error {
	if !r.mu.TryLock() {
		return errors.New("a CFO restart is already in progress")
	}
	defer r.mu.Unlock()
	record, err := r.Preview()
	if err != nil {
		return err
	}
	if identity == "" || identity != record.Identity {
		return errors.New("the CFO changed; review the current conversation before restarting")
	}
	available, _, err := r.Memory()
	if err != nil {
		return fmt.Errorf("free memory could not be checked: %w", err)
	}
	if available < 4<<30 {
		return errors.New("waiting for memory: at least 4 GB must be available to restart the CFO")
	}
	return r.Restart(ctx, record)
}

func (h *HTTP) cfoResume(w http.ResponseWriter, r *http.Request) {
	recovery := h.Service.Options.CFORecovery
	if recovery == nil {
		apiError(w, http.StatusConflict, "CFO recovery is unavailable on this board")
		return
	}
	record, err := recovery.Preview()
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, record)
}

func (h *HTTP) restartCFO(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Identity string `json:"identity"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	recovery := h.Service.Options.CFORecovery
	if recovery == nil {
		apiError(w, http.StatusConflict, "CFO recovery is unavailable on this board")
		return
	}
	// A closed browser must not cancel the launch after the old process stops.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := recovery.Run(ctx, input.Identity); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		Restarted bool `json:"restarted"`
	}{true})
}
