package pathguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a user profile with an ssh key and a credential store, and a
// goblin's worktree, scratch folder and cache beside a folder it was not
// given.
type fixture struct {
	boundary                                   Boundary
	worktree, scratch, cache, outside, profile string
	sshKey, storedCredential, vaultEntry       string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{
		worktree: filepath.Join(root, "worktrees", "project", "task"),
		scratch:  filepath.Join(root, "scratch", "task"),
		cache:    filepath.Join(root, "caches", "go-build"),
		outside:  filepath.Join(root, "dev", "project"),
		profile:  filepath.Join(root, "profile"),
	}
	appData := filepath.Join(f.profile, "AppData", "Roaming")
	localAppData := filepath.Join(f.profile, "AppData", "Local")
	fileStore := filepath.Join(f.profile, ".cfo", "credentials")
	f.sshKey = filepath.Join(f.profile, ".ssh", "id_ed25519")
	f.storedCredential = filepath.Join(fileStore, "project", "GITHUB_TOKEN")
	f.vaultEntry = filepath.Join(appData, "Microsoft", "Credentials", "ABCDEF")
	for _, file := range []string{f.sshKey, f.storedCredential, f.vaultEntry, filepath.Join(f.worktree, "main.go"), filepath.Join(f.outside, "README.md")} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{f.scratch, f.cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	environment := map[string]string{"USERPROFILE": f.profile, "APPDATA": appData, "LOCALAPPDATA": localAppData, "TMP": f.scratch}
	f.boundary = Boundary{
		Writable: []string{f.worktree, f.scratch, f.cache},
		Secrets:  Secrets(f.profile, appData, localAppData, fileStore),
		Cwd:      f.worktree,
		UserHome: f.profile,
		Getenv:   func(name string) string { return environment[name] },
	}
	return f
}

func toolInput(t *testing.T, fields map[string]string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func gitBash(path string) string {
	slashed := filepath.ToSlash(path)
	return "/" + strings.ToLower(slashed[:1]) + slashed[2:]
}

func TestToolRefusalRefusesEachSecretAndOutsideWriteByPath(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, tool string
		input      map[string]string
		path       string
	}{
		{"read an ssh key", "Read", map[string]string{"file_path": f.sshKey}, f.sshKey},
		{"read the credential file store", "Read", map[string]string{"file_path": f.storedCredential}, f.storedCredential},
		{"read Credential Manager's files", "Read", map[string]string{"file_path": f.vaultEntry}, f.vaultEntry},
		{"write an ssh key", "Write", map[string]string{"file_path": f.sshKey}, f.sshKey},
		{"write outside the worktree", "Write", map[string]string{"file_path": filepath.Join(f.outside, "README.md")}, filepath.Join(f.outside, "README.md")},
		{"edit through a relative path out of the worktree", "Edit", map[string]string{"file_path": filepath.Join("..", "..", "..", "dev", "project", "README.md")}, filepath.Join(f.outside, "README.md")},
		{"edit a notebook outside", "NotebookEdit", map[string]string{"notebook_path": filepath.Join(f.outside, "a.ipynb")}, filepath.Join(f.outside, "a.ipynb")},
		{"glob the ssh folder", "Glob", map[string]string{"pattern": filepath.ToSlash(filepath.Dir(f.sshKey)) + "/*"}, filepath.Dir(f.sshKey)},
		{"glob a pattern under the ssh folder", "Glob", map[string]string{"path": f.profile, "pattern": ".ssh/*"}, filepath.Dir(f.sshKey)},
		{"grep the credential store", "Grep", map[string]string{"path": filepath.Dir(f.storedCredential)}, filepath.Dir(f.storedCredential)},
		{"cat an ssh key by ~", "Bash", map[string]string{"command": "cat ~/.ssh/id_ed25519"}, f.sshKey},
		{"cat an ssh key by its Git Bash path", "Bash", map[string]string{"command": "cat " + gitBash(f.sshKey)}, f.sshKey},
		{"read an ssh key by USERPROFILE", "PowerShell", map[string]string{"command": `Get-Content $env:USERPROFILE\.ssh\id_ed25519`}, f.sshKey},
		{"read the ssh folder after moving home", "Bash", map[string]string{"command": "cd ~ && cat .ssh/id_ed25519"}, ".ssh"},
		{"copy the ssh folder by its bare name after moving home", "Bash", map[string]string{"command": "cd ~ && cp -r .ssh ./keys"}, filepath.Dir(f.sshKey)},
		{"move into the ssh folder by its bare name", "Bash", map[string]string{"command": "cd ~; cd .ssh; cat id_ed25519"}, filepath.Dir(f.sshKey)},
		{"copy the ssh folder by its bare name after Set-Location", "PowerShell", map[string]string{"command": "Set-Location ~; Copy-Item -Recurse .ssh keys"}, filepath.Dir(f.sshKey)},
		{"copy the ssh folder by its bare name after pushd", "Bash", map[string]string{"command": "pushd ~ && cp -r .ssh ./keys"}, filepath.Dir(f.sshKey)},
		{"read an ssh key after an escaped double quote", "Bash", map[string]string{"command": `echo x\"; cat ~/.ssh/id_ed25519`}, f.sshKey},
		{"read an ssh key between escaped single quotes", "Bash", map[string]string{"command": `echo x\' ; cat ~/.ssh/id_ed25519 x\'`}, f.sshKey},
		{"list Credential Manager", "PowerShell", map[string]string{"command": "cmdkey /list"}, "cmdkey"},
		{"redirect outside", "Bash", map[string]string{"command": "echo hi > ../../../dev/project/out.txt"}, filepath.Join(f.outside, "out.txt")},
		{"append outside by descriptor", "Bash", map[string]string{"command": "go test ./... 2>>" + gitBash(filepath.Join(f.outside, "log.txt"))}, filepath.Join(f.outside, "log.txt")},
		{"copy out of the worktree", "Bash", map[string]string{"command": "cp main.go " + filepath.ToSlash(filepath.Join(f.outside, "main.go"))}, filepath.Join(f.outside, "main.go")},
		{"remove outside", "PowerShell", map[string]string{"command": "Remove-Item -Recurse -Force " + f.outside}, f.outside},
		{"set content outside", "PowerShell", map[string]string{"command": "Set-Content -Path '" + filepath.Join(f.outside, "x.txt") + "' -Value 1"}, filepath.Join(f.outside, "x.txt")},
		{"copy to a destination outside", "PowerShell", map[string]string{"command": "Copy-Item main.go -Destination " + f.outside}, f.outside},
		{"tee outside", "Bash", map[string]string{"command": "go vet ./... | tee " + filepath.ToSlash(filepath.Join(f.outside, "vet.txt"))}, filepath.Join(f.outside, "vet.txt")},
		{"monitor a command that writes outside", "Monitor", map[string]string{"command": "echo x >> " + filepath.Join(f.outside, "x")}, filepath.Join(f.outside, "x")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Act
			refusal := f.boundary.ToolRefusal(c.tool, toolInput(t, c.input))

			// Assert
			if refusal == "" {
				t.Fatalf("%s %v went on, want it refused", c.tool, c.input)
			}
			if !strings.Contains(strings.ToLower(refusal), strings.ToLower(c.path)) {
				t.Fatalf("refusal %q does not name %s", refusal, c.path)
			}
		})
	}
}

