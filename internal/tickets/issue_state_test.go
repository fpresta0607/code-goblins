package tickets

import (
	"fmt"
	"testing"
)

func TestIssueStateDistinguishesClosedMissingAndUnreadableIdentities(t *testing.T) {
	issuePath := "GET repos/" + northwind + "/issues/7"
	for _, test := range []struct {
		name, body, want string
		failures         map[string]int
		isError          bool
	}{
		{name: "open", body: fmt.Sprintf(`{"number":7,"html_url":"https://github.com/%s/issues/7","state":"open"}`, northwind), want: "open"},
		{name: "closed", body: fmt.Sprintf(`{"number":7,"html_url":"https://github.com/%s/issues/7","state":"closed"}`, northwind), want: "closed"},
		{name: "missing issue in visible repository", want: "missing"},
		{name: "invisible repository", failures: map[string]int{"GET repos/" + northwind: 404}, isError: true},
		{name: "gone issue", failures: map[string]int{issuePath: 410}, want: "missing"},
		{name: "outage", failures: map[string]int{issuePath: 503}, isError: true},
		{name: "bad json", body: "{", isError: true},
		{name: "empty state", body: fmt.Sprintf(`{"number":7,"html_url":"https://github.com/%s/issues/7"}`, northwind), isError: true},
		{name: "other issue", body: fmt.Sprintf(`{"number":8,"html_url":"https://github.com/%s/issues/8","state":"closed"}`, northwind), isError: true},
		{name: "redirected repository", body: `{"number":7,"html_url":"https://github.com/acme/other/issues/7","state":"closed"}`, isError: true},
		{name: "pull request", body: fmt.Sprintf(`{"number":7,"html_url":"https://github.com/%s/pull/7","state":"closed","pull_request":{"url":"x"}}`, northwind), want: "pull request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			commands := &issueGitHub{issues: map[int]string{}, failures: test.failures}
			if test.body != "" {
				commands.issues[7] = test.body
			}
			github := GitHub{Commands: commands}

			// Act
			state, err := github.IssueState(t.Context(), northwind, 7)

			// Assert
			if state != test.want || (err != nil) != test.isError {
				t.Fatalf("state=%q error=%v, want %q, error=%v", state, err, test.want, test.isError)
			}
			if len(commands.calls) > 2 {
				t.Fatalf("unbounded read: %v", commands.calls)
			}
			for _, call := range commands.calls {
				if call.Method != "GET" {
					t.Errorf("read wrote %v", call)
				}
			}
		})
	}
}
