// Package remoteaccess shares the loopback board through owner-only Tailscale
// Serve. It never opens a network listener or enables Funnel.
package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const Port = 4311
const ScrawlPort = 4312
const configFile = "remote-access.json"

// Config keeps the exact endpoint this home owns, including while disabled so
// a failed Serve cleanup can be retried without touching anyone else's route.
type Config struct {
	Enabled      bool   `json:"enabled"`
	Origin       string `json:"origin"`
	Login        string `json:"login"`
	Target       string `json:"target"`
	Scrawl       bool   `json:"scrawl,omitempty"`
	ScrawlTarget string `json:"scrawl_target,omitempty"`
	ScrawlState  string `json:"scrawl_state,omitempty"`
}

func (c Config) validate() error {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() != strconv.Itoa(Port) || !tailName(u.Hostname()) || c.Origin != "https://"+u.Host {
		return errors.New("remote access needs this PC's exact Tailscale HTTPS name on port 4311")
	}
	if strings.TrimSpace(c.Login) == "" || len(c.Login) > 320 || strings.ContainsAny(c.Login, "\r\n") {
		return errors.New("remote access has no valid Tailscale owner")
	}
	if c.ScrawlTarget != "" {
		if err := validateTarget(c.ScrawlTarget); err != nil {
			return err
		}
		if !filepath.IsAbs(c.ScrawlState) {
			return errors.New("Scrawl needs an absolute state directory")
		}
	} else if c.Scrawl || c.ScrawlState != "" {
		return errors.New("Scrawl settings are incomplete")
	}
	return validateTarget(c.Target)
}

func tailName(name string) bool {
	if !strings.HasSuffix(name, ".ts.net") || len(name) > 253 || len(strings.Split(name, ".")) < 4 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return false
			}
		}
	}
	return true
}

func validateTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("remote access requires a numeric loopback board address")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return errors.New("remote access requires a numeric loopback board address")
	}
	return nil
}