func TestToolRefusalLetsTheWorktreeScratchAndCachesBe(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, tool string
		input      map[string]string
	}{
		{"read in the worktree", "Read", map[string]string{"file_path": filepath.Join(f.worktree, "main.go")}},
		{"read a project file outside", "Read", map[string]string{"file_path": filepath.Join(f.outside, "README.md")}},
		{"write in the worktree", "Write", map[string]string{"file_path": filepath.Join(f.worktree, "new.go")}},
		{"edit by a relative path", "Edit", map[string]string{"file_path": "main.go"}},
		{"write in the scratch folder", "Write", map[string]string{"file_path": filepath.Join(f.scratch, "intent.md")}},
		{"write in a cache", "Write", map[string]string{"file_path": filepath.Join(f.cache, "ab", "cd")}},
		{"glob the worktree", "Glob", map[string]string{"pattern": "**/*.go"}},
		{"grep with no path", "Grep", map[string]string{"pattern": "ssh"}},
		{"a tool that names no path", "WebFetch", map[string]string{"url": "https://example.com/.ssh"}},
		{"run tests", "Bash", map[string]string{"command": "go test ./... 2>&1 | tee test.log"}},
		{"discard output", "Bash", map[string]string{"command": "go build ./... > /dev/null 2>&1"}},
		{"discard output in PowerShell", "PowerShell", map[string]string{"command": "go build ./... > $null"}},
		{"write to Git Bash's tmp", "Bash", map[string]string{"command": "echo hi > /tmp/note.txt"}},
		{"copy into the worktree from outside", "Bash", map[string]string{"command": "cp " + filepath.ToSlash(filepath.Join(f.outside, "README.md")) + " docs/"}},
		{"redirect into the scratch folder", "PowerShell", map[string]string{"command": "go test ./... *> '" + filepath.Join(f.scratch, "log.txt") + "'"}},
		{"a URL that names a .ssh folder", "Bash", map[string]string{"command": "curl -s https://example.com/.ssh/x"}},
		{"git in the worktree", "Bash", map[string]string{"command": "git status && git diff --stat"}},
		{"a folder of the worktree named .ssh", "Bash", map[string]string{"command": "ls .ssh && cd docs && cat README.md"}},
		{"an escaped quote in a message", "Bash", map[string]string{"command": `git commit -m "say \"hi\" to the team"`}},
		{"write in the worktree by its Windows path", "PowerShell", map[string]string{"command": `Copy-Item main.go ` + filepath.Join(f.worktree, "copy.go")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Act
			refusal := f.boundary.ToolRefusal(c.tool, toolInput(t, c.input))

			// Assert
			if refusal != "" {
				t.Fatalf("%s %v was refused: %s", c.tool, c.input, refusal)
			}
		})
	}
}

