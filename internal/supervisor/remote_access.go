package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

// remoteFixTitles names the rows of the setting whose command a fix runs.
var remoteFixTitles = map[string]string{"tailscale": "Mend Tailscale for remote access", "scrawl": "Mend the route to Scrawl reviews"}

func (h *HTTP) remoteAccess(w http.ResponseWriter, r *http.Request) {
	manager := h.Service.Options.RemoteAccess
	if manager == nil {
		apiError(w, http.StatusConflict, "Remote access is unavailable on this board")
		return
	}
	// The setting offers the Tailscale switch by what this PC's Tailscale is,
	// so every answer it reads while access is off says that (Detect).
	if r.Method == "GET" {
		respond(w, http.StatusOK, manager.Detect(r.Context(), manager.Status(r.Context())))
		return
	}
	// A fix names its row and the command the board showed under it, which
	// it runs only while that is still the row's command.
	var input struct {
		On      *bool   `json:"on"`
		Scrawl  *bool   `json:"scrawl"`
		Fix     *string `json:"fix"`
		Command *string `json:"command"`
	}
	err := decodeBody(w, r, &input, 4096)
	switch {
	case err == nil && input.Fix != nil && input.Command != nil && input.On == nil && input.Scrawl == nil:
		h.remoteFix(w, r, manager, *input.Fix, *input.Command)
		return
	case err != nil || input.Fix != nil || input.Command != nil || (input.On == nil) == (input.Scrawl == nil):
		apiError(w, http.StatusBadRequest, "Say whether remote access or Scrawl reviews should be on, or which row's command to run")
		return
	}
	if input.Scrawl != nil && *input.Scrawl || input.On != nil && *input.On {
		if offMachine(r, h.Host) != "" {
			apiError(w, http.StatusForbidden, "Turn it on from the board on this PC")
			return
		}
		var status remoteaccess.Status
		var err error
		if input.Scrawl != nil {
			status, err = manager.EnableScrawl(r.Context())
		} else {
			status, err = manager.Enable(r.Context())
		}
		if err != nil {
			apiError(w, http.StatusConflict, err.Error())
			return
		}
		h.Service.notify()
		respond(w, http.StatusOK, status)
		return
	}
	// Revocation cancels this remote request too. Finish removing the owned
	// proxy after that cancellation, with a separate strict deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
	defer cancel()
	if input.Scrawl != nil {
		status, err := manager.DisableScrawl(ctx)
		if err != nil {
			apiError(w, http.StatusConflict, err.Error())
			return
		}
		h.Service.notify()
		respond(w, http.StatusOK, status)
		return
	}
	if err := manager.Disable(ctx); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, manager.Detect(ctx, manager.Status(ctx)))
}

func (h *HTTP) remoteScrawlLink(w http.ResponseWriter, r *http.Request) {
	manager := h.Service.Options.RemoteAccess
	if manager == nil {
		apiError(w, http.StatusConflict, "Remote access is unavailable")
		return
	}
	link, err := manager.ScrawlLink(r.Context(), r.URL.Query().Get("url"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, link, http.StatusTemporaryRedirect)
}

// remoteFix runs the command that mends a row of the setting, tailscale or
// scrawl, in a terminal of its own that the setting draws, as a run card
// runs its command. It is the command the row names now, never anything the
// browser sends: the command the board showed only has to match it. Only
// the board on this PC runs one, and the answer names the run.
func (h *HTTP) remoteFix(w http.ResponseWriter, r *http.Request, manager *remoteaccess.Manager, row, shown string) {
	title, isKnown := remoteFixTitles[row]
	switch {
	case offMachine(r, h.Host) != "":
		apiError(w, http.StatusForbidden, "Run it from the board on this PC")
		return
	case !isKnown:
		apiError(w, http.StatusBadRequest, "Name the row to mend: tailscale or scrawl")
		return
	case h.Service.Options.Runs == nil:
		apiError(w, http.StatusServiceUnavailable, "This board cannot open a terminal. Copy the command instead.")
		return
	}
	status := manager.Detect(r.Context(), manager.Status(r.Context()))
	command := status.Fix
	if row == "scrawl" {
		command = status.ScrawlFix
	}
	switch {
	case command == "":
		apiError(w, http.StatusConflict, "Nothing there needs mending now")
		return
	case shown != command:
		apiError(w, http.StatusConflict, "The command changed; read it again")
		return
	}
	run, err := h.Service.remoteFixRun(row, title, command)
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		remoteaccess.Status
		RunID string `json:"run_id"`
	}{status, run.ID})
}

// remoteFixRun records a fix as an item the board made and starts it at
// once, his press being its Run, whose claim makes it running; one left
// ready, its start never queued, holds nothing up and expires. A terminal the
// supervisor opens has the supervisor's environment, which predates a
// Tailscale installed since, so the script reads PATH as a new sign-in would
// before the command.
func (s *Service) remoteFixRun(row, title, command string) (Run, error) {
	s.runRequests.Lock()
	defer s.runRequests.Unlock()
	if slices.ContainsFunc(s.Store.Snapshot().Runs, func(r Run) bool { return r.RemoteFix != "" && r.State == "running" }) {
		return Run{}, errors.New("A command from Remote access still runs; wait for it to end")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Run{}, err
	}
	id := "remote-fix-" + row + "-" + hex.EncodeToString(nonce[:])
	now := time.Now().UTC()
	identity := sha256.Sum256([]byte("remote-fix\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	script := "$ErrorActionPreference = 'Stop'\n$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')\n" + command + "\nexit $LASTEXITCODE\n"
	run, err := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: script, Cwd: s.Store.Home.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), RemoteFix: row})
	if err != nil {
		return Run{}, err
	}
	_, err = s.Store.Queue(Action{ID: "run-" + run.ID, Kind: "run", RunID: run.ID, Generation: run.Identity})
	return run, err
}
