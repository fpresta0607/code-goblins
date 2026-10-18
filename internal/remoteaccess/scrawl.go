package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func (c Config) scrawlRoute() Config {
	u, _ := url.Parse(c.Origin)
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(ScrawlPort))
	return Config{Origin: u.String(), Login: c.Login, Target: c.Target}
}

type scrawlHealth struct {
	App            string   `json:"app"`
	StateID        string   `json:"state_id"`
	StateDir       string   `json:"state_dir"`
	Hosts          []string `json:"hosts"`
	RequestedHosts []string `json:"requested_hosts"`
	LinkOrigin     string   `json:"link_origin"`
}

// pathMount is a Serve mount a command can name as it is, which the command
// that removes a direct route to Scrawl names so that it removes no other
// handler on the port.
var pathMount = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)

var scrawlClient = &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// What can be wrong with the Scrawl server behind the private route, each a
// state the Scrawl switch shows in a word (scrawlStatus). A stopped server
// is not wrong: the first review a phone opens starts it again.
var (
	errScrawlStopped  = errors.New("Scrawl is stopped; it starts with the next review")
	errScrawlForeign  = errors.New("the Scrawl port belongs to another or older server; update Scrawl and check LAVISH_AXI_PORT on this PC")
	errScrawlOutdated = errors.New("Scrawl does not report its network listeners; update Scrawl on this PC")
)

func readScrawl(ctx context.Context, c Config) (scrawlHealth, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", c.ScrawlTarget+"/health", nil)
	if err != nil {
		return scrawlHealth{}, err
	}
	response, err := scrawlClient.Do(r)
	if err != nil {
		// Nothing listening is a stopped Scrawl, which the next review starts;
		// a listener that does not answer as Scrawl is some other server.
		conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", strings.TrimPrefix(c.ScrawlTarget, "http://"))
		if dialErr != nil {
			return scrawlHealth{}, errScrawlStopped
		}
		conn.Close()
		return scrawlHealth{}, errScrawlForeign
	}
	defer response.Body.Close()
	var h scrawlHealth
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&h) != nil || h.App != "lavish-axi" || h.StateID == "" || !strings.EqualFold(filepath.Clean(h.StateDir), filepath.Clean(c.ScrawlState)) {
		return scrawlHealth{}, errScrawlForeign
	}
	return h, nil
}

func privateScrawl(ctx context.Context, c Config) error {
	h, err := readScrawl(ctx, c)
	if err != nil {
		return err
	}
	link, err := url.Parse(h.LinkOrigin)
	target, _ := url.Parse(c.ScrawlTarget)
	if err != nil || link.Scheme != "http" || link.Port() != target.Port() || link.User != nil || link.Path != "" || link.RawQuery != "" || link.Fragment != "" || (link.Hostname() != "localhost" && (net.ParseIP(link.Hostname()) == nil || !net.ParseIP(link.Hostname()).IsLoopback())) {
		return errors.New("Scrawl still advertises another network origin; turn Scrawl reviews off and on again on this PC to restart it privately")
	}
	for _, hosts := range [][]string{h.Hosts, h.RequestedHosts} {
		if len(hosts) == 0 {
			return errScrawlOutdated
		}
		for _, host := range hosts {
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return errors.New("Scrawl also listens outside this PC; turn Scrawl reviews off and on again on this PC to restart it privately, keeping its sessions")
			}
		}
	}
	return nil
}

// scrawlStatus is Scrawl reviews' state in a word (Status.ScrawlState), in
// full, and the command that mends it where one does. A route of the owner's
// own that serves Scrawl straight to the tailnet shows even while reviews are
// off: Scrawl then answers phones, but without the owner check.
func (m *Manager) scrawlStatus(ctx context.Context, c Config, serving serveConfig) (string, string, string) {
	direct := c
	if direct.ScrawlTarget == "" {
		direct.ScrawlTarget, _, _ = scrawlSettings()
	}
	if direct.ScrawlTarget != "" {
		if isDirect, fix := directScrawlRoute(serving, direct); isDirect {
			return "bypassed", "Your own Tailscale route serves Scrawl to your tailnet without the owner check. Remove it in Tailscale, then turn Scrawl reviews on.", fix
		}
	}
	if !c.Scrawl {
		return "", "Turn Scrawl reviews on to open reviews through the same private connection.", ""
	}
	route := c.scrawlRoute()
	if portOwner(serving, route) != "own" || serving.AllowFunnel[strings.TrimPrefix(route.Origin, "https://")] {
		return "unrouted", "Scrawl's private route is missing or changed; turn Scrawl reviews off and on again on this PC.", ""
	}
	err := privateScrawl(ctx, c)
	switch {
	case err == nil, errors.Is(err, errScrawlStopped):
		return "on", "", ""
	case errors.Is(err, errScrawlForeign):
		return "foreign", err.Error(), ""
	case errors.Is(err, errScrawlOutdated):
		return "outdated", err.Error(), ""
	}
	return "exposed", err.Error(), ""
}

