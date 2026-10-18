package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
)

func runRemote(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		args = []string{"status"}
	}
	if len(args) != 1 || args[0] != "on" && args[0] != "off" && args[0] != "status" {
		fmt.Fprintln(stderr, "usage: cfo remote on|status|off")
		return 2
	}
	// Remote access is off until the Overlord turns it on: turning it on or
	// off is his choice, which the CFO makes on his word, never a goblin or a
	// gate agent.
	if args[0] != "status" && (os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "") {
		fmt.Fprintln(stderr, "cfo remote: turning remote access on or off is the Overlord's choice, which the CFO makes on his word, never a goblin or a gate agent")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	target, boardRunning := remoteTarget(ctx, h)
	login := boardStartAtLogin(h)
	m := boardRemoteAccess(h, target)
	if args[0] == "off" {
		if err := m.Disable(ctx); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Remote board access is off. Existing remote sessions close within one second.")
		return 0
	}
	var status remoteaccess.Status
	if args[0] == "on" {
		if !boardRunning {
			fmt.Fprintln(stderr, "Start this updated board with goblins --board, then run cfo remote on.")
			return 1
		}
		// A candidate command must not call an old board remotely accessible
		// just because that old board's health endpoint answers.
		probe, err := http.NewRequestWithContext(ctx, "GET", target+"/api/remote", nil)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		response, err := (&http.Client{Timeout: 35 * time.Second}).Do(probe)
		if err != nil {
			fmt.Fprintln(stderr, "The board could not confirm remote access support. Update and restart Code Goblins first.")
			return 1
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			fmt.Fprintln(stderr, "Update and restart Code Goblins before enabling remote access.")
			return 1
		}
		status, err = m.Enable(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		status = m.Status(ctx)
	}
	if !status.Enabled {
		fmt.Fprintln(stdout, "Remote board access is off. Run cfo remote on on this PC to enable it.")
	} else {
		fmt.Fprintf(stdout, "Private board: %s\nAllowed Tailscale account: %s\n", status.URL, status.Login)
		if !boardRunning {
			fmt.Fprintln(stdout, "The board is stopped. Start Code Goblins on this PC.")
		}
		fmt.Fprintln(stdout, "On your phone, connect Tailscale with that account, open the link in its browser, and open the CFO terminal. Keep this PC awake and online.")
		if status.ScrawlReady {
			fmt.Fprintln(stdout, "Private Scrawl reviews are ready.")
		} else if status.ScrawlIssue != "" {
			fmt.Fprintln(stdout, "Scrawl:", status.ScrawlIssue)
		}
		if on, unavailable, err := login.StartsAtLogin(); err == nil && on {
			fmt.Fprintln(stdout, "Start at login is on. The board returns after Windows sign-in.")
		} else if unavailable != "" {
			fmt.Fprintln(stdout, unavailable)
		} else {
			fmt.Fprintln(stdout, "Start at login is off or unavailable. Enable it on this PC for automatic recovery after Windows sign-in.")
		}
	}
	if status.Issue != "" {
		fmt.Fprintln(stderr, status.Issue)
		return 1
	}
	return 0
}

// remoteTarget is the address of the board this home runs and whether it
// answers; for a stopped board it is the address the board starts on.
func remoteTarget(ctx context.Context, h home.Home) (string, bool) {
	if record, err := readBoardRecord(h.State); err == nil && boardAlive(ctx, record) == nil {
		return record.URL, true
	}
	return "http://" + boardAddress(), false
}

// remoteAccessLine is cfo doctor's account of remote access: off, on and
// ready, or on and what it needs, and a Tailscale route an earlier change
// may have left. It never names the tailnet or the owner's account, since
// agents quote doctor's lines; cfo remote status shows them.
func remoteAccessLine(ctx context.Context, m *remoteaccess.Manager) string {
	s := m.Status(ctx)
	switch {
	case !s.Enabled && s.Issue != "":
		return "settings unreadable: " + s.Issue
	case !s.Enabled && s.URL != "":
		return "off, but its Tailscale route may remain; run cfo remote off on this PC to remove it"
	case !s.Enabled:
		return "off; to use the board from a phone, connect Tailscale on this PC and the phone, then press Enable in the CFO's Task panel under Remote access, or run cfo remote on (see docs/remote-access.md)"
	case !s.Ready:
		return "on, but not ready: " + s.Issue
	case s.ScrawlReady:
		return "on and ready; cfo remote status shows its link; Scrawl reviews are ready"
	}
	return "on and ready; cfo remote status shows its link; Scrawl: " + s.ScrawlIssue
}

func boardRemoteAccess(h home.Home, target string) *remoteaccess.Manager {
	login := boardStartAtLogin(h)
	return &remoteaccess.Manager{State: h.State, Target: target, StartAtLogin: func() error { return login.SetStartsAtLogin(true) }, Acquire: func() (func(), error) {
		if _, err := lock.AcquireExclusiveNamed(h.State, "remote-access.lock"); err != nil {
			return nil, err
		}
		return func() { _ = lock.ReleaseExclusiveNamed(h.State, "remote-access.lock") }, nil
	}}
}
