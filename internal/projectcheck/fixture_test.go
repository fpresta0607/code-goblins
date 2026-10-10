package projectcheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fixture is one project as a check reads it: a checkout with a commit, and
// the data folder of a home that holds the project's own folder.
type fixture struct {
	t        *testing.T
	checkout string
	data     string
}

// newFixture commits tracked into a new repository named northwind and
// returns it with an empty data folder beside it.
func newFixture(t *testing.T, tracked map[string]string) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{t: t, checkout: filepath.Join(root, "northwind"), data: filepath.Join(root, "data")}
	for path, content := range tracked {
		f.write(path, content)
	}
	if len(tracked) == 0 {
		f.write("README.md", "northwind\n")
	}
	f.git("init", "--quiet", "--initial-branch=main")
	f.git("add", "--all")
	f.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "--message", "first")
	return f
}

// write puts a file in the checkout without committing it.
func (f fixture) write(path, content string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.checkout, filepath.FromSlash(path)), content)
}

// manifest puts one of the project's files in the home's data folder.
func (f fixture) manifest(name, content string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.data, "projects", "northwind", name), content)
}

func (f fixture) git(args ...string) {
	f.t.Helper()
	command := exec.Command("git", args...)
	command.Dir = f.checkout
	if out, err := command.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// commit commits everything the checkout holds.
func (f fixture) commit(message string) {
	f.t.Helper()
	f.git("add", "--all")
	f.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "--message", message)
}

// lag makes the commit the folder is on the default branch as last fetched,
// then steps the folder one commit back, which is a checkout that was fetched
// and never pulled.
func (f fixture) lag() {
	f.t.Helper()
	f.git("update-ref", "refs/remotes/origin/main", "HEAD")
	f.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	f.git("reset", "--quiet", "--hard", "HEAD~1")
}

// check runs the whole assessment with the programs named in onPath found
// and every other program missing.
func (f fixture) check(onPath ...string) Report {
	f.t.Helper()
	report, err := Check(context.Background(), Options{
		DataDir:  f.data,
		Checkout: f.checkout,
		Runner:   execx.OSRunner{},
		LookPath: func(name string) (string, error) {
			for _, known := range onPath {
				if known == name {
					return filepath.Join("bin", name), nil
				}
			}
			return "", exec.ErrNotFound
		},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return report
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// only returns the report's lines for one check, failing unless there is
// exactly one.
func only(t *testing.T, report Report, check string) Finding {
	t.Helper()
	var found []Finding
	for _, line := range report.Lines {
		if line.Check == check {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the report has %d %s lines, want 1:\n%s", len(found), check, report.Text())
	}
	return found[0]
}

// none fails when the report has a line for check.
func none(t *testing.T, report Report, check string) {
	t.Helper()
	for _, line := range report.Lines {
		if line.Check == check {
			t.Fatalf("the report has a %s line, want none: %s", check, line.Text())
		}
	}
}

func contains(t *testing.T, what, text string, wanted ...string) {
	t.Helper()
	for _, want := range wanted {
		if !strings.Contains(text, want) {
			t.Errorf("%s = %q, want it to name %q", what, text, want)
		}
	}
}
