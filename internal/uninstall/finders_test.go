package uninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// recordingRunner answers every command with out and remembers each.
type recordingRunner struct {
	out   string
	asked [][]string
}

func (r *recordingRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.asked = append(r.asked, append([]string{request.Name}, request.Args...))
	return execx.Result{Stdout: []byte(r.out)}, nil
}

// testMachine is a machine whose profile and home are folders of the test's.
func testMachine(t *testing.T) Machine {
	t.Helper()
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	root := filepath.Join(profile, "AppData", "Local", "CodeGoblins")
	for _, folder := range []string{filepath.Join(root, "state"), filepath.Join(profile, "AppData", "Roaming"), filepath.Join(profile, "AppData", "Local", "Temp")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Machine{
		Home:         home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")},
		Profile:      profile,
		LocalAppData: filepath.Join(profile, "AppData", "Local"),
		AppData:      filepath.Join(profile, "AppData", "Roaming"),
		Temp:         filepath.Join(profile, "AppData", "Local", "Temp"),
		Commands:     &recordingRunner{},
		Install: install.Service{Root: root, HarnessDirs: map[string]string{
			"claude": filepath.Join(profile, ".claude"), "codex": filepath.Join(profile, ".codex"), "pi": filepath.Join(profile, ".pi", "agent"),
		}},
	}
}

func whats(items []Item) string {
	var out []string
	for _, item := range items {
		out = append(out, string(item.Action)+" "+item.What)
	}
	return strings.Join(out, "\n")
}

func doAll(t *testing.T, items []Item) {
	t.Helper()
	for _, item := range items {
		if item.do == nil {
			continue
		}
		if err := item.do(context.Background()); err != nil {
			t.Fatalf("%s: %v", item.What, err)
		}
	}
}

func TestFoldersFindsCodeGoblinsOwnFilesOutsideTheHomeAndNothingElse(t *testing.T) {
	// Arrange
	m := testMachine(t)
	for _, path := range []string{
		filepath.Join(m.LocalAppData, "cfo", "conpty", "OpenConsole.exe"),
		filepath.Join(m.AppData, "goblins-window.exe", "EBWebView", "Local State"),
		filepath.Join(m.Temp, "CodeGoblinsInstall.log"),
		filepath.Join(m.Temp, "code-goblins-1234", "SHA256SUMS"),
		filepath.Join(m.Temp, "Code Goblins{ABC}.png"),
		filepath.Join(m.Temp, "jiti", "extensions-cfo-native.291f97c5.mjs"),
		filepath.Join(m.Temp, "jiti", "someone-elses.mjs"),
		filepath.Join(m.Temp, "unrelated.log"),
	} {
		write(t, path, "x")
	}

	// Act
	items, err := m.folders(context.Background())
	doAll(t, items)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 {
		t.Errorf("found\n%s\nwant the six of Code Goblins'", whats(items))
	}
	for _, kept := range []string{filepath.Join(m.Temp, "jiti", "someone-elses.mjs"), filepath.Join(m.Temp, "unrelated.log")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s, which is not Code Goblins', was removed: %v", kept, err)
		}
	}
	if again, _ := m.folders(context.Background()); len(again) != 0 {
		t.Errorf("found again\n%s", whats(again))
	}
}

// fakeVault holds targets in memory.
type fakeVault struct{ targets []string }

func (v *fakeVault) Targets() ([]string, error) { return v.targets, nil }
func (v *fakeVault) Delete(target string) error {
	v.targets = slices.DeleteFunc(v.targets, func(t string) bool { return t == target })
	return nil
}

func TestCredentialsAreEveryOneCodeGoblinsStoredInTheVaultAndInFiles(t *testing.T) {
	// Arrange
	m := testMachine(t)
	t.Setenv("CFO_CREDENTIAL_DIR", "")
	vault := &fakeVault{targets: []string{"cfo:GITHUB_TOKEN", "cfo:demo/API_KEY", "cfo:.env-seen/demo/API_KEY"}}
	m.Vault = vault
	write(t, filepath.Join(m.Profile, ".cfo", "credentials", "OLD_TOKEN"), "x")

	// Act
	items, err := m.credentials(context.Background())
	doAll(t, items)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := whats(items); strings.Count(got, "a saved credential") != 2 || !strings.Contains(got, "a record of what a .env offered") || !strings.Contains(got, "saved credentials in files") {
		t.Errorf("found\n%s", got)
	}
	if len(vault.targets) != 0 {
		t.Errorf("the vault still holds %v", vault.targets)
	}
	if _, err := os.Stat(filepath.Join(m.Profile, ".cfo")); !os.IsNotExist(err) {
		t.Errorf("~\\.cfo is still there: %v", err)
	}
}

