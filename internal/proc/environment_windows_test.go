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
