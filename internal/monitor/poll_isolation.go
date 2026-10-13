package monitor

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
)

// A poll is isolated only when its explicit state and port are independent,
// its local server confirms that state, and its page is absent from live reviews.
// Missing evidence leaves the poll detectable, regardless of its process name.
func isolatedPagePoll(ctx context.Context, page string, environment []string) bool {
	if !filepath.IsAbs(page) {
		return false
	}
	if info, err := os.Stat(page); err != nil || !info.Mode().IsRegular() {
		return false
	}
	var stateDir, port, host string
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "LAVISH_AXI_STATE_DIR":
			stateDir = value
		case "LAVISH_AXI_PORT":
			port = value
		case "LAVISH_AXI_HOST":
			host = value
		}
	}
	liveState := os.Getenv("LAVISH_AXI_STATE_DIR")
	if liveState == "" {
		profile, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		liveState = filepath.Join(profile, ".lavish-axi")
	}
	if !filepath.IsAbs(stateDir) || samePollPath(stateDir, liveState) {
		return false
	}
	for _, directory := range []string{stateDir, liveState} {
		if info, err := os.Stat(directory); err != nil || !info.IsDir() {
			return false
		}
	}
	livePort := os.Getenv("LAVISH_AXI_PORT")
	if livePort == "" {
		livePort = "4387"
	}
	portNumber, err := strconv.Atoi(port)
	livePortNumber, livePortError := strconv.Atoi(livePort)
	if err != nil || livePortError != nil || portNumber < 1 || portNumber > 65535 || portNumber == livePortNumber {
		return false
	}
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	if address := net.ParseIP(host); address == nil || !address.IsLoopback() {
		return false
	}
	sessions, err := (axi.Lavish{}).Sessions()
	if err != nil {
		return false
	}
	for _, session := range sessions {
		if samePollPath(page, session.File) {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/health", nil)
	if err != nil {
		return false
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var health struct {
		App      string `json:"app"`
		StateDir string `json:"state_dir"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&health) != nil {
		return false
	}
	return health.App == "lavish-axi" && samePollPath(health.StateDir, stateDir) && !samePollPath(health.StateDir, liveState)
}

func samePollPath(left, right string) bool {
	if strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
