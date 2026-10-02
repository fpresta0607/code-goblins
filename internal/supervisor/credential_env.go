package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// envFileName matches a project's local environment files by name: .env,
// .env.<name> and <name>.env.
var envFileName = regexp.MustCompile(`^(\.env(\.[A-Za-z0-9_-]+)*|[A-Za-z0-9_-][A-Za-z0-9_.-]*\.env)$`)

// plainEnvValue is a value an env file holds as it is, with no quotes.
var plainEnvValue = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,=-]+$`)

// EnvFileProblem refuses an env file target unless it is one of a project's
// own local environment files: an env file name at the root of the project's
// main checkout, never a goblin's worktree, where the project's environment
// lives and where worktrees share it from; a plain file or one not made yet;
// and a file git ignores and does not track, so a value written there can
// never be committed. A request's target is checked when it is filed, when
// the board takes it, and again before each write.
func EnvFileProblem(ctx context.Context, repository, name string) error {
	if repository == "" {
		return errors.New("an env file needs the project's checkout on this machine")
	}
	if err := envFileNameProblem(name); err != nil {
		return err
	}
	out, _, err := gitExit(ctx, repository, "rev-parse", "--show-prefix", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("%s is not a git checkout: %w", repository, err)
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
	switch {
	case len(lines) != 3:
		return fmt.Errorf("git could not say whether %s is a checkout", repository)
	case lines[0] != "":
		return fmt.Errorf("%s is a folder inside a checkout, not the root of the project's main checkout", repository)
	case !strings.EqualFold(filepath.Clean(lines[1]), filepath.Clean(lines[2])):
		return fmt.Errorf("%s is a goblin's worktree, not the project's main checkout", repository)
	}
	if info, err := os.Lstat(filepath.Join(repository, name)); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a plain file, such as a link or a folder, so nothing is written through it", name)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, code, err := gitExit(ctx, repository, "ls-files", "--error-unmatch", "--", name); err != nil {
		return err
	} else if code == 0 {
		return fmt.Errorf("%s is tracked by git, so a value written there would be committed", name)
	}
	if _, code, err := gitExit(ctx, repository, "check-ignore", "-q", "--", name); err != nil {
		return err
	} else if code == 1 {
		return fmt.Errorf("%s is not ignored by git, so a value written there could be committed", name)
	}
	return nil
}

// envFileNameProblem refuses a name that is not an env file name at the root
// of a checkout.
func envFileNameProblem(name string) error {
	switch {
	case name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, `/\:`) || filepath.Clean(name) != name:
		return fmt.Errorf("the env file %q must be a name at the root of the project's checkout", name)
	case !envFileName.MatchString(name):
		return fmt.Errorf("%q is not an env file name such as .env, .env.local or docker.env", name)
	}
	return nil
}

// gitExit runs git in dir and answers its output and exit code, since the
// checks read git's answer from the code: check-ignore and ls-files exit 1 to
// say no.
func gitExit(ctx context.Context, dir string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := execx.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0, nil
	case errors.As(err, &exit) && ctx.Err() == nil && exit.ExitCode() == 1:
		return out.String(), 1, nil
	case errors.As(err, &exit) && ctx.Err() == nil:
		return "", exit.ExitCode(), fmt.Errorf("git %s exited %d", args[0], exit.ExitCode())
	default:
		return "", 0, fmt.Errorf("git %s: %w", args[0], err)
	}
}

// openPlainFile opens the plain file at path for an in-place rewrite, or makes
// it when nothing is there, and never follows a link: a file that is there
// must be the very file inspected, so a link put in its place between the
// check and the open is refused, and one put in place of a missing file makes
// the create fail.
func openPlainFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a plain file, so nothing is written through it", filepath.Base(path))
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if opened, err := file.Stat(); err != nil || !os.SameFile(before, opened) {
		file.Close()
		return nil, fmt.Errorf("%s changed while it was opened, so nothing is written to it", filepath.Base(path))
	}
	return file, nil
}

// envLine is the line that sets name to value in an env file: the value as it
// is when it is plain, and in single quotes otherwise, which the board's own
// env reader and docker compose both read literally. A value it cannot write
// that way, one with a single quote or a line break, is refused.
func envLine(name, value string) (string, error) {
	switch {
	case strings.ContainsFunc(value, unicode.IsControl):
		return "", fmt.Errorf("%s holds a line break or another control character, so it is not written to an env file", name)
	case plainEnvValue.MatchString(value):
		return name + "=" + value, nil
	case !strings.Contains(value, "'"):
		return name + "='" + value + "'", nil
	}
	return "", fmt.Errorf("%s holds a single quote, which an env file cannot hold literally, so it is not written there", name)
}

// writeEnvFile sets each name's value in a request's env file once the file
// passes its checks again, and answers the names it set and, for those it
// did not, why, naming names only.
func writeEnvFile(request CredentialRequest, values map[string]string, names []string) ([]string, string) {
	if err := EnvFileProblem(context.Background(), request.Repository, request.EnvFile); err != nil {
		return nil, "not written to " + request.EnvFile + ": " + bounded(err.Error(), 300)
	}
	path := filepath.Join(request.Repository, request.EnvFile)
	var written, problems []string
	for _, name := range names {
		value, ok := values[name]
		if !ok {
			problems = append(problems, name+" could not be read back from the credential store")
			continue
		}
		if err := setEnvLine(path, name, value); err != nil {
			problems = append(problems, bounded(err.Error(), 300))
			continue
		}
		written = append(written, name)
	}
	if len(problems) > 0 {
		return written, "not written to " + request.EnvFile + ": " + strings.Join(problems, "; ")
	}
	return written, ""
}

// setsName reports whether an env file line sets name.
func setsName(line, name string) bool {
	key, _, found := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
	return found && strings.TrimSpace(key) == name
}

// setEnvLine sets name to value in the env file at path, making the file if
// it is not there. Every line that sets name is replaced, keeping an export
// prefix, and nothing else changes, line endings included. The file is
// rewritten in place, never replaced, because each goblin worktree shares a
// project's env files as hardlinks to the very same file.
func setEnvLine(path, name, value string) error {
	line, err := envLine(name, value)
	if err != nil {
		return err
	}
	file, err := openPlainFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	ending := "\n"
	if bytes.Contains(content, []byte("\r\n")) {
		ending = "\r\n"
	}
	var lines []string
	if text := strings.ReplaceAll(string(content), "\r\n", "\n"); text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	found := false
	for i, existing := range lines {
		if setsName(existing, name) {
			lines[i], found = line, true
			if strings.HasPrefix(strings.TrimSpace(existing), "export ") {
				lines[i] = "export " + line
			}
		}
	}
	if !found {
		lines = append(lines, line)
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.WriteAt([]byte(strings.Join(lines, ending)+ending), 0); err != nil {
		return err
	}
	return file.Sync()
}
