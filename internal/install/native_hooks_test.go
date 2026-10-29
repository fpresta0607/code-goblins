package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// claudeEvents are the Claude Code events a fleet session reports its
// session through.
var claudeEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop"}

// isNativeHook reports whether hook runs cfo native-hook, which reports its
// session to the supervisor, rather than a cfo hook.
func isNativeHook(hook registeredHook) bool {
	return len(hook.Args) > 0 && hook.Args[0] == "native-hook"
}

// Every Claude Code session the fleet starts, a goblin's and the CFO's,
// carries its native session hooks in its own launch settings, so it reports
// its session as a Codex or Pi session does, and nothing is written to the
// user's settings, which every Claude Code session on the machine loads.
func TestEveryFleetClaudeSessionCarriesItsNativeSessionHooks(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings func(root, stateDir string) ([]byte, error)
	}{
		{"a goblin", GoblinSettings},
		{"the CFO", CFOSettings},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			stateDir := filepath.Join(root, "state")
			settings, err := test.settings(root, stateDir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, settings, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			hooks := slices.DeleteFunc(registeredHooks(t, path), func(hook registeredHook) bool { return !isNativeHook(hook) })

			// Assert
			var events []string
			for _, hook := range hooks {
				want := []string{"native-hook", "claude", "--home", root, "--state", stateDir}
				// A goblin's after a tool call raises the key alarm too.
				if test.name == "a goblin" && hook.Event == "PostToolUse" {
					want = append(want, "--key-alarm")
				}
				if hook.Command != filepath.Join(root, home.BinDir, "cfo.exe") || !slices.Equal(hook.Args, want) || hook.Shell || hook.Matcher != "" {
					t.Errorf("native hook = %+v, want the home's cfo.exe run with %q and no shell for every tool", hook, want)
				}
				events = append(events, hook.Event)
			}
			slices.Sort(events)
			wantEvents := slices.Sorted(slices.Values(claudeEvents))
			if !slices.Equal(events, wantEvents) {
				t.Fatalf("native hook events = %q, want each of %q once", events, wantEvents)
			}
		})
	}
}

// An install brings the board's pi extension up to its own build, since
// nothing else rewrites it: on 2026-10-24 one an install of 2026-10-16 wrote
// still ended its hook after 2.5 s and said "CFO native event could not be
// spooled" of an event that had reached the supervisor (#1502). One the
// Overlord never installed, or a file of his own there, is left as it is.
func TestInstallBringsTheBoardsPiExtensionUpToItsBuild(t *testing.T) {
	const olderBuild = "// CFO native hooks v1\n" +
		`    const result = spawnSync("C:\cfo.exe", ["native-hook","pi"], {input: JSON.stringify(payload), encoding: "utf8", windowsHide: true, timeout: 2500});` + "\n" +
		`    if (result.error || result.status !== 0) console.error("CFO native event could not be spooled");` + "\n"
	const ownExtension = "// the Overlord's own extension\n"
	for _, test := range []struct {
		name, before string
		isRefreshed  bool
	}{
		{"an older build's", olderBuild, true},
		{"none", "", false},
		{"the Overlord's own", ownExtension, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, "", nil)
			path := filepath.Join(f.profile, ".pi", "agent", "extensions", "cfo-native.ts")
			if test.before != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(test.before), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			current, err := nativehook.Install(nativehook.InstallConfig{Harness: "pi", ConfigDir: t.TempDir(), Executable: filepath.Join(f.bin, "cfo.exe"), Home: f.root, State: filepath.Join(f.root, "state")})
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(current)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			first := f.install()
			second := f.install()

			// Assert
			after, err := os.ReadFile(path)
			switch {
			case test.isRefreshed:
				if err != nil || string(after) != string(want) {
					t.Fatalf("pi extension after install = %q, %v; want this build's:\n%s", after, err, want)
				}
				if !strings.Contains(first, "brought the board's pi extension in "+filepath.Dir(filepath.Dir(path))+" up to this build") {
					t.Errorf("first install did not report the refresh:\n%s", first)
				}
			case test.before == "":
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("install wrote a pi extension the Overlord never installed: %q, %v", after, err)
				}
			default:
				if err != nil || string(after) != test.before {
					t.Fatalf("install replaced a pi extension it did not write: %q, %v", after, err)
				}
			}
			if strings.Contains(second, "pi extension") {
				t.Errorf("a second install changed the pi extension again:\n%s", second)
			}
		})
	}
}
