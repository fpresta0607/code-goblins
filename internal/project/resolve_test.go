package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectsRoot builds a temp projects root holding the named directories. A
// name ending in "!" is created without a .git, so it exists and is not a
// checkout.
func projectsRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		bare := strings.TrimSuffix(name, "!")
		if err := os.MkdirAll(filepath.Join(root, bare), 0o755); err != nil {
			t.Fatal(err)
		}
		if bare == name {
			if err := os.Mkdir(filepath.Join(root, bare, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func fixedRoot(root string) func() (string, error) {
	return func() (string, error) { return root, nil }
}

func TestResolveBareNames(t *testing.T) {
	root := projectsRoot(t, "PrecisionDocs-AI", "SIQshift", "PocketPiggies", "PocketPiggies-staging", "Acme.com")

	for name, want := range map[string]string{
		"SIQshift":         "SIQshift",         // exact
		"siqshift":         "SIQshift",         // case-insensitive
		"precisiondocs":    "PrecisionDocs-AI", // the scope name people type
		"acme":             "Acme.com",
		"pocketpiggies":    "PocketPiggies", // a whole-name match beats a longer sibling
		"PrecisionDocs-AI": "PrecisionDocs-AI",
	} {
		got, err := Resolve(name, fixedRoot(root))
		if err != nil {
			t.Errorf("Resolve(%q): %v", name, err)
			continue
		}
		if got != filepath.Join(root, want) {
			t.Errorf("Resolve(%q) = %q, want %q", name, got, filepath.Join(root, want))
		}
	}
}

func TestResolveRefusesAnAmbiguousName(t *testing.T) {
	root := projectsRoot(t, "shop-api", "shop-web", "unrelated")

	_, err := Resolve("shop", fixedRoot(root))
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	for _, want := range []string{root, "shop-api", "shop-web"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "unrelated") {
		t.Errorf("error names a directory that did not match: %v", err)
	}
}

func TestResolveRefusesAnUnknownNameWithTheRootAndCandidates(t *testing.T) {
	root := projectsRoot(t, "SIQshift", "PocketPiggies", "notes!")

	// "siq" stops mid-word, so it is not the start of SIQshift's name.
	for _, name := range []string{"clock-in", "siq"} {
		_, err := Resolve(name, fixedRoot(root))
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("Resolve(%q) err = %v, want ErrUnknown", name, err)
		}
		for _, want := range []string{root, "SIQshift", "PocketPiggies"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not name %q: %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "notes") {
			t.Errorf("error offers a directory that is not a checkout: %v", err)
		}
	}
}

func TestResolveRefusesADirectoryWithoutGit(t *testing.T) {
	root := projectsRoot(t, "notes!")

	_, err := Resolve("notes", fixedRoot(root))
	if err == nil || !strings.Contains(err.Error(), "no .git") {
		t.Fatalf("err = %v, want a refusal naming the missing .git", err)
	}
	if errors.Is(err, ErrUnknown) || errors.Is(err, ErrRootUnset) {
		t.Errorf("a directory that exists must not read as an unknown name: %v", err)
	}
}

func TestResolveLeavesAPathExactlyAsGiven(t *testing.T) {
	root := projectsRoot(t, "demo")
	never := func() (string, error) {
		t.Error("a path consulted the projects root")
		return root, nil
	}

	for _, path := range []string{
		filepath.Join(root, "absent"), // not checked: what a path must be is the caller's rule
		"projects/demo",
		`projects\demo`,
		"./demo",
		".",
		"..",
	} {
		got, err := Resolve(path, never)
		if err != nil || got != path {
			t.Errorf("Resolve(%q) = %q, %v; want it unchanged", path, got, err)
		}
	}
}

func TestResolveNamesTheFixWhenTheRootIsUnset(t *testing.T) {
	for label, root := range map[string]func() (string, error){
		"empty": fixedRoot(""),
		"nil":   nil,
	} {
		_, err := Resolve("precisiondocs", root)
		if !errors.Is(err, ErrRootUnset) {
			t.Fatalf("%s: err = %v, want ErrRootUnset", label, err)
		}
		for _, want := range []string{"precisiondocs", "cfo install --projects-root"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error does not name %q: %v", label, want, err)
			}
		}
	}
}

func TestResolveReportsARootThatCannotBeRead(t *testing.T) {
	cause := errors.New("registry unreadable")
	_, err := Resolve("demo", func() (string, error) { return "", cause })
	if !errors.Is(err, cause) || errors.Is(err, ErrRootUnset) {
		t.Errorf("err = %v, want the read failure, not an unset root", err)
	}

	missing := filepath.Join(t.TempDir(), "gone")
	_, err = Resolve("demo", fixedRoot(missing))
	if err == nil || !strings.Contains(err.Error(), missing) || errors.Is(err, ErrUnknown) {
		t.Errorf("err = %v, want a refusal naming the unreadable root", err)
	}
}