func Read(state string) (Config, error) {
	data, err := fsx.ReadFile(filepath.Join(state, configFile))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, errors.New("remote access settings cannot be read; run cfo remote off to revoke access")
	}
	if c != (Config{}) {
		if err := c.validate(); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

func write(state string, c Config) error {
	if c != (Config{}) {
		if err := c.validate(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(state, configFile), append(data, '\n'))
}

// Manager changes only its dedicated HTTPS port, after checking every handler
// on that port. The mutex also serializes board requests in one supervisor.
type Manager struct {
	State        string
	Target       string
	Commands     execx.Runner
	StartAtLogin func() error
	// Acquire serializes changes across CLI and supervisor processes. The
	// Windows command layer supplies the home's existing exclusive lock.
	Acquire  func() (func(), error)
	mu       sync.Mutex
	scrawlMu sync.Mutex
	Starter  execx.Starter
	// Answers reports whether a Code Goblins board answers privately at an
	// origin; nil asks it over HTTPS (answersAsBoard). machines is the list
	// Machines last found, at machinesAt.
	Answers    func(ctx context.Context, origin string) bool
	machinesMu sync.Mutex
	machines   []Machine
	machinesAt time.Time
}

type Status struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	URL     string `json:"url,omitempty"`
	Login   string `json:"login,omitempty"`
	Issue   string `json:"issue,omitempty"`
	// Attention is what Issue names, in a word: settings, when they cannot
	// be read, and while remote access is on a state of its Tailscale
	// (tailscaleStates), board_moved, account_changed, funnel,
	// route_missing or start_at_login.
	Attention string `json:"attention,omitempty"`
	// Tailscale is, while remote access is off and only when Detect has read
	// it, what this PC's Tailscale is for turning it on: ready; no_https,
	// signed in on a tailnet that has not approved HTTPS; or what it lacks
	// (tailscaleStates), which TailscaleIssue says in full.
	Tailscale      string `json:"tailscale,omitempty"`
	TailscaleIssue string `json:"tailscale_issue,omitempty"`
	// Fix is the command that mends what Tailscale names, where one does.
	Fix string `json:"fix,omitempty"`
	// Scrawl is whether Scrawl reviews are on, and ScrawlState their state:
	// on, or what is wrong (bypassed, exposed, foreign, outdated or
	// unrouted), which ScrawlIssue says in full and ScrawlFix mends where a
	// command does.
	Scrawl      bool   `json:"scrawl"`
	ScrawlState string `json:"scrawl_state,omitempty"`
	ScrawlReady bool   `json:"scrawl_ready"`
	ScrawlIssue string `json:"scrawl_issue,omitempty"`
	ScrawlFix   string `json:"scrawl_fix,omitempty"`
}

// errRefused is a Tailscale command that ran and exited with an error.
var errRefused = errors.New("Tailscale refused the request; in an Administrator terminal, check tailscale status and tailscale serve --help, then enable HTTPS in your tailnet")

// What this PC's Tailscale can lack for remote access. errNotInstalled is a
// Tailscale CLI found neither on PATH nor where its installer puts it.
var (
	errNotInstalled = errors.New("Tailscale is not installed on this PC; install it from https://tailscale.com/download and sign in")
	errNotRunning   = errors.New("Tailscale is not running on this PC; start Tailscale and sign in")
	errSignedOut    = errors.New("Tailscale is signed out on this PC; open Tailscale and sign in")
	errDisconnected = errors.New("Tailscale is not connected on this PC; open Tailscale and connect")
	errUnsuitable   = errors.New("remote access needs an untagged PC signed in to your Tailscale account, with MagicDNS and HTTPS enabled")
)

// tailscaleStates name each lack in a word, for Status.Tailscale and
// Status.Attention; anything else that kept Tailscale from answering is
// unavailable.
var tailscaleStates = []struct {
	err  error
	name string
}{{errNotInstalled, "missing"}, {errNotRunning, "stopped"}, {errSignedOut, "signed_out"}, {errDisconnected, "disconnected"}, {errUnsuitable, "unsuitable"}}

func tailscaleStateOf(err error) string {
	for _, state := range tailscaleStates {
		if errors.Is(err, state.err) {
			return state.name
		}
	}
	return "unavailable"
}

// errNoAnswer is a Tailscale command its 15 seconds ran out on.
var errNoAnswer = errors.New("did not answer within 15 seconds")

// approvalHint names why a first serve times out: on a tailnet that has not
// approved Serve or HTTPS it prints the approval link and waits for it
// without end. Only serve --bg waits so.
const approvalHint = "; it may be waiting for Serve or HTTPS to be approved for your tailnet"

// program is the Tailscale CLI. Its Windows installer puts it in Program
// Files and adds that folder to the machine's PATH, which a board started
// before the install does not have, so the folder is looked in too.
func program() string {
	if _, err := exec.LookPath("tailscale"); err != nil && os.Getenv("ProgramFiles") != "" {
		installed := filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe")
		if info, err := os.Stat(installed); err == nil && info.Mode().IsRegular() {
			return installed
		}
	}
	return "tailscale"
}

func (m *Manager) command(ctx context.Context, args ...string) ([]byte, error) {
	runner := m.Commands
	if runner == nil {
		runner = execx.OSRunner{}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, execx.Request{Name: program(), Args: args})
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, errNotInstalled
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("tailscale %s %w", strings.Join(args, " "), errNoAnswer)
	case err != nil:
		return nil, fmt.Errorf("Tailscale could not be run: %w", err)
	}
	if result.ExitCode != 0 {
		// Never echo CLI output: it can contain login or HTTPS consent links.
		return nil, errRefused
	}
	return result.Stdout, nil
}

// tailnetSelf is this PC as its Tailscale reports it: its name on the
// tailnet, the board's private origin, the signed-in owner, whether the
// tailnet gives the PC the HTTPS certificates Serve needs, which it does once
// HTTPS is approved, and the owner's other machines online in the same
// tailnet, untagged, by name.
type tailnetSelf struct {
	host, origin, login string
	hasHTTPS            bool
	peers               []string
}