// LocalScrawlEnvironment scopes the network policy to Code Goblins children.
// Existing terminals keep their environment; the proxy always verifies the
// live server's actual and requested listeners before forwarding a request.
func LocalScrawlEnvironment(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "LAVISH_AXI_HOST") || strings.EqualFold(key, "LAVISH_AXI_LINK_URL") || strings.EqualFold(key, "LAVISH_AXI_LINK_HOST") || strings.EqualFold(key, "LAVISH_AXI_ALLOWED_HOSTS") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "LAVISH_AXI_HOST=127.0.0.1")
}

func scrawlSettings() (string, string, error) {
	port := os.Getenv("LAVISH_AXI_PORT")
	if port == "" {
		port = "4387"
	}
	target := "http://127.0.0.1:" + port
	if err := validateTarget(target); err != nil {
		return "", "", errors.New("LAVISH_AXI_PORT must be a valid local port")
	}
	state := os.Getenv("LAVISH_AXI_STATE_DIR")
	if state == "" {
		profile, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		state = filepath.Join(profile, ".lavish-axi")
	}
	state, err := filepath.Abs(state)
	return target, state, err
}

// EnableScrawl is an explicit local-only setup action. The supported stop
// command preserves sessions and queued feedback; no PID is killed by name.
func (m *Manager) EnableScrawl(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.acquire()
	if err != nil {
		return Status{}, err
	}
	defer release()
	c, err := Read(m.State)
	if err != nil {
		return Status{}, err
	}
	if !c.Enabled || c.Target != m.Target {
		return Status{}, errors.New("turn Tailscale on first")
	}
	me, err := m.identity(ctx)
	if err != nil || me.origin != c.Origin || me.login != c.Login {
		return Status{}, errors.New("check the PC's Tailscale account, then turn Tailscale off and on again")
	}
	previous := c
	c.ScrawlTarget, c.ScrawlState, err = scrawlSettings()
	if err != nil {
		return Status{}, err
	}
	if c.ScrawlTarget == c.Target {
		return Status{}, errors.New("Scrawl and the board need different local ports")
	}
	config, err := m.serving(ctx)
	if err != nil {
		return Status{}, err
	}
	if isDirect, _ := directScrawlRoute(config, c); isDirect {
		return Status{}, errors.New("your own Tailscale route serves Scrawl without the owner check; remove that route in Tailscale first, then turn Scrawl reviews on and reopen older HTTPS reviews on this PC")
	}
	route := c.scrawlRoute()
	if owner := portOwner(config, route); owner != "" && (previous.ScrawlTarget == "" || portOwner(config, previous.scrawlRoute()) != "own") {
		return Status{}, errors.New("Tailscale port 4312 already serves something else; its routes were left unchanged")
	}
	// Keep ownership before either a partial proxy setup or an interrupted
	// restart, so off/uninstall can always remove precisely our route.
	c.Scrawl = false
	if err := write(m.State, c); err != nil {
		return Status{}, err
	}
	if err := m.startScrawl(ctx, c, true); err != nil {
		return Status{}, err
	}
	if _, err := m.command(ctx, "serve", "--bg", "--https=4312", m.Target); err != nil {
		if errors.Is(err, errNoAnswer) {
			err = fmt.Errorf("%w%s", err, approvalHint)
		}
		return Status{}, err
	}
	config, err = m.serving(ctx)
	if err != nil {
		return Status{}, err
	}
	if portOwner(config, route) != "own" || config.AllowFunnel[strings.TrimPrefix(route.Origin, "https://")] {
		return Status{}, errors.New("Scrawl's private proxy did not verify")
	}
	c.Scrawl = true
	if err := write(m.State, c); err != nil {
		return Status{}, err
	}
	return m.Status(ctx), nil
}

