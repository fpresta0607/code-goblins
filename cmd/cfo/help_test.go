package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestHelpReadsOnlyTheRequestedContractWithoutOpeningAHome(t *testing.T) {
	executedSkillRoutes := 0
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
			if len(args) == 3 && args[1] == "contract" && test.name != "ownership" {
				skill, err := fs.ReadFile(codegoblins.Skills, ".agents/skills/cfo-"+test.name+"/SKILL.md")
				if err != nil {
					t.Fatal(err)
				}
				args, err = contractSkillRoute(string(skill), test.name)
				if err != nil {
					t.Fatal(err)
				}
				executedSkillRoutes++
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
	if executedSkillRoutes != 10 {
		t.Fatalf("executed %d declared skill routes, want all ten", executedSkillRoutes)
	}
}

func contractSkillRoute(skill, topic string) ([]string, error) {
	var routes [][]string
	for _, command := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(skill, -1) {
		words := strings.Fields(command[1])
		if len(words) >= 3 && slices.Equal(words[:3], []string{"cfo", "help", "contract"}) {
			routes = append(routes, words)
		}
	}
	if len(routes) != 1 {
		return nil, fmt.Errorf("cfo-%s declares %d procedure routes, want one", topic, len(routes))
	}
	if !slices.Equal(routes[0], []string{"cfo", "help", "contract", topic}) {
		return nil, fmt.Errorf("cfo-%s has incorrect procedure route %q", topic, routes[0])
	}
	return routes[0][1:], nil
}

func TestContractSkillRouteRejectsInvalidDeclarations(t *testing.T) {
	for _, test := range []struct {
		name  string
		skill string
		want  []string
	}{
		{"valid", "Read `cfo help contract delivery`.", []string{"help", "contract", "delivery"}},
		{"normalized spacing", "Read `cfo  help\tcontract delivery` or list `cfo help`.", []string{"help", "contract", "delivery"}},
		{"missing", "List `cfo help` for topics.", nil},
		{"duplicate", "`cfo help contract delivery` and `cfo help contract delivery`", nil},
		{"missing topic", "`cfo help contract`", nil},
		{"extra argument", "`cfo help contract delivery extra`", nil},
		{"malformed topic", "`cfo help contract ../delivery`", nil},
		{"unclosed command", "`cfo help contract delivery", nil},
		{"unknown topic", "`cfo help contract not-a-procedure`", nil},
		{"wrong topic", "`cfo help contract supervision`", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, err := contractSkillRoute(test.skill, "delivery")
			if test.want == nil {
				if err == nil || args != nil {
					t.Fatalf("invalid route returned %v, %v", args, err)
				}
			} else if err != nil || !slices.Equal(args, test.want) {
				t.Fatalf("route returned %v, %v; want %v", args, err, test.want)
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

func TestHelpListsOnlyTheRequestedCommandGroup(t *testing.T) {
	files := helpIndexFS{fstest.MapFS{
		"docs/commands/auth/request.md": {},
		"docs/commands/pr/check.md":     {},
		"docs/commands/pr/merge.md":     {},
		"docs/contract/dispatch.md":     {},
	}}
	var stdout, stderr bytes.Buffer
	code := runHelp([]string{"pr"}, &stdout, &stderr, files)
	want := "usage: cfo help <topic>\n\nAvailable topics:\n  pr check\n  pr merge\n"
	if code != 0 || stderr.Len() != 0 || stdout.String() != want {
		t.Fatalf("exit %d, stdout %q, stderr %q; want exactly %q", code, stdout.String(), stderr.String(), want)
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
