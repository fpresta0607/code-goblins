package services

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// scriptedDocker answers each docker call by its arguments.
type scriptedDocker struct {
	answers map[string]execx.Result
	calls   []execx.Request
}

func (d *scriptedDocker) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	d.calls = append(d.calls, request)
	return d.answers[strings.Join(request.Args, " ")], nil
}

func TestCLINamesTheStackByItsFilesFromTheCheckout(t *testing.T) {
	// Arrange
	compose := Compose{Dir: `C:\dev\PrecisionDocs-AI`, File: "docker-compose.dev.yml", EnvFile: ".env.docker.local"}
	runner := &scriptedDocker{answers: map[string]execx.Result{
		"compose --file docker-compose.dev.yml --env-file .env.docker.local ps --services --status running": {Stdout: []byte("redis\r\nbackend\r\n\r\n")},
	}}
	cli := CLI{Commands: runner}

	// Act
	running, runErr := cli.Running(context.Background(), compose)
	upErr := cli.Up(context.Background(), compose, []string{"backend", "worker-light"})
	downErr := cli.Down(context.Background(), compose, []string{"worker-light"})
	wholeErr := cli.Down(context.Background(), compose, nil)

	// Assert
	if err := errorsOf(runErr, upErr, downErr, wholeErr); err != "" {
		t.Fatal(err)
	}
	if !slices.Equal(running, []string{"redis", "backend"}) {
		t.Errorf("running = %q, want the services one per line", running)
	}
	want := []string{
		"compose --file docker-compose.dev.yml --env-file .env.docker.local ps --services --status running",
		"compose --file docker-compose.dev.yml --env-file .env.docker.local up --detach --wait --wait-timeout 900 backend worker-light",
		"compose --file docker-compose.dev.yml --env-file .env.docker.local down worker-light",
		"compose --file docker-compose.dev.yml --env-file .env.docker.local down",
	}
	for index, call := range runner.calls {
		if call.Name != "docker" || call.Dir != compose.Dir {
			t.Errorf("call %d = %s in %q, want docker in the checkout", index, call.Name, call.Dir)
		}
		if got := strings.Join(call.Args, " "); index < len(want) && got != want[index] {
			t.Errorf("call %d = %q, want %q", index, got, want[index])
		}
	}
	if len(runner.calls) != len(want) {
		t.Errorf("calls = %d, want %d", len(runner.calls), len(want))
	}
}

func TestCLILeavesTheEnvFileOutWhenNoneIsDeclared(t *testing.T) {
	runner := &scriptedDocker{}

	_ = CLI{Commands: runner}.Down(context.Background(), Compose{Dir: `C:\p`, File: "compose.yml"}, nil)

	if got := strings.Join(runner.calls[0].Args, " "); got != "compose --file compose.yml down" {
		t.Fatalf("args = %q, want no --env-file", got)
	}
}

func TestCLIStartsTheEngineAndChecksItAnswers(t *testing.T) {
	cases := []struct {
		name    string
		info    execx.Result
		wantErr string
	}{
		{"answers", execx.Result{Stdout: []byte("29.7.2\n")}, ""},
		{"started but silent", execx.Result{ExitCode: 1, Stderr: []byte("failed to connect to the docker API")}, "does not answer docker info"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			runner := &scriptedDocker{answers: map[string]execx.Result{"info --format {{.ServerVersion}}": tc.info}}

			// Act
			err := CLI{Commands: runner}.StartEngine(context.Background())

			// Assert
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if got := strings.Join(runner.calls[0].Args, " "); got != "desktop start --timeout 300" {
				t.Errorf("first call = %q, want Docker Desktop started and waited for", got)
			}
		})
	}
}

func TestCLIStopsTheEngineAndReadsWhatRuns(t *testing.T) {
	runner := &scriptedDocker{answers: map[string]execx.Result{"ps --format {{.Names}}": {Stdout: []byte("precisiondocs-dev-redis\n")}}}
	cli := CLI{Commands: runner}

	stopErr := cli.StopEngine(context.Background())
	containers, psErr := cli.RunningContainers(context.Background())

	if err := errorsOf(stopErr, psErr); err != "" {
		t.Fatal(err)
	}
	if got := strings.Join(runner.calls[0].Args, " "); got != "desktop stop --timeout 120" {
		t.Errorf("stop = %q", got)
	}
	if !slices.Equal(containers, []string{"precisiondocs-dev-redis"}) {
		t.Errorf("containers = %v", containers)
	}
}

func TestCLIReportsAFailedCallWithWhatDockerSaid(t *testing.T) {
	runner := &scriptedDocker{answers: map[string]execx.Result{
		"compose --file c.yml up --detach --wait --wait-timeout 900 backend": {ExitCode: 1, Stderr: []byte("container precisiondocs-dev-backend is unhealthy\n")},
	}}

	err := CLI{Commands: runner}.Up(context.Background(), Compose{Dir: `C:\p`, File: "c.yml"}, []string{"backend"})

	if err == nil || !strings.Contains(err.Error(), "exited 1") || !strings.Contains(err.Error(), "is unhealthy") {
		t.Fatalf("err = %v, want the exit and Docker's words", err)
	}
}

func TestCLIEngineRunningReadsAnEngineThatDoesNotAnswerAsStopped(t *testing.T) {
	runner := &scriptedDocker{answers: map[string]execx.Result{"info --format {{.ServerVersion}}": {ExitCode: 1}}}

	running, err := CLI{Commands: runner}.EngineRunning(context.Background())

	if err != nil || running {
		t.Fatalf("running = %v err = %v, want a stopped engine and no error", running, err)
	}
}

func errorsOf(errs ...error) string {
	var said []string
	for _, err := range errs {
		if err != nil {
			said = append(said, err.Error())
		}
	}
	return strings.Join(said, "\n")
}
