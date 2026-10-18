package supervisor

import (
	"context"
	"net/http"
	"time"

	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

func (h *HTTP) remoteAccess(w http.ResponseWriter, r *http.Request) {
	manager := h.Service.Options.RemoteAccess
	if manager == nil {
		apiError(w, http.StatusConflict, "Remote access is unavailable on this board")
		return
	}
	if r.Method == "GET" {
		respond(w, http.StatusOK, manager.Status(r.Context()))
		return
	}
	var input struct {
		On     *bool `json:"on"`
		Scrawl bool  `json:"scrawl"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil || input.On == nil && !input.Scrawl || input.On != nil && input.Scrawl {
		apiError(w, http.StatusBadRequest, "Say whether remote access should be on")
		return
	}
	if input.Scrawl || *input.On {
		if offMachine(r, h.Host) != "" {
			apiError(w, http.StatusForbidden, "Enable remote access from the board on this PC")
			return
		}
		var status remoteaccess.Status
		var err error
		if input.Scrawl {
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
	if err := manager.Disable(ctx); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, manager.Status(ctx))
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
