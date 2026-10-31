package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestHelpReadsOnlyTheRequestedContractWithoutOpeningAHome(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		reference string
	}{
		{"command", []string{"help", "spawn"}, "docs/commands/spawn.md"},
		{"subcommand", []string{"help", "auth", "request"}, "docs/commands/auth/request.md"},
		{"subcommand without arguments", []string{"help", "gate", "tests-kept"}, "docs/commands/gate/tests-kept.md"},
		{"setup without arguments", []string{"help", "dictation", "setup"}, "docs/commands/dictation/setup.md"},
		{"dispatch", []string{"help", "contract", "dispatch"}, "docs/contract/dispatch.md"},
		{"authentication", []string{"help", "contract", "authentication"}, "docs/contract/authentication.md"},
		{"worktrees", []string{"help", "contract", "worktrees"}, "docs/contract/worktrees.md"},
		{"services", []string{"help", "contract", "services"}, "docs/contract/services.md"},
		{"supervision", []string{"help", "contract", "supervision"}, "docs/contract/supervision.md"},
		{"delivery", []string{"help", "contract", "delivery"}, "docs/contract/delivery.md"},
		{"decisions", []string{"help", "contract", "decisions"}, "docs/contract/decisions.md"},
		{"afk", []string{"help", "contract", "afk"}, "docs/contract/afk.md"},
		{"home", []string{"help", "contract", "home"}, "docs/contract/home.md"},
		{"root-issues", []string{"help", "contract", "root-issues"}, "docs/contract/root-issues.md"},
		{"ownership", []string{"help", "contract", "ownership"}, "docs/contract/ownership.md"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer
			want, err := fs.ReadFile(codegoblins.Contract, test.reference)
			if err != nil {
				t.Fatal(err)
			}
			args := test.args
			if len(args) == 4 && args[2] == "contract" && test.name != "ownership" {
				skill, err := fs.ReadFile(codegoblins.Skills, ".agents/skills/cfo-"+test.name+"/SKILL.md")
				if err != nil {
					t.Fatal(err)
				}
				var routes [][]string
				for _, command := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(string(skill), -1) {
					words := strings.Fields(command[1])
					if len(words) >= 3 && words[0] == "cfo" && words[1] == "help" && words[2] == "contract" {
						routes = append(routes, words[1:])
					}
				}
				if len(routes) != 1 {
					t.Fatalf("cfo-%s declares %d procedure routes, want one", test.name, len(routes))
				}
				args = routes[0]
			}
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Fatal("read-only reference help tried to resolve the live home")
				return home.Home{}, nil
			}}

			// Act
			code := runWithRuntime(args, &stdout, &stderr, runtime)

			// Assert
			if code != 0 || stderr.Len() != 0 || !bytes.Equal(stdout.Bytes(), want) {
				t.Fatalf("%v: exit %d, stderr %q, output %q; want %s bytes", args, code, stderr.String(), stdout.String(), test.reference)
			}
		})
	}
}

func TestHelpReturnsTheExactRequestedFixtureBytes(t *testing.T) {
	files := fstest.MapFS{
		"docs/commands/spawn.md":        {Data: []byte("spawn fixture\r\n")},
		"docs/commands/auth/request.md": {Data: []byte("request fixture\n")},
		"docs/contract/dispatch.md":     {Data: []byte("dispatch fixture\x00\n")},
		"docs/contract/supervision.md":  {Data: []byte{}},
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"spawn"}, "spawn fixture\r\n"},
		{[]string{"auth", "request"}, "request fixture\n"},
		{[]string{"contract", "dispatch"}, "dispatch fixture\x00\n"},
		{[]string{"contract", "supervision"}, ""},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runHelp(test.args, &stdout, &stderr, files)
			if code != 0 || stderr.Len() != 0 || stdout.String() != test.want {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exactly %q", code, stdout.String(), stderr.String(), test.want)
			}
		})
	}
}

func TestHelpRejectsUnknownAndMalformedTopicsWithoutDumpingTheContract(t *testing.T) {
	for _, args := range [][]string{
		{"help", "not-a-command"}, {"help", "auth", "not-a-command"},
		{"help", "contract", "missing"}, {"help", "../AGENTS"},
		{"help", "spawn", "--yolo"}, {"help", "SPAWN"},
	} {
		// Arrange
		var stdout, stderr bytes.Buffer

		// Act
		code := runWithRuntime(args, &stdout, &stderr, commandRuntime{})

		// Assert
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown help topic") {
			t.Errorf("help %v: exit %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
}

type helpIndexFS struct {
	fs.FS
}

func (references helpIndexFS) Open(name string) (fs.File, error) {
	if strings.HasSuffix(name, ".md") {
		if _, err := fs.Stat(references.FS, name); err != nil {
			return nil, err
		}
		return nil, fs.ErrPermission
	}
	return references.FS.Open(name)
}

func TestHelpListsTopicsWithoutLoadingTheirBodies(t *testing.T) {
	files := helpIndexFS{fstest.MapFS{
		"docs/commands/auth/request.md": {},
		"docs/commands/pr/check.md":     {},
		"docs/commands/pr/merge.md":     {},
		"docs/contract/dispatch.md":     {},
		"docs/contract/supervision.md":  {},
		"docs/load-map.md":              {},
	}}
	for _, test := range []struct {
		name   string
		args   []string
		topics string
	}{
		{"all", nil, "auth request\n  pr check\n  pr merge\n  contract dispatch\n  contract supervision"},
		{"procedures", []string{"contract"}, "contract dispatch\n  contract supervision"},
		{"commands", []string{"pr"}, "pr check\n  pr merge"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runHelp(test.args, &stdout, &stderr, files)
			want := "usage: cfo help <topic>\n\nAvailable topics:\n  " + test.topics + "\n"
			if code != 0 || stderr.Len() != 0 || stdout.String() != want {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exactly %q", code, stdout.String(), stderr.String(), want)
			}
		})
	}
}

func TestHelpReportsReferenceAndOutputFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		args   []string
		files  fs.FS
		output io.Writer
		want   string
	}{
		{"missing index", nil, fstest.MapFS{}, io.Discard, "list docs"},
		{"unreadable body", []string{"spawn"}, fstest.MapFS{"docs/commands/spawn.md": {Mode: fs.ModeDir}}, io.Discard, "read docs/commands/spawn"},
		{"body output", []string{"spawn"}, fstest.MapFS{"docs/commands/spawn.md": {Data: []byte("contract")}}, gateOutputWriter(func([]byte) (int, error) { return 0, errors.New("output closed") }), "write docs/commands/spawn"},
		{"index output", nil, fstest.MapFS{"docs/commands/spawn.md": {Data: []byte("contract")}}, gateOutputWriter(func([]byte) (int, error) { return 0, errors.New("output closed") }), "write topics"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			var stderr bytes.Buffer

			// Act
			code := runHelp(test.args, test.output, &stderr, test.files)

			// Assert
			if code != 1 || !strings.Contains(stderr.String(), test.want) {
				t.Errorf("exit %d, stderr %q; want reference failure naming %q", code, stderr.String(), test.want)
			}
		})
	}
}
