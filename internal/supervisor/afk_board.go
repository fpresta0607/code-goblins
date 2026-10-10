package supervisor

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The board's AFK switch. POST /api/afk turns AFK mode on or off for the
// program that shows the board, once the supervisor has proven that program
// the Overlord's own, of the process at the other end of the connection.
// GET /api/afk/report is the report of the last stretch as the board's page
// reads it.
//
// A board is his when nothing marks its program an agent's or one another
// program can drive, it runs as the supervisor's own Windows user in the
// supervisor's own session, and one of three things proves it his:
//
//   - its parents reach the desktop, so he started it there;
//   - it is this home's own desktop window, by the program Windows says it
//     runs, whoever opened it;
//   - its browser holds a grant the supervisor gave that browser program
//     while its parents reached the desktop (board_grant.go).
//
// The first stood alone until 2026-10-09, when the Overlord pressed the
// switch in the Code Goblins window itself and was refused. A program
// outlives what opened it, and a window's opener rarely stays: the supervisor
// opens the window through a shell that exits when it moves the window onto
// an update, and the install, a click on a notification and goblins --window
// each leave it with no parent at the desktop either. A browser loses its
// opener the same way, and restarts itself after its own updates. The second
// and third need no parent, so his window and a browser he started keep his
// switches for as long as they run and after.
//
// The proof names its adversary, as the pipe's does: an agent that follows its
// contract and tries the switch from a browser it started, through a proxy or
// from another machine. Its browser carries its environment or its harness
// above it, and is started so that a program can drive it, and either refuses
// it whatever it holds. An agent that drives the Overlord's own running
// browser or window through his desktop is his to the supervisor, as one that
// writes state/afk.json itself is his user; AGENTS.md forbids both, and
// nothing here stops either.

// hisBoard is a board proven the Overlord's own: the program he started that
// shows it, the program the process that holds the connection runs, which is
// the browser's own, and whether its parents reach the desktop.
type hisBoard struct {
	started     proc.Entry
	image       string
	fromDesktop bool
}

// boardRefusal is the refusal of a board: found is what the supervisor found,
// in its own words, for the CFO, and say the one short sentence the board
// shows the Overlord.
type boardRefusal struct{ found, say string }

func (refused *boardRefusal) Error() string { return refused.found }

// drivingSwitches are the switches a browser is started with so that another
// program drives it, or so that it shows no window, without their dashes:
// Chromium's, which WebView2 takes too, and Firefox's.
var drivingSwitches = []string{"remote-debugging-port", "remote-debugging-pipe", "enable-automation", "headless", "marionette"}

// drivenBy names the switch among a program's arguments that lets another
// program drive it, and is empty when none does.
func drivenBy(arguments []string) string {
	for _, argument := range arguments[min(1, len(arguments)):] {
		name, _, _ := strings.Cut(strings.TrimLeft(argument, "-"), "=")
		if strings.HasPrefix(argument, "-") && slices.Contains(drivingSwitches, strings.ToLower(name)) {
			return "--" + strings.ToLower(name)
		}
	}
	return ""
}

// homesWindow reports whether the process started, which runs image, is this
// home's own desktop window: the window's program, by the name Windows keeps
// for a process from its start, run from the home's bin, where an update
// leaves an open window on a renamed copy of the program it started as.
// Whoever can put a program there can replace the supervisor's own. Windows
// may name one folder two ways, by its long name and by its short one, so
// two names that differ are the same folder when the file system says so.
func homesWindow(started proc.Entry, image, bin string) bool {
	if !strings.EqualFold(started.ExeBase, windowName) {
		return false
	}
	folder := filepath.Dir(filepath.Clean(image))
	if strings.EqualFold(folder, filepath.Clean(bin)) {
		return true
	}
	runs, err := os.Stat(folder)
	if err != nil {
		return false
	}
	homes, err := os.Stat(bin)
	return err == nil && os.SameFile(runs, homes)
}

// otherOwner says how the process started is not the supervisor's own
// Windows user's in the supervisor's own session, and is empty when it is.
// The board answers every session of this PC at one address, so only this
// tells the Overlord's desktop from another user's or another sign-in's.
func otherOwner(machine windowSystem, started proc.Entry) (string, error) {
	own, ok := proc.StartTime(os.Getpid())
	if !ok {
		return "", errors.New("the supervisor could not read its own start time")
	}
	mine, err := machine.owner(os.Getpid(), own)
	if err != nil {
		return "", err
	}
	theirs, err := machine.owner(started.PID, started.Start)
	switch {
	case err != nil:
		return "", err
	case theirs.User != mine.User:
		return "as another Windows user", nil
	case theirs.Session != mine.Session:
		return fmt.Sprintf("in Windows session %d, and the supervisor in session %d", theirs.Session, mine.Session), nil
	}
	return "", nil
}

// overlordsBoard proves the request came from a board of the Overlord's own
// and says which: the board's own page on this PC, shown by a program that is
// his. asked is when the request arrived, and who is what it asks for, as a
// refusal words it.
func (s *Service) overlordsBoard(r *http.Request, board string, asked time.Time, who asker) (string, error) {
	started, err := s.overlordsProgram(r, board, asked, who)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("his own board (%s pid %d)", started.ExeBase, started.PID), nil
}

// overlordsProgram is overlordsBoard's proof, returning the program of his
// that shows the board.
func (s *Service) overlordsProgram(r *http.Request, board string, asked time.Time, who asker) (proc.Entry, error) {
	proven, err := s.proveBoard(r, board, asked, who)
	return proven.started, err
}

