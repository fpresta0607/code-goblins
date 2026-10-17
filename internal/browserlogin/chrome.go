package browserlogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

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
// a private inherited DevTools pipe, and no first-run pages.
func (l Launch) Arguments() []string {
	arguments := []string{
		"--user-data-dir=" + l.Profile,
		"--remote-debugging-pipe",
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

// ClearPort removes an old debugger advertisement before the profile is
// opened through a private pipe.
func ClearPort(profile string) error {
	err := fsx.Remove(filepath.Join(profile, activePortFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
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
