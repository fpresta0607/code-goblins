package remoteaccess

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Machine is a computer on the owner's tailnet whose Code Goblins board
// answers privately, by its name there, which the board's setting lists for
// him to pick the one the board and Scrawl connect to. IsThis marks the one
// whose board lists it.
type Machine struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	IsThis bool   `json:"is_this"`
}

// machinesFor is how long Machines keeps the list it found, so the setting
// read again asks no machine more than twice a minute.
const machinesFor = 30 * time.Second

// boardClient asks a machine on the tailnet for its board directly: a proxy
// in the supervisor's environment reaches no tailnet address, and would learn
// the name of each machine asked.
var boardClient = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Machines lists this PC, then each other machine of the owner's online in
// its tailnet and untagged whose board answers privately, asking them all at
// once. It is empty while this PC's Tailscale is not signed in to a tailnet
// remote access can use, which it reads again at the next call; a list it
// found is kept for machinesFor. A request that goes away does not cut the
// asking short, which its own time limits bound.
func (m *Manager) Machines(ctx context.Context) []Machine {
	m.machinesMu.Lock()
	defer m.machinesMu.Unlock()
	if !m.machinesAt.IsZero() && time.Since(m.machinesAt) < machinesFor {
		return m.machines
	}
	ctx = context.WithoutCancel(ctx)
	me, err := m.identity(ctx)
	if err != nil {
		return []Machine{}
	}
	answers := m.Answers
	if answers == nil {
		answers = func(ctx context.Context, origin string) bool { return answersAsBoard(ctx, boardClient, origin) }
	}
	origin := func(host string) string { return "https://" + net.JoinHostPort(host, strconv.Itoa(Port)) }
	found := make([]bool, len(me.peers))
	var asking sync.WaitGroup
	for i, peer := range me.peers {
		asking.Go(func() { found[i] = answers(ctx, origin(peer)) })
	}
	asking.Wait()
	name := func(host string) string { return strings.Split(host, ".")[0] }
	machines := []Machine{{Name: name(me.host), URL: me.origin, IsThis: true}}
	for i, peer := range me.peers {
		if found[i] {
			machines = append(machines, Machine{Name: name(peer), URL: origin(peer)})
		}
	}
	m.machines, m.machinesAt = machines, time.Now()
	return machines
}

// answersAsBoard reports whether origin serves a Code Goblins board's web app
// manifest, which every board serves, through the owner gate of its own.
func answersAsBoard(ctx context.Context, client *http.Client, origin string) bool {
	request, err := http.NewRequestWithContext(ctx, "GET", origin+"/manifest.webmanifest", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var manifest struct {
		Name string `json:"name"`
	}
	return response.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&manifest) == nil && manifest.Name == "Code Goblins"
}