// DisableScrawl turns Scrawl reviews off: phones lose them at once, Scrawl's
// own route is removed and the board's is kept. Scrawl keeps running on this
// PC, on loopback. A route on port 4312 that is not its own is left as it is.
func (m *Manager) DisableScrawl(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	release, err := m.acquire()
	if err != nil {
		return Status{}, err
	}
	defer release()
	c, err := Read(m.State)
	if err != nil {
		return Status{}, err
	}
	if c.ScrawlTarget == "" {
		return m.Status(ctx), nil
	}
	c.Scrawl = false
	if err := write(m.State, c); err != nil {
		return Status{}, err
	}
	route := c.scrawlRoute()
	config, err := m.serving(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("Scrawl reviews are off; removing their route needs Tailscale: %w", err)
	}
	switch portOwner(config, route) {
	case "other":
		return Status{}, errors.New("Scrawl reviews are off; Tailscale port 4312 now serves something else and was left unchanged")
	case "own":
		if _, err := m.command(ctx, "serve", "--https=4312", "off"); err != nil {
			return Status{}, fmt.Errorf("Scrawl reviews are off; their route could not be removed: %w", err)
		}
		after, err := m.serving(ctx)
		if err != nil {
			return Status{}, fmt.Errorf("Scrawl reviews are off; their route could not be read back: %w", err)
		}
		if portOwner(after, route) != "" {
			return Status{}, errors.New("Scrawl reviews are off; Tailscale still reports port 4312")
		}
	}
	c.ScrawlTarget, c.ScrawlState = "", ""
	if err := write(m.State, c); err != nil {
		return Status{}, err
	}
	return m.Status(ctx), nil
}

func (m *Manager) startScrawl(ctx context.Context, c Config, restart bool) error {
	m.scrawlMu.Lock()
	defer m.scrawlMu.Unlock()
	if privateScrawl(ctx, c) == nil {
		return nil
	}
	// Do not stop or adopt an unknown listener, even at the chosen port.
	h, healthErr := readScrawl(ctx, c)
	if healthErr != nil {
		conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", strings.TrimPrefix(c.ScrawlTarget, "http://"))
		if dialErr == nil {
			conn.Close()
			return healthErr
		}
	} else if !restart {
		return errors.New("Scrawl's network settings changed; turn Scrawl reviews off and on again on this PC")
	}
	env := LocalScrawlEnvironment(os.Environ())
	env = append(env, "LAVISH_AXI_STATE_DIR="+c.ScrawlState, "LAVISH_AXI_PORT="+strings.TrimPrefix(c.ScrawlTarget, "http://127.0.0.1:"))
	profile, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	runner := m.Commands
	if runner == nil {
		runner = execx.OSRunner{}
	}
	if h.App != "" {
		result, err := runner.Run(ctx, execx.Request{Name: "lavish-axi", Args: []string{"stop"}, Dir: profile, Env: env})
		if err != nil || result.ExitCode != 0 {
			return errors.New("Scrawl could not be restarted; run lavish-axi stop on this PC, then turn Scrawl reviews on again")
		}
	}
	starter := m.Starter
	if starter == nil {
		starter = execx.OSRunner{}
	}
	if err := starter.Start(ctx, execx.Request{Name: "lavish-axi", Args: []string{"server"}, Dir: profile, Env: env}); err != nil {
		return errors.New("install or update Scrawl with Code Goblins on this PC, then turn Scrawl reviews on again")
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if privateScrawl(ctx, c) == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Scrawl did not verify as private; check its setup on this PC and retry")
		case <-tick.C:
		}
	}
}