func TestConversationRecordsAreTheHomesAndNamedByWhoseTheyAre(t *testing.T) {
	// Arrange
	m := testMachine(t)
	worktrees := filepath.Join(m.Home.Root, "worktrees")
	claude := filepath.Join(m.Install.HarnessDirs["claude"], "projects")
	pi := filepath.Join(m.Install.HarnessDirs["pi"], "sessions")
	codex := filepath.Join(m.Install.HarnessDirs["codex"], "sessions", "2026", "10", "10")
	write(t, filepath.Join(claude, slug(m.Home.Root), "cfo.jsonl"), "{}")
	write(t, filepath.Join(claude, slug(filepath.Join(worktrees, "demo", "g1")), "g1.jsonl"), "{}")
	write(t, filepath.Join(claude, slug(filepath.Join(m.Profile, "source", "mine")), "mine.jsonl"), "{}")
	write(t, filepath.Join(claude, slug(m.Home.Root)+"Window", "other.jsonl"), "{}")
	write(t, filepath.Join(pi, piSlug(filepath.Join(worktrees, "demo", "g1")), "s.jsonl"), "{}")
	write(t, filepath.Join(pi, piSlug(filepath.Join(m.Profile, "source", "mine")), "s.jsonl"), "{}")
	meta := func(cwd string) string {
		data, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"cwd": cwd}})
		return string(data) + "\n{}\n"
	}
	write(t, filepath.Join(codex, "rollout-1.jsonl"), meta(filepath.Join(worktrees, "demo", "g1")))
	write(t, filepath.Join(codex, "rollout-2.jsonl"), meta(filepath.Join(m.Profile, "source", "mine")))

	// Act
	items, err := m.conversations()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	got := whats(items)
	for _, want := range []string{"Claude Code's record of the CFO's conversations", "Claude Code's record of demo-g1's conversations", "pi's record of demo-g1's conversations", "Codex's record of g1's conversations"} {
		if !strings.Contains(got, want) {
			t.Errorf("found\n%s\nwant %q", got, want)
		}
	}
	if len(items) != 4 {
		t.Errorf("found %d records, want the home's four and none of the person's:\n%s", len(items), got)
	}
}

func TestTrustEntriesForTheHomesFoldersGoAndTheRestStay(t *testing.T) {
	// Arrange
	m := testMachine(t)
	g1 := filepath.Join(m.Home.Root, "worktrees", "demo", "g1")
	mine := filepath.Join(m.Profile, "source", "mine")
	claudeFile := filepath.Join(m.Profile, ".claude.json")
	claude, _ := json.Marshal(map[string]any{"numStartups": 3, "projects": map[string]any{filepath.ToSlash(g1): map[string]bool{"hasTrustDialogAccepted": true}, filepath.ToSlash(mine): map[string]bool{"hasTrustDialogAccepted": true}}})
	write(t, claudeFile, string(claude))
	if err := os.MkdirAll(m.Install.HarnessDirs["claude"], 0o755); err != nil {
		t.Fatal(err)
	}
	codexFile := filepath.Join(m.Install.HarnessDirs["codex"], "config.toml")
	write(t, codexFile, "model = \"x\"\r\n\r\n[projects.'"+g1+"']\r\ntrust_level = \"trusted\"\r\n\r\n[projects.'"+mine+"']\r\ntrust_level = \"trusted\"\r\n")

	// Act
	items, err := m.trust()
	doAll(t, items)

	// Assert
	if err != nil || len(items) != 2 {
		t.Fatalf("found\n%s\n%v; want Claude Code's and Codex's entries", whats(items), err)
	}
	data, _ := os.ReadFile(claudeFile)
	if bytes.Contains(data, []byte("g1")) || !bytes.Contains(data, []byte("mine")) || !bytes.Contains(data, []byte("numStartups")) {
		t.Errorf(".claude.json = %s, want only the goblin's entry gone", data)
	}
	toml, _ := os.ReadFile(codexFile)
	if strings.Contains(string(toml), "g1") || !strings.Contains(string(toml), "mine") || !strings.Contains(string(toml), "model = \"x\"\r\n") {
		t.Errorf("config.toml = %q, want only the goblin's table gone", toml)
	}
	if again, _ := m.trust(); len(again) != 0 {
		t.Errorf("found again\n%s", whats(again))
	}
}

