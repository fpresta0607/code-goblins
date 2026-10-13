package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// browserSteps records, in order, each request to the supervisor and each
// handover of the terminal's browser session.
type browserSteps struct {
	steps    []string
	refuse   error
	handFail error
}

func (b *browserSteps) runtime(t *testing.T) commandRuntime {
	runtime := testCommandRuntime(t)
	runtime.requestBrowser = func(_ home.Home, request supervisor.BrowserRequest) (supervisor.BrowserLease, error) {
		b.steps = append(b.steps, request.Action+" "+request.Task+" "+request.Provider)
		if b.refuse != nil && request.Action == "attach" {
			return supervisor.BrowserLease{}, b.refuse
		}
		if request.Action == "release" {
			return supervisor.BrowserLease{}, nil
		}
		return supervisor.BrowserLease{Login: "stripe-precisiondocs-ai", Provider: "Stripe", Account: "PrecisionDocs-AI", URL: "http://127.0.0.1:9333"}, nil
	}
	runtime.handBrowser = func(_ context.Context, url string) error {
		b.steps = append(b.steps, "hand "+url)
		return b.handFail
	}
	return runtime
}

func TestAuthBrowserPointsTheTasksOwnSessionAtTheLentBrowser(t *testing.T) {
	// Arrange
	t.Setenv(spawn.BrowserSessionVariable, "pd-keys")
	recorded := &browserSteps{}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"auth", "browser", "stripe", "--task", "pd-keys"}, &stdout, &stderr, recorded.runtime(t))

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if want := []string{"attach pd-keys stripe", "hand http://127.0.0.1:9333"}; !slices.Equal(recorded.steps, want) {
		t.Fatalf("steps = %q, want %q", recorded.steps, want)
	}
	for _, want := range []string{"drives the Stripe login of PrecisionDocs-AI (stripe-precisiondocs-ai)", "reaches only Stripe's hosts", "never reveal or read a key", "cfo auth browser stripe --task pd-keys --release"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q does not say %q", stdout.String(), want)
		}
	}
}

func TestAuthBrowserReleaseLetsGoOfTheBrowserBeforeItCloses(t *testing.T) {
	// Arrange
	t.Setenv(spawn.BrowserSessionVariable, "pd-keys")
	recorded := &browserSteps{}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"auth", "browser", "stripe", "--task", "pd-keys", "--release"}, &stdout, &stderr, recorded.runtime(t))

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if want := []string{"hand ", "release pd-keys stripe"}; !slices.Equal(recorded.steps, want) {
		t.Fatalf("steps = %q, want the session let go first, then the release", recorded.steps)
	}
}

func TestAuthBrowserSignInFilesACardWithoutLendingTheWindow(t *testing.T) {
	t.Setenv(spawn.BrowserSessionVariable, "")
	var stdout, stderr bytes.Buffer
	runtime := testCommandRuntime(t)
	runtime.requestBrowser = func(_ home.Home, request supervisor.BrowserRequest) (supervisor.BrowserLease, error) {
		if request.Action != "sign-in" || request.Task != "pd-keys" || request.Provider != "stripe" {
			t.Fatalf("request = %+v", request)
		}
		return supervisor.BrowserLease{Login: "stripe-precisiondocs-ai", Provider: "Stripe", Account: "PrecisionDocs-AI", SignIn: "signin-1234"}, nil
	}
	runtime.handBrowser = func(context.Context, string) error { t.Fatal("the sign-in window was lent to a task"); return nil }

	exit := runWithRuntime([]string{"auth", "browser", "stripe", "--task", "pd-keys", "--sign-in"}, &stdout, &stderr, runtime)

	if exit != 0 || !strings.Contains(stdout.String(), "sign-in card") {
		t.Fatalf("exit = %d, output = %s %s", exit, stdout.String(), stderr.String())
	}
}

func TestAuthBrowserIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session string
		args    []string
		refuse  error
		hand    error
		exit    int
		steps   []string
		want    string
	}{
		{"for a terminal whose session is not its task's", "", []string{"stripe", "--task", "pd-keys"}, nil, nil, 2, nil, `browser session is "", not task pd-keys's own`},
		{"for another task's session", "pd-other", []string{"stripe", "--task", "pd-keys"}, nil, nil, 2, nil, "nothing was lent"},
		{"without a task", "pd-keys", []string{"stripe"}, nil, nil, 2, nil, "--task names your own task's ID"},
		{"without a provider", "pd-keys", []string{"--task", "pd-keys"}, nil, nil, 2, nil, "usage: cfo auth browser"},
		{"when the supervisor refuses", "pd-keys", []string{"stripe", "--task", "pd-keys"}, errors.New("PrecisionDocs-AI's Stripe login is lent to pd-other; ask again in a minute"), nil, 1, []string{"attach pd-keys stripe"}, "lent to pd-other"},
		{"when the session cannot take it, which gives it back", "pd-keys", []string{"stripe", "--task", "pd-keys"}, nil, errors.New("chrome-devtools-axi start exited 1"), 1, []string{"attach pd-keys stripe", "hand http://127.0.0.1:9333", "release pd-keys stripe"}, "asked to close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			t.Setenv(spawn.BrowserSessionVariable, tc.session)
			recorded := &browserSteps{refuse: tc.refuse, handFail: tc.hand}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(append([]string{"auth", "browser"}, tc.args...), &stdout, &stderr, recorded.runtime(t))

			// Assert
			if exit != tc.exit || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit = %d, stderr = %q, want exit %d saying %q", exit, stderr.String(), tc.exit, tc.want)
			}
			if !slices.Equal(recorded.steps, tc.steps) {
				t.Fatalf("steps = %q, want %q", recorded.steps, tc.steps)
			}
		})
	}
}

// browserTool is a runner standing in for chrome-devtools-axi.
type browserTool struct {
	runs []execx.Request
}

func (b *browserTool) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	b.runs = append(b.runs, request)
	return execx.Result{}, nil
}

func TestHandingOverRestartsTheSessionsBridgeOnTheLentBrowser(t *testing.T) {
	// Arrange
	tool := &browserTool{}

	// Act
	err := handBrowserSession(tool)(context.Background(), "http://127.0.0.1:9333")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(tool.runs) != 2 || tool.runs[0].Args[0] != "stop" || tool.runs[1].Args[0] != "start" || tool.runs[0].Name != "chrome-devtools-axi" {
		t.Fatalf("runs = %+v, want chrome-devtools-axi stop then start", tool.runs)
	}
	if !slices.Contains(tool.runs[1].Env, "CHROME_DEVTOOLS_AXI_BROWSER_URL=http://127.0.0.1:9333") || slices.ContainsFunc(tool.runs[0].Env, func(entry string) bool { return strings.HasPrefix(entry, "CHROME_DEVTOOLS_AXI_BROWSER_URL=") }) {
		t.Fatalf("start env names the lent browser: %v, stop env: %v", slices.Contains(tool.runs[1].Env, "CHROME_DEVTOOLS_AXI_BROWSER_URL=http://127.0.0.1:9333"), tool.runs[0].Env)
	}
}

func TestLettingGoOnlyStopsTheSessionsBridge(t *testing.T) {
	// Arrange
	tool := &browserTool{}

	// Act
	err := handBrowserSession(tool)(context.Background(), "")

	// Assert
	if err != nil || len(tool.runs) != 1 || tool.runs[0].Args[0] != "stop" {
		t.Fatalf("runs = %+v (%v), want chrome-devtools-axi stop alone", tool.runs, err)
	}
}