func (m *Manager) serveScrawl(w http.ResponseWriter, r *http.Request, c Config) {
	if err := privateScrawl(r.Context(), c); err != nil {
		// An idle server can stop itself. Wake it with the stored loopback
		// policy, but never replace a live server from a remote request.
		if err = m.startScrawl(r.Context(), c, false); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	target, _ := url.Parse(c.ScrawlTarget)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(out *http.Request) {
		out.URL.Scheme, out.URL.Host, out.Host = target.Scheme, target.Host, target.Host
		for name := range out.Header {
			if strings.HasPrefix(http.CanonicalHeaderKey(name), "Tailscale-") || strings.HasPrefix(http.CanonicalHeaderKey(name), "X-Forwarded-") || strings.EqualFold(name, "Forwarded") {
				out.Header.Del(name)
			}
		}
		if out.Header.Get("Origin") != "" {
			out.Header.Set("Origin", c.ScrawlTarget)
		}
		if ref, err := url.Parse(out.Header.Get("Referer")); err == nil && ref.Host != "" {
			ref.Scheme, ref.Host = target.Scheme, target.Host
			out.Header.Set("Referer", ref.String())
		}
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if location, err := url.Parse(response.Header.Get("Location")); err == nil && location.Host == target.Host {
			remote, _ := url.Parse(c.scrawlRoute().Origin)
			location.Scheme, location.Host = remote.Scheme, remote.Host
			response.Header.Set("Location", location.String())
		}
		// Keep upstream artifact sandbox/CSP and all normal WebSocket behavior.
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Scrawl is reconnecting; refresh this review", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

// ScrawlLink maps only known session URLs, including reviews opened before
// remote access was enabled. Other websites never pass through this proxy.
func (m *Manager) ScrawlLink(ctx context.Context, raw string) (string, error) {
	c, err := Read(m.State)
	if err != nil || !c.Enabled || !c.Scrawl {
		return "", errors.New("turn Scrawl reviews on in the CFO Task panel on this PC first")
	}
	u, err := url.Parse(raw)
	target, _ := url.Parse(c.ScrawlTarget)
	board, _ := url.Parse(c.Origin)
	if err != nil || u.User != nil || u.Scheme != "http" || u.Port() != target.Port() || !strings.HasPrefix(u.Path, "/session/") || strings.TrimPrefix(u.Path, "/session/") == "" {
		return "", errors.New("this review uses an older or unconfigured address; reopen the review on this PC to refresh its link, then open it from the phone board")
	}
	ip := net.ParseIP(u.Hostname())
	known := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback() || u.Hostname() == board.Hostname()
	if !known && ip != nil {
		data, err := m.command(ctx, "status", "--json")
		var status struct {
			BackendState string
			Self         *struct {
				DNSName      string
				TailscaleIPs []string
			}
		}
		if err == nil && json.Unmarshal(data, &status) == nil && status.BackendState == "Running" && status.Self != nil && strings.TrimSuffix(strings.ToLower(status.Self.DNSName), ".") == board.Hostname() {
			for _, address := range status.Self.TailscaleIPs {
				if ip.Equal(net.ParseIP(address)) {
					known = true
					break
				}
			}
		}
	}
	if !known {
		return "", errors.New("this review is not on the configured Scrawl PC; reopen the review on this PC to refresh its link")
	}
	remote, _ := url.Parse(c.scrawlRoute().Origin)
	u.Scheme, u.Host = remote.Scheme, remote.Host
	return u.String(), nil
}

// directScrawlRoute finds a Serve route straight to Scrawl, which would
// bypass the owner gate, and the command that removes it. The route is the
// user's, so setup diagnoses it and never takes it over or removes it; the
// command is for him to run, and there is none for a route inside a
// foreground serve or a Tailscale service, which their own definitions hold,
// or at a mount a command cannot name as it is.
func directScrawlRoute(config serveConfig, c Config) (bool, string) {
	target, _ := url.Parse(c.ScrawlTarget)
	isLoopback := func(host string) bool {
		ip := net.ParseIP(host)
		return host == "localhost" || ip != nil && ip.IsLoopback()
	}
	off := func(flag, port, mount string) string {
		if _, err := strconv.Atoi(port); err != nil || mount != "" && !pathMount.MatchString(mount) {
			return ""
		}
		command := "tailscale serve --" + flag + "=" + port
		if mount != "" && mount != "/" {
			command += " --set-path=" + mount
		}
		return command + " off"
	}
	for port, raw := range config.TCP {
		var tcp struct{ TCPForward string }
		if json.Unmarshal(raw, &tcp) == nil {
			host, forwarded, err := net.SplitHostPort(tcp.TCPForward)
			if err == nil && forwarded == target.Port() && isLoopback(host) {
				return true, off("tcp", port, "")
			}
		}
	}
	for key, web := range config.Web {
		for mount, handler := range web.Handlers {
			u, err := url.Parse(handler.Proxy)
			if err == nil && u.Scheme == "http" && u.Port() == target.Port() && isLoopback(u.Hostname()) {
				_, port, _ := net.SplitHostPort(key)
				var mode struct{ HTTP bool }
				_ = json.Unmarshal(config.TCP[port], &mode)
				if mode.HTTP {
					return true, off("http", port, mount)
				}
				return true, off("https", port, mount)
			}
		}
	}
	for _, children := range []map[string]json.RawMessage{config.Foreground, config.Services} {
		for _, raw := range children {
			var child serveConfig
			if json.Unmarshal(raw, &child) == nil {
				if isDirect, _ := directScrawlRoute(child, c); isDirect {
					return true, ""
				}
			}
		}
	}
	return false, ""
}
