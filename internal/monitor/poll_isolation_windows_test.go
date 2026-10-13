package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessPollsOnlyExcludesProvenIsolatedScrawl(t *testing.T) {
	for _, name := range []string{"isolated", "live state", "live state alternate spelling", "live port", "live page", "live page alias", "unreadable live sessions", "foreign server", "unknown server", "missing state", "remote host", "unnamed page", "redirect", "unreachable", "slow health", "oversized health"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			fixture := newPollFixture(t)
			worktree := fixture.dir("app", ".worktrees", "gb-scrawl-proof")
			page := filepath.Join(worktree, "probe.html")
			if err := os.WriteFile(page, []byte("<html>probe</html>"), 0o600); err != nil {
				t.Fatal(err)
			}
			liveState, scratchState := fixture.dir("live"), fixture.dir("scratch")
			liveData := []byte(`{"sessions":{}}`)
			if name == "live page" || name == "live page alias" {
				livePage := page
				if name == "live page alias" {
					livePage = filepath.Join(liveState, "owner.html")
					if err := os.Link(page, livePage); err != nil {
						t.Fatal(err)
					}
				}
				liveData, _ = json.Marshal(map[string]map[string]map[string]string{"sessions": {"live": {"file": livePage}}})
			}
			if name == "unreadable live sessions" {
				liveData = []byte("invalid")
			}
			if err := os.WriteFile(filepath.Join(liveState, "state.json"), liveData, 0o600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/health" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				if name == "redirect" {
					http.Redirect(w, r, "/poll", http.StatusFound)
					return
				}
				if name == "slow health" {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(time.Second):
					}
				}
				app, directory := "lavish-axi", scratchState
				if name == "foreign server" {
					directory = liveState
				}
				if name == "unknown server" {
					app = "other"
				}
				health := map[string]string{"app": app, "state_dir": directory}
				if name == "oversized health" {
					health["padding"] = strings.Repeat("x", 64<<10)
				}
				json.NewEncoder(w).Encode(health)
			}))
			defer server.Close()
			address, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAVISH_AXI_STATE_DIR", scratchState)
			t.Setenv("LAVISH_AXI_HOST", "127.0.0.1")
			t.Setenv("LAVISH_AXI_PORT", address.Port())
			if name == "live state" {
				t.Setenv("LAVISH_AXI_STATE_DIR", liveState)
			}
			if name == "live state alternate spelling" {
				t.Setenv("LAVISH_AXI_STATE_DIR", liveState+string(filepath.Separator)+".")
			}
			if name == "missing state" {
				t.Setenv("LAVISH_AXI_STATE_DIR", "")
			}
			if name == "remote host" {
				t.Setenv("LAVISH_AXI_HOST", "192.0.2.1")
			}
			arguments := []string{"poll", page, "--timeout-ms", "60000"}
			if name == "unnamed page" {
				arguments = []string{"poll", "--timeout-ms", "60000"}
			}
			pid := fixture.poll(worktree, arguments...)
			t.Setenv("LAVISH_AXI_STATE_DIR", liveState)
			t.Setenv("LAVISH_AXI_PORT", "4387")
			if name == "live port" {
				t.Setenv("LAVISH_AXI_PORT", address.Port())
			}
			if name == "unreachable" {
				server.Close()
			}

			// Act
			_, isListed := listed(t, pid)

			// Assert
			if shouldList := name != "isolated"; isListed != shouldList {
				t.Fatalf("listed = %v, want %v for %s", isListed, shouldList, name)
			}
		})
	}
}