// proveBoard is the proof of a board. Every refusal is a boardRefusal.
func (s *Service) proveBoard(r *http.Request, board string, asked time.Time, who asker) (hisBoard, error) {
	refused := func(found error, inWindow bool) (hisBoard, error) {
		say := who.say
		if inWindow {
			say = who.sayInWindow
		}
		return hisBoard{}, &boardRefusal{found: found.Error(), say: say}
	}
	if loopbackProblem(r, board) != "" {
		return refused(who.refuse("this board is not the board's own page on the PC the fleet runs on, or reached it through a proxy"), false)
	}
	unknown := who.refuse("the supervisor could not tell which program shows this board, so nothing says it is his")
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return refused(unknown, false)
	}
	// The connection's own end at the supervisor, which is the board's address
	// unless the request names another loopback name for it.
	local, err := netip.ParseAddrPort(board)
	if address, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		local, err = netip.ParseAddrPort(address.String())
	}
	if err != nil {
		return refused(unknown, false)
	}
	peerOf := s.peerOf
	if peerOf == nil {
		peerOf = tcpPeerProcess
	}
	pid, err := peerOf(peer, local)
	if err != nil {
		return refused(unknown, false)
	}
	ancestry, _, err := s.unmarked(pid, asked, who)
	inWindow := slices.ContainsFunc(ancestry, func(entry proc.Entry) bool { return strings.EqualFold(entry.ExeBase, windowName) })
	if err != nil {
		return refused(err, inWindow)
	}
	// The process that holds the connection is a browser's own child. The
	// program that is his is the one just under the desktop, and where the
	// parents stop short of it the last one still running, since a program
	// outlives what opened it.
	desktop := fromDesktop(ancestry)
	at := len(ancestry) - 1
	if desktop >= 0 {
		at = max(desktop-1, 0)
	}
	started := ancestry[at]
	machine := s.windows.orWindows()
	// What shows the board is that program and what it started, down to the
	// process that holds the connection. A browser one of them was started
	// to drive is a program's, whoever started it.
	shows := make([]proc.Identity, at+1)
	for i, entry := range ancestry[:at+1] {
		if shows[i], err = machine.identify(entry.PID, entry.Start); err != nil {
			return refused(errors.New(who.unread), inWindow)
		}
		if with := drivenBy(shows[i].Arguments); with != "" {
			return refused(who.refuse(fmt.Sprintf("another program can drive the browser that shows this board (%s pid %d was started with %s)", entry.ExeBase, entry.PID, with)), inWindow)
		}
	}
	// The environment read so far is the connection's process's, which a
	// browser may have started with one of its own making.
	if where := agentMark(s.Store.Home.State, ancestry[at:], shows[at].Environment); where != "" {
		return refused(who.refuse(who.runs+" "+where), inWindow)
	}
	other, err := otherOwner(machine, started)
	if err != nil {
		return refused(errors.New(who.unread), inWindow)
	}
	if other != "" {
		return refused(who.refuse(who.runs+" "+other), inWindow)
	}
	proven := hisBoard{started: started, image: shows[0].Image, fromDesktop: desktop >= 0}
	if proven.fromDesktop || homesWindow(started, shows[at].Image, s.Store.Home.Bin()) {
		return proven, nil
	}
	if _, granted, err := s.grantOf(r, proven.image, asked); err != nil {
		return refused(who.refuse("the grants this home gave its boards could not be read ("+err.Error()+"), so nothing proves the program that shows this board is his"), inWindow)
	} else if !granted {
		return refused(errors.New(who.unproven), inWindow)
	}
	return proven, nil
}

// refuse answers a board whose request the supervisor refused or could not
// carry out: the board gets say, the one short sentence the Overlord reads,
// and the CFO is told what happened in full through its wake queue, under
// key. No dialog shows him the supervisor's own words for it.
func (h *HTTP) refuse(w http.ResponseWriter, status int, key, what, say string, err error) {
	var refused *boardRefusal
	if errors.As(err, &refused) {
		say = refused.say
	}
	state := h.Service.Store.Home.State
	_, told := wake.Append(state, "review", key, what+": "+err.Error()+". The board told him: "+say)
	if told == nil {
		_, told = wake.PublishEpisode(state)
	}
	if told != nil {
		h.Service.publish(fmt.Errorf("the CFO's wake queue could not be told what a board was refused: %w", told))
	}
	apiError(w, status, say)
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
	from, err := h.Service.overlordsBoard(r, h.Host, asked, switchingBoard(*input.On))
	if err != nil {
		h.refuse(w, http.StatusForbidden, "afk", "A press of AFK mode's switch on a board was refused", "", err)
		return
	}
	h.Service.runRequests.Lock()
	err = h.Service.switchAFKAs(from, "", *input.On)
	h.Service.runRequests.Unlock()
	// Off while it is already off asks for nothing new.
	if err != nil && !errors.Is(err, afk.ErrNotOn) {
		say := "AFK did not switch, and the CFO has been told why."
		// His off resets a switch that cannot be read, and his on cannot.
		if _, unreadable := afk.Read(h.Service.Store.Home.State); unreadable != nil {
			say = "Turn AFK off to reset its switch, which cannot be read."
		}
		h.refuse(w, http.StatusConflict, "afk", "The Overlord's press of AFK mode's switch on his board failed", say, err)
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
// decisions under their headings, how long it lasted, each allowance used
// with its percent at either end for the board to draw, and no list left out.
// Asked and EndedAsked are the Overlord's words for a switch the CFO made at
// his ask, and empty for one he made himself.
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
	Spent      []afk.Used    `json:"spent"`
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
		Spent:    append([]afk.Used{}, afk.Spent(report.Before, report.After)...),
		Notes:    append([]string{}, report.Notes...),
	})
}
