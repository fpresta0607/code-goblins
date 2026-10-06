package supervisor

import "net/http"

// StartAtLogin is whether Windows starts this home at login, which the board
// shows in the CFO's panel and turns on or off; without it the board shows no
// such setting. It is the same setting as the desktop window's tray item and
// the setup's box.
type StartAtLogin struct {
	// Read says whether Windows starts this home at login.
	Read func() (StartAtLoginView, error)
	// Set turns it on or off and keeps the choice in the home, so an install
	// or update keeps it.
	Set func(on bool) error
}

// StartAtLoginView is Start at login as the board shows it: on or off, or
// why this home cannot be started at login.
type StartAtLoginView struct {
	On          bool   `json:"on"`
	Unavailable string `json:"unavailable,omitempty"`
}

// startAtLoginView reads Start at login for the snapshot, or nil on a board
// without it; one that cannot be read says why among the board's issues.
func (s *Service) startAtLoginView() (*StartAtLoginView, string) {
	if s.Options.StartAtLogin == nil {
		return nil, ""
	}
	view, err := s.Options.StartAtLogin.Read()
	if err != nil {
		return nil, "Start at login cannot be read: " + err.Error()
	}
	return &view, ""
}

// setStartAtLogin serves POST /api/start-at-login, the board's switch.
func (h *HTTP) setStartAtLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		On *bool `json:"on"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.On == nil {
		apiError(w, http.StatusBadRequest, "Say whether Start at login is on")
		return
	}
	setting := h.Service.Options.StartAtLogin
	if setting == nil {
		apiError(w, http.StatusConflict, "This board cannot change Start at login")
		return
	}
	if err := setting.Set(*input.On); err != nil {
		apiError(w, http.StatusInternalServerError, "Start at login was not changed: "+err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, struct {
		On bool `json:"on"`
	}{*input.On})
}
