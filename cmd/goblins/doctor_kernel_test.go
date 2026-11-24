package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// goblins doctor says how many access tokens the kernel holds beside how many
// processes run, and how large its pools are, so tokens left behind by
// processes that ended show before they take the memory goblins need, and it
// says when the kernel could not be read rather than leaving the line out
// (#2526).
func TestRunDoctorSaysTheKernelsTokensAndPools(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kernel supervisor.Kernel
		err    error
		want   string
	}{
		{"read", supervisor.Kernel{Tokens: 655_000, Processes: 330, PagedPool: 6 << 30, NonpagedPool: 3 << 29}, nil, "kernel: 655000 access tokens alive while 330 processes run; paged pool 6.0 GB, nonpaged pool 1.5 GB\n"},
		{"unreadable", supervisor.Kernel{}, errors.New("access is denied"), "kernel: unreadable (access is denied)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			t.Setenv("GOBLINS_HOME", t.TempDir())
			t.Setenv("GOBLINS_STATE_OVERRIDE", "")
			runtime := runtimeOn(t, devdrive.Machine{})
			runtime.readKernel = func() (supervisor.Kernel, error) { return tc.kernel, tc.err }

			// Act
			var stdout, stderr bytes.Buffer
			runWithRuntime([]string{"doctor"}, &stdout, &stderr, runtime)

			// Assert
			if !strings.Contains(stdout.String(), tc.want) {
				t.Errorf("stdout lacks %q\n%s", tc.want, stdout.String())
			}
		})
	}
}
