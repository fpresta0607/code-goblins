package proc

import (
	"os"
	"strings"
	"testing"
)

func TestEnvironmentReadsOwnedProcessWithoutChangingIt(t *testing.T) {
	t.Setenv("CFO_CONNECTION_ENV_TEST", "synthetic-value")
	entries, err := Environment(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry == "CFO_CONNECTION_ENV_TEST=synthetic-value" {
			found = true
		}
	}
	if !found {
		t.Fatal("known environment assignment missing")
	}
	if os.Getenv("CFO_CONNECTION_ENV_TEST") != "synthetic-value" {
		t.Fatal("read changed the environment")
	}
	_, err = Environment(-1)
	if err == nil || strings.Contains(err.Error(), "synthetic-value") {
		t.Fatal("invalid process did not return a safe error")
	}
}

// A sweep that reads every process pays for each read of another process's
// memory, so a process's directory, arguments and environment come through
// one handle, the environment in one read of the size its block records. A
// process that grew its environment after it started is still read whole.
func TestParametersAndEnvironmentReadAProcessThroughOneHandle(t *testing.T) {
	// Arrange
	t.Setenv("CFO_PARAMETERS_ENV_GROWN", strings.Repeat("x", 3*os.Getpagesize()))
	t.Setenv("CFO_PARAMETERS_ENV_LAST", "set-after-the-large-one")
	wantDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wantDirectoryOnly, wantArguments, err := Parameters(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}

	// Act
	directory, arguments, environment, err := ParametersAndEnvironment(os.Getpid())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if directory != wantDirectoryOnly || !strings.EqualFold(strings.TrimRight(directory, `\`), wantDirectory) {
		t.Errorf("directory = %q, want %q as Parameters reads it and as the process knows it (%q)", directory, wantDirectoryOnly, wantDirectory)
	}
	if strings.Join(arguments, "\x00") != strings.Join(wantArguments, "\x00") {
		t.Errorf("arguments = %q, want %q", arguments, wantArguments)
	}
	wanted := map[string]bool{"CFO_PARAMETERS_ENV_LAST=set-after-the-large-one": false, "CFO_PARAMETERS_ENV_GROWN=" + strings.Repeat("x", 3*os.Getpagesize()): false}
	for _, entry := range environment {
		if _, ok := wanted[entry]; ok {
			wanted[entry] = true
		}
	}
	for entry, found := range wanted {
		if !found {
			t.Errorf("the environment read lacks %.60s, of %d entries", entry, len(environment))
		}
	}
	if _, _, unreadable, err := ParametersAndEnvironment(-1); err == nil || unreadable != nil {
		t.Errorf("a process that does not exist read as %v, %v; want an error and no environment", unreadable, err)
	}
}
