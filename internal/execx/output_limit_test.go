package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestOutputLimitHelper(t *testing.T) {
	if os.Getenv("EXECX_OUTPUT_HELPER") != "1" {
		return
	}
	stream := os.Stdout
	if os.Getenv("EXECX_OUTPUT_STREAM") == "stderr" {
		stream = os.Stderr
	}
	fmt.Fprint(stream, "0123456789")
	os.Exit(0)
}

func TestOSRunnerBoundsRequestedCapturedOutput(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		for _, limit := range []int{0, 9, 10} {
			t.Run(fmt.Sprintf("%s limit %d", stream, limit), func(t *testing.T) {
				request := Request{Name: os.Args[0], Args: []string{"-test.run=^TestOutputLimitHelper$"}, Env: []string{"EXECX_OUTPUT_HELPER=1", "EXECX_OUTPUT_STREAM=" + stream, "GOCOVERDIR=" + t.TempDir()}, OutputLimit: limit}
				result, err := (OSRunner{}).Run(context.Background(), request)
				if limit == 9 {
					if !errors.Is(err, ErrOutputLimit) || len(result.Stdout)+len(result.Stderr) != 0 {
						t.Fatalf("oversized output was reusable: %+v, %v", result, err)
					}
					return
				}
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("bounded command: %+v, %v", result, err)
				}
				output := result.Stdout
				if stream == "stderr" {
					output = result.Stderr
				}
				if string(output) != "0123456789" {
					t.Fatalf("complete output=%q", output)
				}
			})
		}
	}
}
