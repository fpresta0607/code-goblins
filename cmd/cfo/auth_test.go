package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// useFileStore points the credential store at a temporary directory, so a
// test never reads or writes the operator's real vault.
func useFileStore(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "credentials")
	t.Setenv(auth.StoreDirEnv, root)
	return root
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := runAuth(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestAuthStoreAndListKeepTwoProjectsApart(t *testing.T) {
	useFileStore(t)

	for _, args := range [][]string{
		{"store", "--project", "precisiondocs", "DATABASE_URL", "postgres://precisiondocs"},
		{"store", "--project", "clock-in", "DATABASE_URL", "postgres://clock-in"},
		{"store", "GITHUB_TOKEN", "gho_shared_value"},
	} {
		if code, _, stderr := runCLI(t, args...); code != 0 {
			t.Fatalf("cfo auth %v = %d: %s", args, code, stderr)
		}
	}

	code, stdout, stderr := runCLI(t, "list")
	if code != 0 {
		t.Fatalf("cfo auth list = %d: %s", code, stderr)
	}
	for _, want := range []string{"GITHUB_TOKEN", "clock-in/DATABASE_URL", "precisiondocs/DATABASE_URL"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("listing lacks %q:\n%s", want, stdout)
		}
	}
	// A listing is names only; a value in it would defeat the whole store.
	if strings.Contains(stdout, "postgres://") || strings.Contains(stdout, "gho_shared_value") {
		t.Fatalf("listing disclosed a credential:\n%s", stdout)
	}

	code, stdout, stderr = runCLI(t, "list", "--project", "clock-in")
	if code != 0 {
		t.Fatalf("cfo auth list --project = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "clock-in/DATABASE_URL") || strings.Contains(stdout, "precisiondocs") {
		t.Errorf("scoped listing = %q, want only clock-in", stdout)
	}
}

func TestAuthStoreRedactsWhatItConfirms(t *testing.T) {
	useFileStore(t)
	code, stdout, stderr := runCLI(t, "store", "--project", "precisiondocs", "STRIPE_SECRET_KEY", "sk_live_0123456789abcdef")
	if code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "sk_live_0123456789abcdef") {
		t.Fatalf("store confirmation disclosed the credential:\n%s", stdout)
	}
	if !strings.Contains(stdout, "precisiondocs/STRIPE_SECRET_KEY") {
		t.Errorf("confirmation = %q, want the scoped key named", stdout)
	}
}

func TestAuthCopyMovesASharedValueIntoAProjectScopeWithoutReEnteringIt(t *testing.T) {
	useFileStore(t)
	if code, _, stderr := runCLI(t, "store", "DATABASE_URL", "postgres://stored-before-namespacing"); code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}

	code, stdout, stderr := runCLI(t, "copy", "DATABASE_URL", "--to", "precisiondocs")
	if code != 0 {
		t.Fatalf("cfo auth copy = %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "postgres://stored-before-namespacing") {
		t.Fatalf("copy disclosed the credential:\n%s", stdout)
	}

	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := store.Get(auth.Scoped("precisiondocs", "DATABASE_URL"))
	if err != nil || !found || value != "postgres://stored-before-namespacing" {
		t.Fatalf("scoped value = (%q, %v, %v), want the shared value carried over", value, found, err)
	}
	// The shared entry stays: which project owns it is exactly the question
	// this command must not answer on the operator's behalf.
	if _, found, _ := store.Get(auth.Shared("DATABASE_URL")); !found {
		t.Error("copy removed the shared value it was only asked to copy")
	}
}

func TestAuthCopyRefusesWhatIsNotStored(t *testing.T) {
	useFileStore(t)
	code, _, stderr := runCLI(t, "copy", "ABSENT_TOKEN", "--to", "precisiondocs")
	if code == 0 {
		t.Fatal("cfo auth copy = 0 for a credential that is not stored")
	}
	if !strings.Contains(stderr, "ABSENT_TOKEN") {
		t.Errorf("stderr = %q, want the missing name reported", stderr)
	}
}

func TestAuthStoreRefusesAScopeThatCouldEscapeTheStore(t *testing.T) {
	useFileStore(t)
	if code, _, _ := runCLI(t, "store", "--project", "../escaped", "TOKEN_VALUE", "value"); code == 0 {
		t.Error("cfo auth store accepted a traversing project scope")
	}
	if code, _, _ := runCLI(t, "store", "--project", "precisiondocs", "bad name", "value"); code == 0 {
		t.Error("cfo auth store accepted a name that is not an environment variable")
	}
	// A checkout whose name contains dots or a space is not a traversal, and
	// the spawn path already reads and writes credentials under that scope.
	// Refusing it here would leave the operator unable to store the very
	// credential a preflight refusal tells them to store.
	for _, scope := range []string{"docs..example", "Retire 91"} {
		if code, _, stderr := runCLI(t, "store", "--project", scope, "TOKEN_VALUE", "value"); code != 0 {
			t.Fatalf("cfo auth store --project %q = %d: %s", scope, code, stderr)
		}
		code, stdout, stderr := runCLI(t, "list", "--project", scope)
		if code != 0 {
			t.Fatalf("cfo auth list --project %q = %d: %s", scope, code, stderr)
		}
		if !strings.Contains(stdout, scope+"/TOKEN_VALUE") {
			t.Errorf("listing = %q, want the credential stored under scope %q", stdout, scope)
		}
	}
}