func (m *Manager) identity(ctx context.Context) (tailnetSelf, error) {
	data, err := m.command(ctx, "status", "--json")
	if errors.Is(err, errRefused) {
		return tailnetSelf{}, errNotRunning
	}
	if err != nil {
		return tailnetSelf{}, err
	}
	var status struct {
		BackendState string
		CertDomains  []string
		Self         *struct {
			DNSName string
			UserID  json.Number
			Tags    []string
		}
		Peer map[string]struct {
			DNSName string
			UserID  json.Number
			Online  bool
			Tags    []string
		}
		User map[string]struct{ LoginName string }
	}
	err = json.Unmarshal(data, &status)
	switch {
	case err == nil && status.BackendState == "NeedsLogin":
		return tailnetSelf{}, errSignedOut
	case err != nil || status.BackendState != "Running" || status.Self == nil:
		return tailnetSelf{}, errDisconnected
	}
	name := strings.TrimSuffix(strings.ToLower(status.Self.DNSName), ".")
	login := status.User[status.Self.UserID.String()].LoginName
	if !tailName(name) || login == "" || len(status.Self.Tags) != 0 {
		return tailnetSelf{}, errUnsuitable
	}
	_, tailnet, _ := strings.Cut(name, ".")
	var peers []string
	for _, peer := range status.Peer {
		host := strings.TrimSuffix(strings.ToLower(peer.DNSName), ".")
		if peer.Online && peer.UserID == status.Self.UserID && len(peer.Tags) == 0 && tailName(host) && strings.HasSuffix(host, "."+tailnet) {
			peers = append(peers, host)
		}
	}
	slices.Sort(peers)
	return tailnetSelf{host: name, origin: "https://" + net.JoinHostPort(name, strconv.Itoa(Port)), login: login, hasHTTPS: len(status.CertDomains) > 0, peers: peers}, nil
}

// Detect reads, while remote access is off, what this PC's Tailscale is for
// turning it on (Status.Tailscale), and the command that mends it where one
// does (Status.Fix): Tailscale's install, or the first serve, which asks for
// Serve and HTTPS to be approved for the tailnet and whose route turning
// remote access on then takes as its own. Status leaves this out, since
// cfo doctor and others read it without needing it; the board's setting and
// cfo remote status call it.
func (m *Manager) Detect(ctx context.Context, s Status) Status {
	if s.Enabled || s.Issue != "" {
		return s
	}
	me, err := m.identity(ctx)
	switch {
	case err != nil:
		s.Tailscale, s.TailscaleIssue = tailscaleStateOf(err), err.Error()
	case !me.hasHTTPS:
		s.Tailscale, s.TailscaleIssue = "no_https", "HTTPS is not approved for your tailnet; approve it in Tailscale, then turn remote access on"
	default:
		s.Tailscale = "ready"
	}
	switch {
	case s.Tailscale == "missing":
		s.Fix = "winget install --exact --id Tailscale.Tailscale"
	case s.Tailscale == "no_https" || s.Tailscale == "ready" && s.URL != "":
		// A route reserved by a turn-on that waited on approval, or a
		// tailnet that has not approved HTTPS: the first serve asks.
		s.Fix = "tailscale serve --bg --https=" + strconv.Itoa(Port) + " " + m.Target
	}
	return s
}

type serveConfig struct {
	TCP map[string]json.RawMessage
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string
			Path  string
			Text  string
		}
	}
	AllowFunnel map[string]bool
	Foreground  map[string]json.RawMessage
	Services    map[string]json.RawMessage
}

func (m *Manager) serving(ctx context.Context) (serveConfig, error) {
	data, err := m.command(ctx, "serve", "status", "--json")
	if err != nil {
		return serveConfig{}, err
	}
	var config serveConfig
	if json.Unmarshal(data, &config) != nil {
		return serveConfig{}, errors.New("Tailscale Serve status could not be read; its routes were left unchanged")
	}
	return config, nil
}

