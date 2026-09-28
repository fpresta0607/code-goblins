package supervisor

import (
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/siqspeak"
)

func TestVoiceHistoryRequiresTheBoardOriginAndInstance(t *testing.T) {
	for _, test := range []struct {
		name      string
		method    string
		origin    string
		token     string
		instance  string
		host      string
		fetchSite string
		want      int
	}{
		{name: "board", method: "POST", origin: "http://board.local", token: "current", instance: "current", want: 200},
		{name: "no token", method: "POST", origin: "http://board.local", instance: "current", want: 403},
		{name: "expired instance", method: "POST", origin: "http://board.local", token: "old", instance: "current", want: 403},
		{name: "no origin", method: "POST", token: "current", instance: "current", want: 403},
		{name: "cross origin", method: "POST", origin: "http://elsewhere.local", token: "current", instance: "current", want: 403},
		{name: "cross site", method: "POST", origin: "http://board.local", token: "current", instance: "current", fetchSite: "cross-site", want: 403},
		{name: "untrusted host", method: "POST", host: "elsewhere.local", origin: "http://board.local", token: "current", instance: "current", want: 403},
		{name: "uninitialized board", method: "POST", origin: "http://board.local", want: 403},
		{name: "get", method: "GET", instance: "current", want: 404},
		{name: "head", method: "HEAD", instance: "current", want: 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHTTP(&Service{Instance: test.instance}, "board.local", nil)
			calls := 0
			handler.readVoice = func() (siqspeak.Snapshot, error) {
				calls++
				return siqspeak.Snapshot{State: "running", History: siqspeak.History{Entries: []siqspeak.Entry{{Text: "private words"}}}}, nil
			}
			request := httptest.NewRequest(test.method, "http://board.local/api/voice", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-CFO-Token", test.token)
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			if test.host != "" {
				request.Host = test.host
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			isAuthorized := test.want == 200
			if (calls == 1) != isAuthorized || strings.Contains(response.Body.String(), "private words") != isAuthorized {
				t.Fatal("history did not remain behind board authorization")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("history could be cached")
			}
		})
	}
}

func TestVoiceHistoryReportsReadFailuresWithoutPrivateDetails(t *testing.T) {
	handler := NewHTTP(&Service{Instance: "current"}, "board.local", nil)
	handler.readVoice = func() (siqspeak.Snapshot, error) {
		return siqspeak.Snapshot{}, errors.New("private transcript and path")
	}
	request := httptest.NewRequest("POST", "http://board.local/api/voice", nil)
	request.Header.Set("Origin", "http://board.local")
	request.Header.Set("X-CFO-Token", "current")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != 503 || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("unsafe read failure: %d %s", response.Code, response.Body)
	}
}

func TestVoiceSnapshotUsesOnlyTheConfiguredInstallation(t *testing.T) {
	t.Setenv("CFO_SIQSPEAK_DIR", t.TempDir())
	t.Setenv("CFO_PROJECTS_ROOT", filepath.Join(t.TempDir(), "unreadable"))

	value, err := readVoiceSnapshot()

	if err != nil || value.State != "missing" || len(value.Entries) != 0 {
		t.Fatalf("configured directory = %+v, %v", value, err)
	}
	t.Setenv("CFO_SIQSPEAK_DIR", "relative/path")
	if _, err := readVoiceSnapshot(); err == nil {
		t.Fatal("invalid installation configuration was hidden")
	}
	t.Setenv("CFO_SIQSPEAK_DIR", "")
	if _, err := readVoiceSnapshot(); err == nil {
		t.Fatal("unreadable projects root was hidden")
	}
}
