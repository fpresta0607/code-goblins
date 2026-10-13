package browserlogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Launch is how a Chrome starts on a login's profile.
type Launch struct {
	Executable string
	Profile    string
	// Hosts are the only host names the browser may reach, each a name or
	// *. and a name. Every other name resolves to nothing, so a page that
	// steers a goblin elsewhere leads nowhere. None reaches every host.
	Hosts []string
	// Headless starts it with no window at all.
	Headless bool
	Page     string
}

// Arguments are Chrome's command line for the launch: the login's profile,
// a DevTools port Chrome picks on 127.0.0.1, which it writes into the
// profile, and no first-run pages.
func (l Launch) Arguments() []string {
	arguments := []string{
		"--user-data-dir=" + l.Profile,
		"--remote-debugging-port=0",
		"--no-first-run",
		"--no-default-browser-check",
	}
	if l.Headless {
		arguments = append(arguments, "--headless")
	}
	if len(l.Hosts) > 0 {
		arguments = append(arguments, "--host-resolver-rules="+HostRules(l.Hosts))
	}
	page := l.Page
	if page == "" {
		page = "about:blank"
	}
	return append(arguments, page)
}

// HostRules are Chrome's resolver rules that leave hosts as they are and
// resolve every other name to nothing.
func HostRules(hosts []string) string {
	rules := []string{"MAP * ~NOTFOUND"}
	for _, host := range hosts {
		rules = append(rules, "EXCLUDE "+host)
	}
	return strings.Join(rules, ", ")
}

// activePortFile is where Chrome writes the DevTools port it picked.
const activePortFile = "DevToolsActivePort"

// ClearPort removes the port file an earlier Chrome of the profile left, so
// the next one's port is the only one read.
func ClearPort(profile string) error {
	err := fsx.Remove(filepath.Join(profile, activePortFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// AwaitDevTools waits for the Chrome on profile to write its DevTools port
// and answer on it, and returns its address, http://127.0.0.1:<port>.
func AwaitDevTools(ctx context.Context, profile string) (string, error) {
	for {
		if data, err := fsx.ReadFile(filepath.Join(profile, activePortFile)); err == nil {
			line, _, _ := strings.Cut(string(data), "\n")
			if port, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && port > 0 && port < 65536 {
				address := "http://127.0.0.1:" + strconv.Itoa(port)
				if _, err := browserSocket(ctx, address); err == nil {
					return address, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("chrome did not open its DevTools port: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// browserSocket is the browser's own DevTools WebSocket at address.
func browserSocket(ctx context.Context, address string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/json/version", nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s/json/version answered %s", address, response.Status)
	}
	var version struct {
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil {
		return "", err
	}
	if !strings.HasPrefix(version.Socket, "ws://127.0.0.1:") {
		return "", fmt.Errorf("%s gave no DevTools socket on this machine", address)
	}
	return version.Socket, nil
}

// CloseBrowser asks the Chrome at address to close itself, which writes its
// session to the profile first, and returns once it has taken the command.
func CloseBrowser(ctx context.Context, address string) error {
	socket, err := browserSocket(ctx, address)
	if err != nil {
		return err
	}
	conn, err := cdp.Dial(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Call(ctx, "Browser.close", nil, nil)
}