// portOwner is empty for a free port, own for our exact root proxy, and other
// for anything else. Unknown TCP modes and foreground configurations fail closed.
func portOwner(s serveConfig, c Config) string {
	u, err := url.Parse(c.Origin)
	if err != nil {
		return "other"
	}
	port := u.Port()
	host := strings.TrimPrefix(c.Origin, "https://")
	occupied := false
	if raw, ok := s.TCP[port]; ok {
		occupied = true
		var mode map[string]json.RawMessage
		if json.Unmarshal(raw, &mode) != nil || len(mode) != 1 || string(mode["HTTPS"]) != "true" {
			return "other"
		}
	}
	for _, foreground := range s.Foreground {
		var nested serveConfig
		if json.Unmarshal(foreground, &nested) != nil || portOwner(nested, c) != "" {
			return "other"
		}
	}
	matched := false
	_, hasTCP := s.TCP[port]
	for key, web := range s.Web {
		_, p, err := net.SplitHostPort(key)
		if err != nil {
			return "other"
		}
		if p != port {
			continue
		}
		occupied = true
		handler, ok := web.Handlers["/"]
		if key != host || len(web.Handlers) != 1 || !ok || handler.Proxy != c.Target || handler.Path != "" || handler.Text != "" {
			return "other"
		}
		matched = true
	}
	if !occupied {
		return ""
	}
	if !matched || !hasTCP {
		return "other"
	}
	return "own"
}

func (m *Manager) Status(ctx context.Context) Status {
	c, err := Read(m.State)
	if err != nil {
		return Status{Issue: err.Error(), Attention: "settings"}
	}
	s := Status{Enabled: c.Enabled, URL: c.Origin, Login: c.Login, Scrawl: c.Scrawl}
	if !c.Enabled {
		return s
	}
	// The board's own supervisor moves remote access to a board that came
	// back at another address (Follow) once Tailscale answers, so until then
	// what Tailscale lacks is what needs him.
	me, err := m.identity(ctx)
	if err != nil {
		s.Issue, s.Attention = err.Error(), tailscaleStateOf(err)
		return s
	}
	if me.origin != c.Origin || me.login != c.Login {
		s.Issue, s.Attention = "The Tailscale account or device name changed. Turn remote access off and on again on this PC.", "account_changed"
		return s
	}
	if c.Target != m.Target {
		s.Issue, s.Attention = "The board moved to another address and its private route could not follow. Turn remote access off and on again on this PC.", "board_moved"
		return s
	}
	config, err := m.serving(ctx)
	if err != nil {
		s.Issue, s.Attention = err.Error(), "unavailable"
		return s
	}
	if config.AllowFunnel[strings.TrimPrefix(c.Origin, "https://")] {
		s.Issue, s.Attention = "Funnel is enabled on the remote port. Turn remote access off and on again; public requests are refused.", "funnel"
		return s
	}
	if portOwner(config, c) != "own" {
		s.Issue, s.Attention = "The private proxy is missing or changed. Turn remote access off and on again on this PC.", "route_missing"
		return s
	}
	s.Ready = true
	s.ScrawlState, s.ScrawlIssue, s.ScrawlFix = m.scrawlStatus(ctx, c, config)
	s.ScrawlReady = s.ScrawlState == "on"
	return s
}