func TestToolRefusalFollowsALinkOutOfTheWorktree(t *testing.T) {
	// Arrange
	f := newFixture(t)
	link := filepath.Join(f.worktree, "keys")
	if err := os.Symlink(filepath.Dir(f.sshKey), link); err != nil {
		t.Skipf("this account cannot make a symbolic link: %v", err)
	}
	// Where the link leads is named in its long form, which a temporary
	// folder's short name, as a hosted runner's is, is not.
	sshKey, err := filepath.EvalSymlinks(f.sshKey)
	if err != nil {
		t.Fatal(err)
	}
	given := filepath.Join(link, "id_ed25519")

	// Act
	refusal := f.boundary.ToolRefusal("Read", toolInput(t, map[string]string{"file_path": given}))

	// Assert
	if !strings.Contains(strings.ToLower(refusal), strings.ToLower(given)) || !strings.Contains(strings.ToLower(refusal), strings.ToLower(sshKey)) {
		t.Fatalf("a read through a link to the ssh folder gave %q, want it refused naming %s and where it leads, %s", refusal, given, sshKey)
	}
}

func TestToolRefusalRefusesArgumentsItCannotRead(t *testing.T) {
	// Arrange
	f := newFixture(t)

	// Act
	refusal := f.boundary.ToolRefusal("Write", json.RawMessage(`{"file_path": 3}`))

	// Assert
	if refusal == "" {
		t.Fatal("a Write whose arguments could not be read went on")
	}
}
