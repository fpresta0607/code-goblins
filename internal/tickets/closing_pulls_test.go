package tickets

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestOpenClosingPullRequestsKeepsOnlyTheOpenOnes(t *testing.T) {
	// Arrange
	gh := &fakeGitHub{graphql: []string{`{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[
		{"url":"https://github.com/acme/widgets/pull/8","state":"OPEN"},
		{"url":"https://github.com/acme/widgets/pull/5","state":"MERGED"},
		{"url":"https://github.com/acme/other/pull/2","state":"OPEN"}]}}}}}`}}

	// Act
	open, err := GitHub{Commands: gh}.OpenClosingPullRequests(context.Background(), "acme/widgets", 7)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://github.com/acme/widgets/pull/8", "https://github.com/acme/other/pull/2"}; !slices.Equal(open, want) {
		t.Fatalf("open = %v, want %v", open, want)
	}
	call := gh.graphqlCalls()[0]
	for name, want := range map[string]string{"owner": "acme", "name": "widgets", "number": "7"} {
		if got, _ := argValue(call, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestOpenClosingPullRequestsNeverReadsAnUnclearAnswerAsNone(t *testing.T) {
	cases := []struct {
		name, answer, stderr, want string
		exitCode                   int
	}{
		{name: "missing issue", answer: `{"data":{"repository":{"issue":null}},"errors":[{"message":"Could not resolve to an Issue"}]}`, exitCode: 1, stderr: "gh: Could not resolve to an Issue", want: "Could not resolve to an Issue"},
		{name: "no issue in the answer", answer: `{"data":{"repository":{"issue":null}}}`, want: "did not say which pull requests close acme/widgets#7"},
		{name: "unreadable answer", answer: `not json`, want: "read the pull requests that close acme/widgets#7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &fakeGitHub{graphql: []string{tc.answer}, graphqlExitCode: tc.exitCode, graphqlStderr: tc.stderr}

			// Act
			open, err := GitHub{Commands: gh}.OpenClosingPullRequests(context.Background(), "acme/widgets", 7)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.want) || open != nil {
				t.Fatalf("open = %v, err = %v, want %q", open, err, tc.want)
			}
		})
	}
}