func (m *Manager) Enable(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.State, 0700); err != nil {
		return Status{}, err
	}
	release, err := m.acquire()
	if err != nil {
		return Status{}, fmt.Errorf("remote access is already being changed: %w", err)
	}
	defer release()
	if err := validateTarget(m.Target); err != nil {
		return Status{}, err
	}
	previous, err := Read(m.State)
	if err != nil {
		return Status{}, err
	}
	me, err := m.identity(ctx)
	if err != nil {
		return Status{}, err
	}
	c := Config{Enabled: true, Origin: me.origin, Login: me.login, Target: m.Target}
	if previous.Origin == me.origin && previous.Target == m.Target && previous.Login == me.login {
		c.Scrawl, c.ScrawlTarget, c.ScrawlState = previous.Scrawl, previous.ScrawlTarget, previous.ScrawlState
	} else if previous.ScrawlTarget != "" {
		return Status{}, errors.New("turn remote access off before changing its address or account")
	}
	if err := c.validate(); err != nil {
		return Status{}, err
	}
	config, err := m.serving(ctx)
	if err != nil {
		return Status{}, err
	}
	// A route that is exactly this one, as the first serve run by hand to
	// get Serve and HTTPS approved leaves (Detect), is taken as its own.
	if portOwner(config, c) == "other" && (previous.Origin == "" || portOwner(config, previous) != "own") {
		return Status{}, errors.New("Tailscale port 4311 already serves something else; its routes were left unchanged")
	}
	// Reserve this exact route while access is still off. If HTTPS consent
	// needs the user's terminal, the same route can be adopted on the retry.
	c.Enabled = false
	if err := write(m.State, c); err != nil {
		return Status{}, err
	}
	if _, err := m.command(ctx, "serve", "--bg", "--https=4311", m.Target); err != nil {
		if errors.Is(err, errNoAnswer) {
			err = fmt.Errorf("%w%s", err, approvalHint)
		}
		return Status{}, fmt.Errorf("remote access is off: %w. In an Administrator terminal run tailscale serve --bg --https=4311 %s and follow its HTTPS approval link, then run cfo remote on again", err, m.Target)
	}
	config, err = m.serving(ctx)
	if err != nil {
		return Status{}, err
	}
	if portOwner(config, c) != "own" || config.AllowFunnel[strings.TrimPrefix(c.Origin, "https://")] {
		return Status{}, errors.New("the private Tailscale proxy did not verify; remote access was not enabled")
	}
	c.Enabled = true
	if err := write(m.State, c); err != nil {
		return Status{}, err
	}
	s := Status{Enabled: true, Ready: true, URL: c.Origin, Login: c.Login, Scrawl: c.Scrawl}
	s.ScrawlState, s.ScrawlIssue, s.ScrawlFix = m.scrawlStatus(ctx, c, config)
	s.ScrawlReady = s.ScrawlState == "on"
	if m.StartAtLogin != nil {
		if err := m.StartAtLogin(); err != nil {
			s.Issue, s.Attention = "Remote access is on, but Start at login could not be enabled: "+err.Error(), "start_at_login"
		}
	}
	return s, nil
}

// Follow points remote access at this board when it is on for this PC's
// board at another address, as a restart that finds the usual address taken
// leaves it: the same Tailscale name and account keep their private link,
// and only the routes this home owns move, Scrawl's with the board's. A
// changed name or account, or a route that is no longer this home's, is
// left for Status to name. It reports whether it moved remote access.
func (m *Manager) Follow(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Unreadable settings are Status's to name.
	if c, err := Read(m.State); err != nil || !c.Enabled || c.Target == m.Target {
		return false, nil
	}
	if err := validateTarget(m.Target); err != nil {
		return false, err
	}
	release, err := m.acquire()
	if err != nil {
		return false, fmt.Errorf("remote access is already being changed: %w", err)
	}
	defer release()
	c, err := Read(m.State)
	if err != nil || !c.Enabled || c.Target == m.Target {
		return false, err
	}
	me, err := m.identity(ctx)
	if err != nil {
		return false, err
	}
	if me.origin != c.Origin || me.login != c.Login {
		return false, nil
	}
	config, err := m.serving(ctx)
	if err != nil {
		return false, err
	}
	moved := c
	moved.Target = m.Target
	routes := [][2]Config{{c, moved}}
	if c.ScrawlTarget != "" {
		routes = append(routes, [2]Config{c.scrawlRoute(), moved.scrawlRoute()})
	}
	var serves []Config
	for i, route := range routes {
		if config.AllowFunnel[strings.TrimPrefix(route[1].Origin, "https://")] {
			return false, nil
		}
		from, to := portOwner(config, route[0]), portOwner(config, route[1])
		switch {
		case to == "own":
		case from == "" && i > 0:
			// Scrawl's route reserved by a turn-on that never finished.
		case from != "own":
			return false, nil
		default:
			serves = append(serves, route[1])
		}
	}
	for _, route := range serves {
		u, _ := url.Parse(route.Origin)
		if _, err := m.command(ctx, "serve", "--bg", "--https="+u.Port(), m.Target); err != nil {
			return false, err
		}
	}
	config, err = m.serving(ctx)
	if err != nil {
		return false, err
	}
	for i, route := range routes {
		if owner := portOwner(config, route[1]); owner == "own" && !config.AllowFunnel[strings.TrimPrefix(route[1].Origin, "https://")] || owner == "" && i > 0 {
			continue
		}
		return false, errors.New("the private Tailscale proxy did not verify at the board's new address")
	}
	return true, write(m.State, moved)
}