func TestADevDriveCodeGoblinsMadeGoesAsAdministratorAndOneThePersonHadKeepsAllButItsFolder(t *testing.T) {
	t.Run("made by Code Goblins", func(t *testing.T) {
		// Arrange
		m := testMachine(t)
		m.Commands = &recordingRunner{out: "0"}
		if err := home.WriteDevDriveConfig(m.Home.Root, home.DevDriveConfig{Root: `Q:\CodeGoblins`, VHD: `C:\DevDrives\CodeGoblins.vhdx`}); err != nil {
			t.Fatal(err)
		}

		// Act
		items, err := m.devDrive(context.Background())

		// Assert
		if err != nil || len(items) != 3 {
			t.Fatalf("found\n%s\n%v; want the drive, its task and its folder", whats(items), err)
		}
		for _, item := range items {
			if !item.Admin || !strings.Contains(item.Script, "$still") {
				t.Errorf("%s is not an administrator's part that reads itself back: %q", item.What, item.Script)
			}
		}
		if !strings.Contains(items[0].Script, "Dismount-DiskImage") || !strings.Contains(items[0].Script, "mountvol.exe") || !strings.Contains(items[1].Script, "Unregister-ScheduledTask") {
			t.Errorf("the scripts do not detach the drive, free its letter and remove its task:\n%s\n%s", items[0].Script, items[1].Script)
		}
		for _, item := range items {
			if strings.Contains(item.Script, "Set-MpPreference") || strings.Contains(item.Script, "Add-MpPreference") {
				t.Errorf("%s changes a Defender setting", item.What)
			}
		}
	})
	t.Run("one the person had", func(t *testing.T) {
		// Arrange
		m := testMachine(t)
		folder := filepath.Join(t.TempDir(), "CodeGoblins")
		write(t, filepath.Join(folder, "worktrees", "x"), "x")
		if err := home.WriteDevDriveConfig(m.Home.Root, home.DevDriveConfig{Root: folder}); err != nil {
			t.Fatal(err)
		}

		// Act
		items, err := m.devDrive(context.Background())

		// Assert
		if err != nil || len(items) != 1 || items[0].Admin || items[0].Where != folder {
			t.Fatalf("found\n%s\n%v; want only the Code Goblins folder on it", whats(items), err)
		}
	})
}

func TestToolsTheInstallFetchedAreLeftUnlessThePersonChoseToRemoveThem(t *testing.T) {
	// Arrange
	m := testMachine(t)
	runner := &recordingRunner{}
	m.Commands = runner
	t.Setenv(install.UserEnvFileVariable, filepath.Join(t.TempDir(), "env.json"))
	m.Install.Env = install.NewEnvStore(execx.OSRunner{})
	skill := filepath.Join(m.Profile, ".claude", "skills", "gh-axi")
	write(t, filepath.Join(skill, "SKILL.md"), "x")
	write(t, filepath.Join(m.AppData, "npm", "node_modules", "@openai", "codex", "package.json"), "{}")
	fetched, _ := json.Marshal(Fetched{Tools: []FetchedTool{{Name: "codex", Kind: "npm", Package: "@openai/codex"}, {Name: "gone", Kind: "npm", Package: "gone"}}, Skills: []string{skill}})
	write(t, filepath.Join(m.Home.State, FetchedFile), string(fetched))

	// Act
	left, leftErr := m.tools(context.Background(), Choices{})
	removed, removedErr := m.tools(context.Background(), Choices{RemoveTools: true})
	doAll(t, removed)

	// Assert
	if leftErr != nil || removedErr != nil {
		t.Fatal(leftErr, removedErr)
	}
	if got := whats(left); got != "leave Codex\nleave the skill gh-axi" {
		t.Errorf("by default the plan has\n%s\nwant the tool and its skill left", got)
	}
	if got := whats(removed); got != "remove Codex\nremove the skill gh-axi" {
		t.Errorf("with the choice the plan has\n%s", got)
	}
	if len(runner.asked) == 0 || strings.Join(runner.asked[len(runner.asked)-1], " ") != "npm.cmd uninstall --global @openai/codex" {
		t.Errorf("ran %v, want npm's own uninstall of the package last", runner.asked)
	}
	if _, err := os.Stat(skill); !os.IsNotExist(err) {
		t.Errorf("the skill is still there: %v", err)
	}
}

