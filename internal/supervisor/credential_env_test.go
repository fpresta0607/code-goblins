package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// envRepository makes a project checkout whose .gitignore ignores .env*
// files but tracks .env.example, the shape real projects have, and returns
// it with a git runner for further setup.
func envRepository(t *testing.T) (string, func(dir string, args ...string)) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "precisiondocs")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(root, "init", "-q", "--initial-branch=main")
	git(root, "config", "user.email", "t@t")
	git(root, "config", "user.name", "t")
	for name, content := range map[string]string{".gitignore": ".env*\n!.env.example\n.worktrees/\n", ".env.example": "DATABASE_URL=\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(root, "add", ".gitignore", ".env.example")
	git(root, "commit", "-q", "-m", "start")
	return root, git
}

// A request may name only a project's own local environment file: an
// ignored, untracked .env file at the root of the project's main checkout,
// never a goblin's worktree, a tracked file, a nested path or a link.
func TestEnvFileTargetIsAnIgnoredUntrackedEnvFileOfTheMainCheckout(t *testing.T) {
	root, git := envRepository(t)
	worktree := filepath.Join(root, ".worktrees", "gb-billing")
	git(root, "worktree", "add", "-q", "-b", "billing", worktree)
	if err := os.WriteFile(filepath.Join(root, ".env.docker.local"), []byte("PORT=3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(root, "add", "-f", ".env.example")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n.env.*\n.worktrees/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, repository, file, want string
	}{
		{"an existing ignored file", root, ".env.docker.local", ""},
		{"an ignored file not made yet", root, ".env.local", ""},
		{"a name ending in .env", root, "docker.env", "not ignored"},
		{"a tracked file", root, ".env.example", "tracked"},
		{"a file git does not ignore", root, "local.env", "not ignored"},
		{"a nested path", root, "config/.env", "at the root"},
		{"a parent escape", root, "../.env", "at the root"},
		{"an absolute path", root, filepath.Join(root, ".env"), "at the root"},
		{"not an env file", root, "notes.txt", "env file"},
		{"no checkout", "", ".env", "checkout"},
		{"a goblin's worktree", worktree, ".env.local", "main checkout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			err := EnvFileProblem(context.Background(), test.repository, test.file)

			// Assert
			if test.want == "" && err != nil {
				t.Fatalf("EnvFileProblem(%q) = %v, want none", test.file, err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("EnvFileProblem(%q) = %v, want one saying %q", test.file, err, test.want)
			}
		})
	}
}

// An env file name that is a link is refused: a write through it would land
// outside the checkout.
func TestEnvFileTargetThatIsALinkIsRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows links")
	}
	root, _ := envRepository(t)
	elsewhere := t.TempDir()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, ".env.local"), elsewhere).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v\n%s", err, out)
	}

	// Act
	err := EnvFileProblem(context.Background(), root, ".env.local")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "not a plain file") {
		t.Fatalf("EnvFileProblem(junction) = %v, want it refused as not a plain file", err)
	}
}

// A value is written so the project's own env parser reads it back exactly:
// plain when it is plain, in single quotes otherwise, which docker compose
// also reads literally; a value it cannot write that way is refused.
func TestEnvLineReadsBackAsTheSameValue(t *testing.T) {
	dir := t.TempDir()
	for _, value := range []string{
		"rk" + "_test_51Habc",
		"postgres://app:pa$word@localhost:5432/app?sslmode=disable",
		"two words # and a hash",
		" padded ",
		`back\slash`,
	} {
		// Act
		line, err := envLine("DATABASE_URL", value)
		if err != nil {
			t.Fatalf("envLine(%q) = %v", value, err)
		}
		path := filepath.Join(dir, ".env")
		if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		values, err := auth.ParseEnvFile(path)

		// Assert
		if err != nil || values["DATABASE_URL"] != value {
			t.Fatalf("%q written as %q reads back as %q (%v)", value, line, values["DATABASE_URL"], err)
		}
	}
	for _, value := range []string{"it's", "one\nOTHER=two", "one\rtwo"} {
		if line, err := envLine("DATABASE_URL", value); err == nil {
			t.Fatalf("envLine(%q) = %q, want it refused", value, line)
		}
	}
}

// Setting a name changes its lines in the file itself and nothing else:
// comments, other names, an export prefix and the file's line endings stay, a
// name set twice is set in both places, and a hardlinked copy, as each goblin
// worktree has, sees the new value because the file is rewritten in place.
func TestSetEnvLineChangesOnlyItsLinesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.docker.local")
	original := "# local stack\r\nPORT=3000\r\nexport DATABASE_URL=old\r\nREDIS_URL=redis://localhost\r\nDATABASE_URL=older\r\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(dir, "worktree.env")
	if err := os.Link(path, shared); err != nil {
		t.Fatal(err)
	}

	// Act
	setErr := setEnvLine(path, "DATABASE_URL", "postgres://new")
	addErr := setEnvLine(path, "STRIPE_SECRET_KEY", "rk"+"_test_1")
	created := filepath.Join(dir, ".env.local")
	newErr := setEnvLine(created, "RESEND_API_KEY", "re"+"_test")

	// Assert
	if setErr != nil || addErr != nil || newErr != nil {
		t.Fatal(setErr, addErr, newErr)
	}
	want := "# local stack\r\nPORT=3000\r\nexport DATABASE_URL=postgres://new\r\nREDIS_URL=redis://localhost\r\nDATABASE_URL=postgres://new\r\nSTRIPE_SECRET_KEY=rk" + "_test_1\r\n"
	for _, file := range []string{path, shared} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Fatalf("%s = %q (%v), want %q", filepath.Base(file), got, err, want)
		}
	}
	if got, err := os.ReadFile(created); err != nil || string(got) != "RESEND_API_KEY=re"+"_test\n" {
		t.Fatalf("new file = %q (%v)", got, err)
	}
}

// A request names an env file only with the checkout it lives in, and only
// by an env file name at its root; the board checks git before taking it.
func TestCredentialRequestNamesAnEnvFileOnlyWithItsCheckout(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "precisiondocs")
	request := func(change func(*CredentialRequest)) CredentialRequest {
		r := CredentialRequest{Project: "precisiondocs", Repository: checkout, Names: []string{"DATABASE_URL"}, Why: "Local database", EnvFile: ".env.docker.local"}
		change(&r)
		return r
	}
	for _, test := range []struct {
		name   string
		change func(*CredentialRequest)
		want   string
	}{
		{"an env file in its checkout", func(*CredentialRequest) {}, ""},
		{"no env file", func(r *CredentialRequest) { r.EnvFile = "" }, ""},
		{"no checkout", func(r *CredentialRequest) { r.Repository = "" }, "checkout"},
		{"a nested path", func(r *CredentialRequest) { r.EnvFile = "config/.env" }, "root"},
		{"not an env file", func(r *CredentialRequest) { r.EnvFile = "notes.txt" }, "env file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			err := CredentialRequestProblem(request(test.change))

			// Assert
			if test.want == "" && err != nil {
				t.Fatalf("problem = %v, want none", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("problem = %v, want one saying %q", err, test.want)
			}
		})
	}
}