// followEvery is how often FollowBoard looks again, which reads only the
// settings file unless remote access is on at another board address.
const followEvery = 15 * time.Second

// FollowBoard runs Follow now and every followEvery until ctx ends, so a
// board that came back at another address is reachable again as soon as
// Tailscale answers, a Tailscale that starts after the board included. It
// says each new failure once, and each move.
func (m *Manager) FollowBoard(ctx context.Context, say func(string)) {
	tick := time.NewTicker(followEvery)
	defer tick.Stop()
	last := ""
	for {
		moved, err := m.Follow(ctx)
		switch {
		case moved:
			say("remote access: the board moved to " + m.Target + ", so its private routes now point there")
			last = ""
		case err != nil && err.Error() != last:
			last = err.Error()
			say("remote access: the board moved to " + m.Target + ", and its private routes could not follow yet: " + last)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (m *Manager) Disable(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.State, 0700); err != nil {
		return err
	}
	release, err := m.acquire()
	if err != nil {
		return fmt.Errorf("remote access is already being changed: %w", err)
	}
	defer release()
	c, readErr := Read(m.State)
	c.Enabled = false
	// Revoke first, even if Tailscale is stopped, settings are corrupt, or the
	// route now belongs to another application. Existing streams are canceled.
	if readErr != nil {
		// The gate already fails closed on unreadable settings. Keep the
		// damaged ownership record so uninstall cannot erase the home while
		// background routes whose ownership is unknown remain behind.
		return errors.New("remote access is revoked; its settings are unreadable. Review Tailscale ports 4311 and 4312 and repair the settings before uninstalling")
	}
	if err := write(m.State, c); err != nil {
		return err
	}
	if c.Origin == "" {
		return nil
	}
	config, err := m.serving(ctx)
	if errors.Is(err, errRefused) {
		return errors.New("remote access is revoked; Tailscale is not running on this PC, so its proxy is still there: start Tailscale, then run cfo remote off again")
	}
	if err != nil {
		return fmt.Errorf("remote access is revoked; proxy cleanup needs Tailscale: %w", err)
	}
	routes := []Config{c}
	if c.ScrawlTarget != "" {
		routes = append(routes, c.scrawlRoute())
	}
	var cleanup []error
	for _, route := range routes {
		u, _ := url.Parse(route.Origin)
		owner := portOwner(config, route)
		if current := route; owner == "other" {
			current.Target = m.Target
			if portOwner(config, current) == "own" {
				route, owner = current, "own"
			}
		}
		switch owner {
		case "":
			continue
		case "other":
			cleanup = append(cleanup, fmt.Errorf("remote access is revoked; port %s now serves something else and was left unchanged", u.Port()))
			continue
		}
		if _, err := m.command(ctx, "serve", "--https="+u.Port(), "off"); err != nil {
			cleanup = append(cleanup, err)
			continue
		}
		after, err := m.serving(ctx)
		if err != nil {
			cleanup = append(cleanup, err)
			continue
		}
		if portOwner(after, route) != "" {
			cleanup = append(cleanup, fmt.Errorf("remote access is revoked; Tailscale still reports port %s", u.Port()))
		}
	}
	if err := errors.Join(cleanup...); err != nil {
		return err
	}
	return write(m.State, Config{})
}

func (m *Manager) acquire() (func(), error) {
	if m.Acquire != nil {
		return m.Acquire()
	}
	return func() {}, nil
}
