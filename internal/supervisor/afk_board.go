package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

// The board's AFK switch. POST /api/afk turns AFK mode on or off for the
// program that shows the board, once the supervisor has proven the Overlord
// started that program himself: the proof a command gets over the pipe, made
// of the process at the other end of the connection. GET /api/afk/report is
// the report of the last stretch as the board's page reads it.
//
// The proof names its adversary, as the pipe's does: an agent that follows its
// contract and tries the switch from a browser it started, through a proxy or
// from another machine. An agent that drives the Overlord's own running
// browser is his browser to the supervisor, as one that writes state/afk.json
// itself is his user; AGENTS.md forbids both, and nothing here stops either.

// overlordsBoard proves the request came from a board of the Overlord's own
// and says which: the board's own page on this PC, shown by a program he
// started himself. asked is when the request arrived.
func (s *Service) overlordsBoard(r *http.Request, board string, asked time.Time) (string, error) {
	if loopbackProblem(r, board) != "" {
		return "", errors.New("AFK mode is the Supreme Overlord's switch, and this board is not the board's own page on the PC the fleet runs on, or reached it through a proxy" + onlyHis)
	}
	unknown := errors.New("AFK mode is the Supreme Overlord's switch, and the supervisor could not tell which program shows this board, so nothing says it is his" + onlyHis)
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "", unknown
	}
	// The connection's own end at the supervisor, which is the board's address
	// unless the request names another loopback name for it.
	local, err := netip.ParseAddrPort(board)
	if address, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		local, err = netip.ParseAddrPort(address.String())
	}
	if err != nil {
		return "", unknown
	}
	peerOf := s.peerOf
	if peerOf == nil {
		peerOf = tcpPeerProcess
	}
	pid, err := peerOf(peer, local)
	if err != nil {
		return "", unknown
	}
	ancestry, err := s.overlordsOwn(pid, asked, askingBoard)
	if err != nil {
		return "", err
	}
	// The process that holds the connection is a browser's own child; the
	// program he started is the one just under the desktop.
	started := ancestry[max(fromDesktop(ancestry)-1, 0)]
	return fmt.Sprintf("his own board (%s pid %d)", started.ExeBase, started.PID), nil
}

// switchAFKFromBoard serves POST /api/afk: the board's AFK switch. The body is
// read before anything is proven, so a body the endpoint does not take is
// refused as one whoever sent it.
func (h *HTTP) switchAFKFromBoard(w http.ResponseWriter, r *http.Request) {
	asked := time.Now()
	var input struct {
		On *bool `json:"on"`
	}
	if err := decodeBody(w, r, &input, 1<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.On == nil {
		apiError(w, http.StatusBadRequest, "Say whether AFK mode turns on or off")
		return
	}
	from, err := h.Service.overlordsBoard(r, h.Host, asked)
	if err != nil {
		apiError(w, http.StatusForbidden, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), runRequestTimeout)
	defer cancel()
	h.Service.runRequests.Lock()
	err = h.Service.switchAFKAs(ctx, from, "", *input.On)
	h.Service.runRequests.Unlock()
	// Off while it is already off asks for nothing new.
	if err != nil && !errors.Is(err, afk.ErrNotOn) {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.publish(nil)
	state := "off"
	if *input.On {
		state = "on"
	}
	respond(w, http.StatusOK, struct {
		State string `json:"state"`
	}{state})
}

// afkReportPage is the report of a stretch as the board's page reads it: the
// decisions under their headings, how long it lasted and what was spent in
// the words the CFO's text uses, and no list left out. Asked and EndedAsked
// are the Overlord's words for a switch the CFO made at his ask, and empty
// for one he made himself.
type afkReportPage struct {
	Found      bool          `json:"found"`
	Session    string        `json:"session"`
	Since      time.Time     `json:"since"`
	Ended      time.Time     `json:"ended"`
	Lasted     string        `json:"lasted"`
	From       string        `json:"from"`
	Asked      string        `json:"asked"`
	EndedFrom  string        `json:"ended_from"`
	EndedAsked string        `json:"ended_asked"`
	Sections   []afk.Section `json:"sections"`
	Finished   []afk.Finish  `json:"finished"`
	Held       []afk.Held    `json:"held"`
	Spent      []string      `json:"spent"`
	Notes      []string      `json:"notes"`
}

// afkReport serves GET /api/afk/report: the report of the last stretch of AFK
// mode that ended, with each item it held as it stands now, or that none has.
func (h *HTTP) afkReport(w http.ResponseWriter, _ *http.Request) {
	report, found, err := afk.ReadReport(h.Service.Store.Home.State)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		respond(w, http.StatusOK, struct {
			Found bool `json:"found"`
		}{})
		return
	}
	respond(w, http.StatusOK, afkReportPage{
		Found: true, Session: report.Session, Since: report.Since, Ended: report.Ended, Lasted: report.Lasted(), From: report.From, EndedFrom: report.EndedFrom, Asked: report.Asked, EndedAsked: report.EndedAsked,
		Sections: report.Sections(),
		Finished: append([]afk.Finish{}, report.Finished...),
		Held:     heldAsNow(h.Service.Store.Snapshot(), report.Held, report.Ended),
		Spent:    append([]string{}, afk.Spent(report.Before, report.After)...),
		Notes:    append([]string{}, report.Notes...),
	})
}
