package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// fetchRuntime answers each fetch with lease, or refuses it with refusal,
// and records the requests.
func fetchRuntime(t *testing.T, lease supervisor.BrowserLease, refusal error, asked *[]supervisor.BrowserRequest) commandRuntime {
	runtime := testCommandRuntime(t)
	runtime.requestBrowser = func(_ home.Home, request supervisor.BrowserRequest) (supervisor.BrowserLease, error) {
		*asked = append(*asked, request)
		return lease, refusal
	}
	return runtime
}

func TestAuthFetchAsksTheSupervisorAndPrintsOnlyTheNamesStored(t *testing.T) {
	// Arrange
	var asked []supervisor.BrowserRequest
	runtime := fetchRuntime(t, supervisor.BrowserLease{Login: "stripe-precisiondocs-ai", Provider: "Stripe", Account: "PrecisionDocs-AI", Fetched: []string{"STRIPE_TEST_PUBLISHABLE_KEY", "STRIPE_TEST_SECRET_KEY"}}, nil, &asked)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"auth", "fetch", "cred-0123456789abcdef", "--task", "pd-keys"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if len(asked) != 1 || asked[0] != (supervisor.BrowserRequest{Action: "fetch", Task: "pd-keys", Request: "cred-0123456789abcdef"}) {
		t.Fatalf("asked = %+v, want one fetch of the request for pd-keys", asked)
	}
	if want := "fetched STRIPE_TEST_PUBLISHABLE_KEY, STRIPE_TEST_SECRET_KEY from the Stripe key page into the vault, through the PrecisionDocs-AI login"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestAuthFetchSaysTheFetchWaitsForHisSignIn(t *testing.T) {
	// Arrange
	var asked []supervisor.BrowserRequest
	runtime := fetchRuntime(t, supervisor.BrowserLease{Login: "stripe-precisiondocs-ai", Provider: "Stripe", Account: "PrecisionDocs-AI", SignIn: "signin-0123"}, nil, &asked)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"auth", "fetch", "cred-0123456789abcdef", "--task", "pd-keys"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	for _, want := range []string{"asks for a sign-in", "(signin-0123)", "carries on by itself once he has signed in"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q does not say %q", stdout.String(), want)
		}
	}
}

func TestAuthFetchRefusesWithoutARequestOrItsOwnTask(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		exit int
	}{
		{"no request", []string{"auth", "fetch", "--task", "pd-keys"}, 2},
		{"no task", []string{"auth", "fetch", "cred-0123456789abcdef"}, 2},
		{"a task that is no task ID", []string{"auth", "fetch", "cred-0123456789abcdef", "--task", "../pd-keys"}, 2},
		{"an extra argument", []string{"auth", "fetch", "cred-0123456789abcdef", "--task", "pd-keys", "more"}, 2},
		{"the supervisor's refusal", []string{"auth", "fetch", "cred-0123456789abcdef", "--task", "pd-keys"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var asked []supervisor.BrowserRequest
			runtime := fetchRuntime(t, supervisor.BrowserLease{}, errors.New("the Stripe key page: the page changed"), &asked)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(test.args, &stdout, &stderr, runtime)

			// Assert
			if exit != test.exit || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, want %d and nothing printed", exit, stdout.String(), test.exit)
			}
			if (len(asked) == 1) != (test.exit == 1) {
				t.Fatalf("asked = %+v, want the supervisor asked only for a valid fetch", asked)
			}
		})
	}
}
