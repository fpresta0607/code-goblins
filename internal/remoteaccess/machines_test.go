package remoteaccess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// tailnetStatus answers tailscale status with this PC and its peers.
type tailnetStatus struct {
	mu   sync.Mutex
	json string
}

func (s *tailnetStatus) Run(_ context.Context, r execx.Request) (execx.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Join(r.Args, " ") != "status --json" {
		return execx.Result{ExitCode: 1}, nil
	}
	return execx.Result{Stdout: []byte(s.json)}, nil
}

// owner's tailnet: this PC, a laptop and a desktop of the owner's online,
// one offline, a tagged server, a machine another account shared, and a
// node of his in another tailnet.
const ownersTailnet = `{"BackendState":"Running","CertDomains":["home.tail.example.ts.net"],
	"Self":{"DNSName":"home.tail.example.ts.net.","UserID":123},
	"Peer":{
		"k1":{"DNSName":"laptop.tail.example.ts.net.","UserID":123,"Online":true},
		"k2":{"DNSName":"Desk.Tail.Example.ts.net.","UserID":123,"Online":true},
		"k3":{"DNSName":"away.tail.example.ts.net.","UserID":123,"Online":false},
		"k4":{"DNSName":"server.tail.example.ts.net.","UserID":123,"Online":true,"Tags":["tag:server"]},
		"k5":{"DNSName":"shared.tail.example.ts.net.","UserID":456,"Online":true},
		"k6":{"DNSName":"other.elsewhere.example.ts.net.","UserID":123,"Online":true}},
	"User":{"123":{"LoginName":"owner@example.com"},"456":{"LoginName":"friend@example.com"}}}`

const signedOut = `{"BackendState":"NeedsLogin"}`

// boards answers as a Code Goblins board at the origins it holds, while the
// asking is not cut short, and records each origin it was asked about.
type boards struct {
	mu     sync.Mutex
	at     []string
	probed []string
}

func (b *boards) answers(ctx context.Context, origin string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probed = append(b.probed, origin)
	return ctx.Err() == nil && slices.Contains(b.at, origin)
}

func (b *boards) asked() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Sorted(slices.Values(b.probed))
}

// The Overlord, 2026-10-18: "with tailscale connection should we be able to
// see and select the server we want to connect to". The machines listed are
// this PC first, then each other machine of his own in its tailnet, online
// and untagged, whose board answers, by name; nothing else is asked, and a
// request that goes away does not cut the asking short.
func TestMachinesListThisPCAndTheOwnersBoardsOnTheTailnet(t *testing.T) {
	// Arrange
	b := &boards{at: []string{"https://laptop.tail.example.ts.net:4311", "https://desk.tail.example.ts.net:4311"}}
	m := &Manager{State: t.TempDir(), Target: "http://127.0.0.1:4310", Commands: &tailnetStatus{json: ownersTailnet}, Answers: b.answers}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	machines := m.Machines(ctx)

	// Assert
	want := []Machine{{Name: "home", URL: "https://home.tail.example.ts.net:4311", IsThis: true}, {Name: "desk", URL: "https://desk.tail.example.ts.net:4311"}, {Name: "laptop", URL: "https://laptop.tail.example.ts.net:4311"}}
	if !slices.Equal(machines, want) {
		t.Errorf("machines = %+v, want %+v", machines, want)
	}
	if asked, want := b.asked(), []string{"https://desk.tail.example.ts.net:4311", "https://laptop.tail.example.ts.net:4311"}; !slices.Equal(asked, want) {
		t.Errorf("asked %q, want only his own online untagged machines in this tailnet %q", asked, want)
	}
}

// A PC whose Tailscale is not signed in to a tailnet remote access can use
// lists no machine and asks none, and reads Tailscale again at the next call.
func TestMachinesWithoutAUsableTailscaleListNoneUntilItIs(t *testing.T) {
	for name, status := range map[string]string{
		"signed out": signedOut,
		"tagged":     `{"BackendState":"Running","Self":{"DNSName":"home.tail.example.ts.net.","UserID":123,"Tags":["tag:pc"]},"Peer":{"k1":{"DNSName":"laptop.tail.example.ts.net.","UserID":123,"Online":true}},"User":{"123":{"LoginName":"owner@example.com"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			b := &boards{at: []string{"https://laptop.tail.example.ts.net:4311"}}
			tailscale := &tailnetStatus{json: status}
			m := &Manager{State: t.TempDir(), Target: "http://127.0.0.1:4310", Commands: tailscale, Answers: b.answers}

			// Act
			machines := m.Machines(context.Background())
			tailscale.json = ownersTailnet
			again := m.Machines(context.Background())

			// Assert
			if machines == nil || len(machines) != 0 || len(b.asked()) != 2 {
				t.Errorf("machines = %#v, asked %q; want an empty list, then the tailnet asked", machines, b.asked())
			}
			if len(again) != 2 {
				t.Errorf("once Tailscale is ready, machines = %+v, want this PC and the laptop", again)
			}
		})
	}
}

// Reading the setting again soon after lists what was found without asking
// each machine again, and asks again once the list is older than
// machinesFor.
func TestMachinesAreAskedAtMostTwiceAMinute(t *testing.T) {
	// Arrange
	b := &boards{at: []string{"https://laptop.tail.example.ts.net:4311"}}
	m := &Manager{State: t.TempDir(), Target: "http://127.0.0.1:4310", Commands: &tailnetStatus{json: ownersTailnet}, Answers: b.answers}
	first := m.Machines(context.Background())

	// Act
	soon := m.Machines(context.Background())
	m.machinesAt = time.Now().Add(-machinesFor - time.Second)
	later := m.Machines(context.Background())

	// Assert
	if !slices.Equal(soon, first) || !slices.Equal(later, first) || len(b.asked()) != 4 {
		t.Errorf("soon %+v, later %+v after %d asks; want %+v, asked twice for each of the two machines", soon, later, len(b.asked()), first)
	}
}

// A machine's board answers when its private origin serves the board's
// manifest itself: anything else, another server, a redirect or more than
// any manifest holds, is no board.
func TestABoardAnswersByItsManifest(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		want   bool
	}{
		"the board":                {http.StatusOK, `{"name":"Code Goblins","display":"standalone"}`, true},
		"another server":           {http.StatusOK, `{"name":"Photos"}`, false},
		"no manifest":              {http.StatusNotFound, `{"error":"not found"}`, false},
		"not JSON":                 {http.StatusOK, `<html></html>`, false},
		"a redirect to a manifest": {http.StatusFound, "", false},
		"more than a manifest":     {http.StatusOK, `{"padding":"` + strings.Repeat("x", 70<<10) + `","name":"Code Goblins"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/manifest.webmanifest":
					if c.status == http.StatusFound {
						http.Redirect(w, r, "/elsewhere.webmanifest", http.StatusFound)
						return
					}
					w.WriteHeader(c.status)
					_, _ = w.Write([]byte(c.body))
				case "/elsewhere.webmanifest":
					_, _ = w.Write([]byte(`{"name":"Code Goblins"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := *boardClient
			client.Transport = server.Client().Transport

			// Act
			got := answersAsBoard(context.Background(), &client, server.URL)

			// Assert
			if got != c.want {
				t.Errorf("answersAsBoard = %v, want %v", got, c.want)
			}
		})
	}
}