// The commands npm made for a tool, in the folder above its packages, are the
// tool's too, so the plan names each with it, whatever way the package names
// them, and names no command of another's.
func TestTheCommandsNpmMadeForAToolAreNamedWithIt(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     []string
	}{
		{name: "a bin of named programs", manifest: `{"name":"@openai/codex","bin":{"codex":"bin/codex.js","codex-tui":"bin/tui.js"}}`, want: []string{"codex", "codex.cmd", "codex.ps1", "codex-tui", "codex-tui.cmd", "codex-tui.ps1"}},
		{name: "a bin of one program, named for the package without its scope", manifest: `{"name":"@openai/codex","bin":"bin/codex.js"}`, want: []string{"codex", "codex.cmd", "codex.ps1"}},
		{name: "no bin", manifest: `{"name":"@openai/codex"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			m := testMachine(t)
			prefix := filepath.Join(m.AppData, "npm")
			write(t, filepath.Join(prefix, "node_modules", "@openai", "codex", "package.json"), tc.manifest)
			for _, name := range []string{"codex", "codex.cmd", "codex.ps1", "codex-tui", "codex-tui.cmd", "codex-tui.ps1", "other.cmd"} {
				write(t, filepath.Join(prefix, name), "x")
			}
			fetched, _ := json.Marshal(Fetched{Tools: []FetchedTool{{Name: "codex", Kind: "npm", Package: "@openai/codex"}}})
			write(t, filepath.Join(m.Home.State, FetchedFile), string(fetched))

			// Act
			items, err := m.tools(context.Background(), Choices{})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, item := range items[1:] {
				if item.Action != Leave || item.What != "Codex" {
					t.Errorf("named %s %s for a command of Codex", item.Action, item.What)
				}
				got = append(got, filepath.Base(item.Where))
			}
			if items[0].Where != filepath.Join(prefix, "node_modules", "@openai", "codex") || strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("named %s and the commands %v, want the package and %v", items[0].Where, got, tc.want)
			}
		})
	}
}

// npmUninstall is npm uninstalling a package as npm does on Windows: the
// package goes, while its scope's folder stays, and here its commands stay
// too, as they would were npm to miss them.
type npmUninstall struct{ root string }

func (n npmUninstall) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "npm.cmd" && len(request.Args) == 3 && request.Args[0] == "uninstall" {
		return execx.Result{}, os.RemoveAll(filepath.Join(n.root, filepath.FromSlash(request.Args[2])))
	}
	return execx.Result{ExitCode: 1}, nil
}

// Once npm has removed a tool, nothing of it stays: no command it made and
// no scope folder it emptied, while npm's own folder of packages does.
func TestRemovingAnNpmToolLeavesNoCommandAndNoEmptyScope(t *testing.T) {
	// Arrange
	m := testMachine(t)
	prefix := filepath.Join(m.AppData, "npm")
	root := filepath.Join(prefix, "node_modules")
	m.Commands = npmUninstall{root: root}
	write(t, filepath.Join(root, "@openai", "codex", "package.json"), `{"name":"@openai/codex","bin":{"codex":"bin/codex.js"}}`)
	write(t, filepath.Join(root, "lavish-axi", "package.json"), `{"name":"lavish-axi","bin":"dist/cli.mjs"}`)
	for _, name := range []string{"codex", "codex.cmd", "codex.ps1", "lavish-axi.cmd"} {
		write(t, filepath.Join(prefix, name), "x")
	}
	fetched, _ := json.Marshal(Fetched{Tools: []FetchedTool{{Name: "codex", Kind: "npm", Package: "@openai/codex"}, {Name: "lavish-axi", Kind: "npm", Package: "lavish-axi"}}})
	write(t, filepath.Join(m.Home.State, FetchedFile), string(fetched))
	items, err := m.tools(context.Background(), Choices{RemoveTools: true})
	if err != nil {
		t.Fatal(err)
	}

	// Act
	doAll(t, items)
	again, againErr := m.tools(context.Background(), Choices{RemoveTools: true})

	// Assert
	if againErr != nil || len(again) != 0 {
		t.Errorf("read back\n%s\n%v, want nothing of the tools", whats(again), againErr)
	}
	for _, gone := range []string{"codex", "codex.cmd", "codex.ps1", "lavish-axi.cmd", filepath.Join("node_modules", "@openai")} {
		if _, err := os.Stat(filepath.Join(prefix, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("npm's own folder of packages went: %v", err)
	}
}

// No word the uninstall says has a semicolon or a dash between its clauses.
func TestTheWordsHaveNoSemicolonAndNoDash(t *testing.T) {
	// Arrange
	plan := Plan{Home: `C:\Users\you\AppData\Local\CodeGoblins`, Kept: `C:\Users\you\Code Goblins kept`, Items: []Item{
		{Action: Stop, Group: GroupRunning, What: "the board"}, {Action: Stop, Group: GroupRunning, What: "the window"},
		{Action: Stop, Group: GroupRunning, What: "the CFO's terminal"}, {Action: Stop, Group: GroupRunning, What: "g1's terminal"},
		{Action: Keep, Group: GroupWork, What: "g1's work, 1 commit not pushed, 2 changed files"},
		{Action: Keep, Group: GroupFleetData, What: "your fleet data: briefs, backlog, memory and reports"},
		{Action: Remove, Group: GroupDevDrive, Admin: true, What: "the Dev Drive E:", Where: `C:\DevDrives\CodeGoblins.vhdx`},
		{Action: Remove, Group: GroupCredentials, What: "a saved credential", Where: "cfo:GITHUB_TOKEN"},
		{Action: Remove, Group: GroupHarnesses, What: "Claude Code's record of g1's conversations"},
		{Action: Remove, Group: GroupHarnesses, What: "the skill lavish"},
		{Action: Leave, Group: GroupTools, What: "Claude Code"}, {Action: Leave, Group: GroupTools, What: "Codex"},
		{Action: Remove, Group: GroupHome, What: "the app and its folder", Where: `C:\Users\you\AppData\Local\CodeGoblins`, Bytes: 3 << 30},
	}}
	var summary, dry bytes.Buffer

	// Act
	plan.WriteSummary(&summary)
	plan.WriteDryRun(&dry)
	var done []string
	for group := GroupRunning; group <= GroupHome; group++ {
		done = append(done, plan.GroupDone(group, plan.itemsWhere(Stop, inGroup(group))), plan.GroupDone(group, plan.itemsWhere(Remove, inGroup(group))))
	}

	// Assert
	words := summary.String() + dry.String() + strings.Join(done, "\n")
	if strings.ContainsAny(words, ";\u2014\u2013") {
		t.Errorf("the words hold a semicolon or a dash:\n%s", words)
	}
	for _, want := range []string{"It keeps, in " + plan.Kept, "Work not landed", "the board, the window, the CFO and 1 goblin", "Claude Code's record of g1's conversations", "It leaves the tools it installed", "Windows asks once for an administrator", "3.0 GB"} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("the summary does not say %q:\n%s", want, summary.String())
		}
	}
	if !strings.Contains(dry.String(), "Nothing was changed.") {
		t.Errorf("the dry run does not say nothing was changed:\n%s", dry.String())
	}
}

func TestTheKeptFolderNeverGoesOverOneThatIsThere(t *testing.T) {
	// Arrange
	profile := t.TempDir()
	write(t, filepath.Join(profile, KeptFolderName, "x"), "x")

	// Act
	kept, err := keptFolder(profile)

	// Assert
	if err != nil || kept != filepath.Join(profile, KeptFolderName+" 2") {
		t.Errorf("keptFolder = %q, %v; want the next name", kept, err)
	}
}
