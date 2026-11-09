//go:build windows

package cutover

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// fakeEnv is the user's settings.
type fakeEnv struct {
	values     map[string]string
	broadcasts int
}

func (e *fakeEnv) Get(name string) (string, bool, error) {
	value, isSet := e.values[name]
	return value, isSet, nil
}
func (e *fakeEnv) Set(name, value string) error { e.values[name] = value; return nil }
func (e *fakeEnv) Unset(name string) error      { delete(e.values, name); return nil }
func (e *fakeEnv) Names() ([]string, error)     { return slices.Sorted(maps.Keys(e.values)), nil }
func (e *fakeEnv) Broadcast() error             { e.broadcasts++; return nil }

// fakeJobs is the Task Scheduler.
type fakeJobs map[string]string

func (j fakeJobs) Jobs() ([]Job, error) {
	var jobs []Job
	for _, name := range slices.Sorted(maps.Keys(j)) {
		jobs = append(jobs, Job{Name: name, XML: j[name]})
	}
	return jobs, nil
}
func (j fakeJobs) Register(job Job) error { j[job.Name] = job.XML; return nil }
func (j fakeJobs) Delete(name string) error {
	if _, isThere := j[name]; !isThere {
		return errors.New("no job " + name)
	}
	delete(j, name)
	return nil
}

// fakeVault is Credential Manager.
type fakeVault map[string]auth.VaultEntry

func (v fakeVault) Entries(prefix string) ([]auth.VaultEntry, error) {
	var entries []auth.VaultEntry
	for _, target := range slices.Sorted(maps.Keys(v)) {
		if strings.HasPrefix(target, prefix) {
			entries = append(entries, v[target])
		}
	}
	return entries, nil
}
func (v fakeVault) Write(entry auth.VaultEntry) error { v[entry.Target] = entry; return nil }
func (v fakeVault) Delete(target string) error        { delete(v, target); return nil }

// fakeLabels are GitHub's labels, by repository.
type fakeLabels map[string][]string

func (l fakeLabels) Labels(repository string) ([]string, error) { return l[repository], nil }
func (l fakeLabels) Rename(repository, from, to string) error {
	index := slices.Index(l[repository], from)
	if index < 0 {
		return errors.New("no label " + from)
	}
	l[repository][index] = to
	return nil
}

func junction(link, target string) error {
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return errors.New(string(out))
	}
	return nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// theOldBuilds respells what the new build's native hook install writes as a
// build before the rename wrote it: its program, its marker and its files.
var theOldBuilds = strings.NewReplacer(newName+".exe", oldName+".exe", "CEO native hooks", oldSpelling("CEO native hooks"), newName+"-native", oldName+"-native")

// asTheOldBuildWroteThem rewrites what the new build's native hook install
// wrote in folder as a build before the rename wrote it.
func asTheOldBuildWroteThem(t *testing.T, folder string) {
	t.Helper()
	var written []string
	err := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			written = append(written, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range written {
		content := read(t, path)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(filepath.Dir(path), theOldBuilds.Replace(filepath.Base(path))), theOldBuilds.Replace(content))
	}
}

// fixture is an installed home a build before the rename set up, and the
// machine around it, all under one temporary folder.
type fixture struct {
	machine Machine
	env     *fakeEnv
	jobs    fakeJobs
	vault   fakeVault
	labels  fakeLabels
	folders []string
}

const (
	skillText       = "---\nname: " + newName + "-afk\n---\nRun `" + newName + " afk on`.\n"
	lavishText      = "---\nname: lavish\n---\nRun `" + newName + " present` with the plan.\n"
	agentsText      = "# The CEO\n\nRun `" + newName + " spawn` for every task.\n"
	spawnPage       = "# " + newName + " spawn\n"
	lavishOwnerText = "Code Goblins installed this skill\r\nThe files it wrote:\r\nSKILL.md\r\n"
)

func newFixture(t *testing.T) fixture {
	t.Helper()
	top := t.TempDir()
	root, user, local, projects := filepath.Join(top, "home"), filepath.Join(top, "user"), filepath.Join(top, "local"), filepath.Join(top, "projects")
	state, data := filepath.Join(root, "state"), filepath.Join(root, "data")
	build := filepath.Join(top, "build")
	write(t, filepath.Join(build, newName+".exe"), "this build")
	write(t, filepath.Join(build, newName+"-hook.exe"), "this build's hook program")
	write(t, filepath.Join(build, "goblins-window.exe"), "this build's window")
	write(t, filepath.Join(root, "bin", oldName+".exe"), "the old build")
	write(t, filepath.Join(root, "bin", oldName+"-hook.exe"), "the old hook program")
	write(t, filepath.Join(root, "bin", "goblins.exe"), "the old build")
	write(t, filepath.Join(root, "bin", "goblins-window.exe"), "the old window")
	write(t, filepath.Join(root, "."+oldName+"-home"), "")
	write(t, filepath.Join(root, "AGENTS.md"), oldSpelling(agentsText))

	harnesses := harnessmap.Find(func(string) string { return "" }, user)
	for _, root := range harnesses.Roots {
		_, err := nativehook.Install(nativehook.InstallConfig{Harness: root.Harness, ConfigDir: root.Path, Executable: filepath.Join(filepath.Dir(state), "bin", newName+".exe"), Home: filepath.Dir(state), State: state})
		if err != nil {
			t.Fatal(err)
		}
		asTheOldBuildWroteThem(t, root.Path)
	}
	harnesses = harnessmap.Find(func(string) string { return "" }, user)
	claude, _ := harnesses.Harness("claude")
	settings := read(t, filepath.Join(claude.Path, "settings.json"))
	settings = strings.Replace(settings, `"hooks"`, `"permissions": {"allow": ["Bash(`+oldName+` question *)", "Bash(git status)"]},`+"\n  "+`"hooks"`, 1)
	write(t, filepath.Join(claude.Path, "settings.json"), settings)
	write(t, filepath.Join(claude.Path, "settings.json."+oldName+"-install.bak"), "{}\n")
	write(t, filepath.Join(claude.Path, "CLAUDE.md"), "Run "+oldName+" doctor first.\n")

	shared := filepath.Join(harnesses.SharedSkills, oldName+"-afk")
	write(t, filepath.Join(shared, "SKILL.md"), oldSpelling(skillText))
	write(t, filepath.Join(shared, "notes.md"), "a file the old build shipped and this one does not\n")
	write(t, filepath.Join(shared, ".code-goblins"), "Code Goblins installed this skill\r\nThe files it wrote:\r\nSKILL.md\r\nnotes.md\r\n")
	if err := os.MkdirAll(claude.Skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := junction(filepath.Join(claude.Skills, oldName+"-afk"), shared); err != nil {
		t.Fatal(err)
	}
	lavish := filepath.Join(harnesses.SharedSkills, "lavish")
	write(t, filepath.Join(lavish, "SKILL.md"), oldSpelling(lavishText))
	write(t, filepath.Join(lavish, ".code-goblins"), lavishOwnerText)
	if err := junction(filepath.Join(claude.Skills, "lavish"), lavish); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(harnesses.SharedSkills, "their-own", "SKILL.md"), "Run "+oldName+" spawn.\n")

	write(t, filepath.Join(state, oldName+"-engine.json"), `{"harness":"claude","problem":"The `+strings.ToUpper(oldName)+` started as claude"}`)
	write(t, filepath.Join(state, oldName+"-wake-skips.jsonl"), `{"skipped":"`+oldName+` spawn"}`+"\n")
	write(t, filepath.Join(state, "hosts", oldName+".json"), `{"host":"`+oldName+`","started_by_`+oldName+`":true,"pid":7}`)
	write(t, filepath.Join(state, "hosts", oldName+".log"), "the CEO's terminal\n")
	write(t, filepath.Join(state, "hosts", "cg-"+oldName+"-wakes.log"), "a task whose id keeps its words\n")
	write(t, filepath.Join(state, "cg-x.status"), "started: "+oldName+" spawn cg-x\n")
	write(t, filepath.Join(state, "cg-x.meta"), "brief="+filepath.Join(state, "tasktmp", "cg-x", "brief.md")+"\nstarted_by_"+oldName+"=true\n")
	write(t, filepath.Join(state, "tasktmp", "cg-x", "brief.md"), "# Brief\n\nRun `"+oldName+" gate test` with "+strings.ToUpper(oldName)+"_HOME cleared, then "+oldName+" notify cg-x --done.\n\nThe Overlord wrote: \"restart the "+oldName+" when "+oldName+" gate test hangs\"\n\n> "+oldName+" spawn is his to run\n\nTask cg-"+oldName+"-wakes and the skill "+oldName+"-afk stay named.\n")
	write(t, filepath.Join(state, "tasktmp", "cg-x", "delivery.json"), `{"fast":[["`+oldName+`","gate","test"]],"mode":"no-mistakes"}`)
	write(t, filepath.Join(state, "tasktmp", "cg-x", "run.log"), oldName+" gate test\n")
	write(t, filepath.Join(state, "tickets", "cg-x.json"), `{"task_id":"cg-x","repository":"owner/repo","labels":["`+oldName+`: running"]}`)
	write(t, filepath.Join(state, "ops", "watchdog.ps1"), "& \"$env:"+strings.ToUpper(oldName)+"_HOME\\bin\\"+oldName+".exe\" watch\n")
	write(t, filepath.Join(data, "backlog.md"), "| cg-x | run `"+oldName+" spawn cg-x` |\n| "+oldName+"-native-board | done |\n")
	write(t, filepath.Join(data, oldName+"-handoff-2026-11-05.md"), "The CFO handed off; "+oldName+" spawn later.\n")
	write(t, filepath.Join(data, "memory", "fleet.md"), "Use "+oldName+" gate turn --low-priority.\n")
	write(t, filepath.Join(data, "cg-x", "ticket.md"), "Run "+oldName+" status first.\n")
	write(t, filepath.Join(data, "overlord.md"), "\""+oldName+" spawn\" said he\n")
	write(t, filepath.Join(data, "archive", "finished", "cg-old", "brief.md"), "Run "+oldName+" gate test.\n")
	write(t, filepath.Join(data, oldName+"-native-board", "brief.md"), "Run "+oldName+" gate test.\n")
	write(t, filepath.Join(user, "."+oldName, "credentials", "x"), "kept\n")
	write(t, filepath.Join(local, oldName, "conpty", "OpenConsole.exe"), "a cache")

	other := filepath.Join(projects, "other-app")
	write(t, filepath.Join(other, "README.md"), "Gate with "+oldName+" gate test.\nNothing here.\nThe "+strings.ToUpper(oldName)+"_HOME setting.\n")
	write(t, filepath.Join(other, "untracked.md"), oldName+" gate test\n")
	write(t, filepath.Join(projects, "code-goblins", "cmd", oldName, "main.go"), "package main // "+oldName+" spawn\n")
	write(t, filepath.Join(projects, "code-goblins", "README.md"), oldName+" spawn\n")
	for _, checkout := range []string{other, filepath.Join(projects, "code-goblins")} {
		for _, args := range [][]string{{"init", "-q"}, {"add", "README.md"}} {
			if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
	}

	env := &fakeEnv{values: map[string]string{
		strings.ToUpper(oldName) + "_HOME":          root,
		strings.ToUpper(oldName) + "_PROJECTS_ROOT": projects,
		"PATH": filepath.Join(root, "bin"),
	}}
	jobs := fakeJobs{
		`\` + oldName + `-stall-watchdog`: `<Task><RegistrationInfo><URI>\` + oldName + `-stall-watchdog</URI></RegistrationInfo><Actions><Exec><Command>pythonw</Command><Arguments>` + filepath.Join(state, "ops", "stall-watchdog.py") + `</Arguments></Exec></Actions></Task>`,
		`\Code Goblins\Resume`:            `<Task><Actions><Exec><Command>` + filepath.Join(root, "bin", oldName+".exe") + `</Command><Arguments>resume</Arguments></Exec></Actions></Task>`,
		`\Unrelated`:                      `<Task><Actions><Exec><Command>notepad</Command></Exec></Actions></Task>`,
	}
	vault := fakeVault{
		oldName + ":GITHUB_TOKEN":   {Target: oldName + ":GITHUB_TOKEN", User: oldName, Value: []byte("token")},
		oldName + ":app/STRIPE_KEY": {Target: oldName + ":app/STRIPE_KEY", User: oldName, Value: []byte("key")},
		oldName + ":someone-else's": {Target: oldName + ":someone-else's", User: "another program", Value: []byte("theirs")},
	}
	labels := fakeLabels{"owner/repo": {oldName + ": running", oldName + ": queued", "bug"}}
	return fixture{
		machine: Machine{
			Root: root, UserHome: user, LocalAppData: local, Harnesses: harnesses, ProjectsRoot: projects,
			Program: filepath.Join(build, newName+".exe"),
			Skills: fstest.MapFS{
				newName + "-afk/SKILL.md": {Data: []byte(skillText)},
				"lavish/SKILL.md":         {Data: []byte(lavishText)},
			},
			Contract: fstest.MapFS{
				"AGENTS.md":              {Data: []byte(agentsText)},
				"docs/commands/spawn.md": {Data: []byte(spawnPage)},
			},
			Commands: []string{"afk", "doctor", "gate", "native-hook", "notify", "question", "resume", "spawn", "status", "watch"},
			Env:      env, Jobs: jobs, Vault: vault, Labels: labels, Link: junction,
			Git: func(dir string, args ...string) ([]byte, error) {
				return exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
			},
		},
		env: env, jobs: jobs, vault: vault, labels: labels,
		folders: []string{root, user, local, projects},
	}
}

// entry is what a snapshot holds of one path: a file's content and time, a
// link's target, or a folder.
type entry struct {
	content string
	modTime time.Time
	link    string
	isDir   bool
}

// snapshot is everything on the machine the cutover may change.
type snapshot struct {
	files  map[string]entry
	env    map[string]string
	jobs   map[string]string
	vault  map[string]string
	labels map[string]string
}

func (f fixture) snapshot(t *testing.T) snapshot {
	t.Helper()
	s := snapshot{files: map[string]entry{}, env: maps.Clone(f.env.values), jobs: maps.Clone(map[string]string(f.jobs)), vault: map[string]string{}, labels: map[string]string{}}
	for target, held := range f.vault {
		s.vault[target] = held.User + "=" + string(held.Value)
	}
	for repository, names := range f.labels {
		s.labels[repository] = strings.Join(names, ",")
	}
	for _, folder := range f.folders {
		err := filepath.WalkDir(folder, func(path string, item fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if item.Name() == ".git" {
				return filepath.SkipDir
			}
			if target, err := os.Readlink(path); err == nil {
				s.files[path] = entry{link: target}
				return filepath.SkipDir
			}
			if item.IsDir() {
				s.files[path] = entry{isDir: true}
				return nil
			}
			info, err := item.Info()
			if err != nil {
				return err
			}
			s.files[path] = entry{content: read(t, path), modTime: info.ModTime()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s snapshot) differences(other snapshot) []string {
	var found []string
	for _, path := range slices.Sorted(maps.Keys(s.files)) {
		if theirs, isThere := other.files[path]; !isThere {
			found = append(found, "gone: "+path)
		} else if theirs != s.files[path] {
			found = append(found, "changed: "+path)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(other.files)) {
		if _, isThere := s.files[path]; !isThere {
			found = append(found, "new: "+path)
		}
	}
	for name, pair := range map[string][2]map[string]string{"settings": {s.env, other.env}, "jobs": {s.jobs, other.jobs}, "credentials": {s.vault, other.vault}, "labels": {s.labels, other.labels}} {
		if !maps.Equal(pair[0], pair[1]) {
			found = append(found, name+" differ")
		}
	}
	return found
}

func TestTheDryRunListsEveryChangeAndChangesNothing(t *testing.T) {
	// Arrange
	f := newFixture(t)
	before := f.snapshot(t)

	// Act
	plan, err := f.machine.Plan()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the dry run changed the machine:\n%s", strings.Join(found, "\n"))
	}
	listing := strings.Join(plan.Listing(), "\n")
	t.Logf("the dry run on the test home:\n%s\nplan: %s", listing, plan.Digest())
	root, user := f.machine.Root, f.machine.UserHome
	shared := filepath.Join(user, ".agents", "skills")
	for _, want := range []string{
		KindProgram + ":", filepath.Join(root, "bin", newName+".exe") + ": this build", filepath.Join(root, "bin", oldName+".exe") + ": removed",
		filepath.Join(root, "bin", "goblins-window.exe") + ": this build",
		KindContract + ":", filepath.Join(root, "AGENTS.md") + ": this build's", filepath.Join(root, "docs", "commands", "spawn.md") + ": this build's",
		filepath.Join(shared, newName+"-afk", "SKILL.md") + ": this build's", filepath.Join(shared, newName+"-afk", "notes.md") + ": removed",
		filepath.Join(shared, "lavish", "SKILL.md") + ": this build's", filepath.Join(shared, "lavish", ".code-goblins") + ": this build's",
		filepath.Join(f.machine.LocalAppData, oldName) + ": the old build's caches outside the home, left for you to delete",
		KindSetting + ":", strings.ToUpper(oldName) + "_HOME=" + root + " becomes " + strings.ToUpper(newName) + "_HOME",
		KindHook + ":", filepath.Join(user, ".claude", "settings.json") + ": ", "renamed " + newName + "-native.ts", filepath.Join(root, "state", oldName+"-wake-skips.jsonl") + ": renamed",
		KindSkill + ":", filepath.Join(user, ".agents", "skills", oldName+"-afk") + ": becomes",
		KindJob + ":", `\` + oldName + `-stall-watchdog: becomes \` + newName + `-stall-watchdog`, `\Code Goblins\Resume: its definition rewritten`,
		KindCredential + ":", oldName + ":GITHUB_TOKEN becomes " + newName + ":GITHUB_TOKEN", filepath.Join(user, "."+oldName) + ": the credential file store's folder",
		KindState + ":", filepath.Join(root, "state", "hosts", oldName+".log") + ": renamed " + newName + ".log", filepath.Join(root, "."+oldName+"-home") + ": renamed",
		KindTask + ":", filepath.Join(root, "data", "backlog.md") + ": 1 line rewritten", filepath.Join(root, "state", "tasktmp", "cg-x", "delivery.json"),
		KindLabel + ":", `owner/repo: "` + oldName + `: running" becomes "` + newName + `: running"`,
		KindListed + ":", filepath.Join(f.machine.ProjectsRoot, "other-app", "README.md") + ": names the old command on line 1, 3",
		KindNote + ":", "the first-open layout and the quick tour once more",
	} {
		if !strings.Contains(listing, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, listing)
		}
	}
	for _, unwanted := range []string{"cg-" + oldName + "-wakes", "archive", "overlord.md", "cg-x.status", "run.log", "CLAUDE.md", "untracked.md", filepath.Join("code-goblins", "README.md"), oldName + "-native-board", "someone-else", "Unrelated", "their-own", filepath.Join(f.machine.LocalAppData, newName)} {
		if strings.Contains(listing, unwanted) {
			t.Errorf("the dry run names %q, which it never changes:\n%s", unwanted, listing)
		}
	}
	again, err := f.machine.Plan()
	if err != nil || again.Digest() != plan.Digest() {
		t.Fatalf("planned again, the digest is %s (%v), want %s", again.Digest(), err, plan.Digest())
	}
}

func TestTheCutoverLeavesTheHomeWorkingUnderTheNewName(t *testing.T) {
	// Arrange
	f := newFixture(t)
	plan, err := f.machine.Plan()
	if err != nil {
		t.Fatal(err)
	}
	root, user, state := f.machine.Root, f.machine.UserHome, filepath.Join(f.machine.Root, "state")
	untouched := map[string]string{}
	for _, path := range []string{
		filepath.Join(state, "cg-x.status"), filepath.Join(state, "tasktmp", "cg-x", "run.log"),
		filepath.Join(state, "hosts", "cg-"+oldName+"-wakes.log"), filepath.Join(root, "data", "overlord.md"),
		filepath.Join(root, "data", "archive", "finished", "cg-old", "brief.md"), filepath.Join(root, "data", oldName+"-native-board", "brief.md"),
		filepath.Join(user, ".claude", "CLAUDE.md"), filepath.Join(f.machine.ProjectsRoot, "other-app", "README.md"),
		filepath.Join(user, ".agents", "skills", "their-own", "SKILL.md"), filepath.Join(f.machine.LocalAppData, oldName, "conpty", "OpenConsole.exe"),
	} {
		untouched[path] = read(t, path)
	}

	// Act
	folder, err := f.machine.Apply(plan, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(folder, UndoFile)) {
		t.Fatalf("the apply kept no %s in %s", UndoFile, folder)
	}
	for path, content := range untouched {
		if read(t, path) != content {
			t.Errorf("%s changed, and it is recorded history or another's", path)
		}
	}
	bin := filepath.Join(root, "bin")
	for name, want := range map[string]string{newName + ".exe": "this build", "goblins.exe": "this build", newName + "-hook.exe": "this build's hook program", "goblins-window.exe": "this build's window"} {
		if got := read(t, filepath.Join(bin, name)); got != want {
			t.Errorf("bin\\%s is %q, want %q", name, got, want)
		}
	}
	for _, gone := range []string{filepath.Join(bin, oldName+".exe"), filepath.Join(bin, oldName+"-hook.exe"), filepath.Join(state, oldName+"-engine.json"), filepath.Join(user, ".agents", "skills", oldName+"-afk"), filepath.Join(user, ".claude", "skills", oldName+"-afk")} {
		if exists(gone) {
			t.Errorf("%s is still there", gone)
		}
	}
	if f.env.values[strings.ToUpper(newName)+"_HOME"] != root || f.env.values[strings.ToUpper(newName)+"_PROJECTS_ROOT"] != f.machine.ProjectsRoot || f.env.broadcasts != 1 {
		t.Errorf("settings %v, broadcast %d times", f.env.values, f.env.broadcasts)
	}
	if _, isSet := f.env.values[strings.ToUpper(oldName)+"_HOME"]; isSet {
		t.Errorf("the old home setting is still set: %v", f.env.values)
	}
	for _, root := range f.machine.Harnesses.Roots {
		if isInstalled, err := nativehook.Installed(root.Harness, root.Path); err != nil || !isInstalled {
			t.Errorf("%s's native hooks are not this build's: %v, %v", root.Harness, isInstalled, err)
		}
	}
	settings := read(t, filepath.Join(user, ".claude", "settings.json"))
	if !strings.Contains(settings, "Bash("+newName+" question *)") || strings.Contains(strings.ToLower(settings), oldName) {
		t.Errorf("Claude Code's settings still name the old command:\n%s", settings)
	}
	if !exists(filepath.Join(user, ".claude", "settings.json."+newName+"-install.bak")) {
		t.Errorf("the settings' backup was not renamed")
	}
	if problems := harnessmap.Check(f.machine.Harnesses); len(problems) > 0 {
		t.Errorf("the skills' check found %v", problems)
	}
	skill := filepath.Join(user, ".claude", "skills", newName+"-afk", "SKILL.md")
	if got := read(t, skill); got != skillText {
		t.Errorf("%s is %q, want this build's skill", skill, got)
	}
	if exists(filepath.Join(user, ".agents", "skills", newName+"-afk", "notes.md")) {
		t.Errorf("a file the old build shipped and this one does not is still in the skill")
	}
	watchdog, resume := f.jobs[`\`+newName+`-stall-watchdog`], f.jobs[`\Code Goblins\Resume`]
	if !strings.Contains(watchdog, `<URI>\`+newName+`-stall-watchdog</URI>`) || !strings.Contains(resume, newName+".exe") || strings.Contains(resume, oldName+".exe") || len(f.jobs) != 3 {
		t.Errorf("the scheduled jobs are %v", f.jobs)
	}
	if held := f.vault[newName+":app/STRIPE_KEY"]; held.User != auth.VaultUser || string(held.Value) != "key" || len(f.vault) != 3 {
		t.Errorf("the credentials are %v", f.vault)
	}
	if _, isKept := f.vault[oldName+":someone-else's"]; !isKept {
		t.Errorf("another program's credential was moved: %v", f.vault)
	}
	for path, want := range map[string]string{
		filepath.Join(state, newName+"-wake-skips.jsonl"):              `{"skipped":"` + oldName + ` spawn"}` + "\n",
		filepath.Join(state, "hosts", newName+".json"):                 `{"host":"` + newName + `","started_by_` + newName + `":true,"pid":7}`,
		filepath.Join(state, "tasktmp", "cg-x", "delivery.json"):       `{"fast":[["` + newName + `","gate","test"]],"mode":"no-mistakes"}`,
		filepath.Join(state, "tickets", "cg-x.json"):                   `{"task_id":"cg-x","repository":"owner/repo","labels":["` + newName + `: running"]}`,
		filepath.Join(state, "ops", "watchdog.ps1"):                    "& \"$env:" + strings.ToUpper(newName) + "_HOME\\bin\\" + newName + ".exe\" watch\n",
		filepath.Join(root, "data", "backlog.md"):                      "| cg-x | run `" + newName + " spawn cg-x` |\n| " + oldName + "-native-board | done |\n",
		filepath.Join(root, "data", "memory", "fleet.md"):              "Use " + newName + " gate turn --low-priority.\n",
		filepath.Join(root, "data", "cg-x", "ticket.md"):               "Run " + newName + " status first.\n",
		filepath.Join(root, "data", newName+"-handoff-2026-11-05.md"):  "The CFO handed off; " + oldName + " spawn later.\n",
		filepath.Join(root, "AGENTS.md"):                               agentsText,
		filepath.Join(root, "docs", "commands", "spawn.md"):            spawnPage,
		filepath.Join(user, ".agents", "skills", "lavish", "SKILL.md"): lavishText,
		filepath.Join(state, "tasktmp", "cg-x", "brief.md"):            "# Brief\n\nRun `" + newName + " gate test` with " + strings.ToUpper(newName) + "_HOME cleared, then " + newName + " notify cg-x --done.\n\nThe Overlord wrote: \"restart the " + oldName + " when " + oldName + " gate test hangs\"\n\n> " + oldName + " spawn is his to run\n\nTask cg-" + oldName + "-wakes and the skill " + newName + "-afk stay named.\n",
	} {
		if got := read(t, path); got != want {
			t.Errorf("%s is\n%q, want\n%q", path, got, want)
		}
	}
	if !exists(filepath.Join(root, "."+newName+"-home")) || !exists(filepath.Join(user, "."+newName, "credentials", "x")) {
		t.Errorf("the home's marker or the credential folder were not renamed")
	}
	if exists(filepath.Join(f.machine.LocalAppData, newName)) {
		t.Errorf("the old build's caches outside the home were moved")
	}
	if owner := read(t, filepath.Join(user, ".agents", "skills", "lavish", ".code-goblins")); owner == lavishOwnerText || !strings.Contains(owner, "SKILL.md") {
		t.Errorf("the unrenamed skill's owner file is %q, want this build's", owner)
	}
	if got := strings.Join(f.labels["owner/repo"], ","); got != newName+": running,"+newName+": queued,bug" {
		t.Errorf("the labels are %s", got)
	}
}

func TestTheUndoRestoresTheMachineExactly(t *testing.T) {
	// Arrange
	f := newFixture(t)
	before := f.snapshot(t)
	plan, err := f.machine.Plan()
	if err != nil {
		t.Fatal(err)
	}
	folder, err := f.machine.Apply(plan, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = f.machine.Undo(folder)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the undo left the machine changed:\n%s", strings.Join(found, "\n"))
	}
}

func TestAFailedStepUndoesEveryStepBeforeIt(t *testing.T) {
	// Arrange
	f := newFixture(t)
	plan, err := f.machine.Plan()
	if err != nil {
		t.Fatal(err)
	}
	f.labels["owner/repo"] = []string{"bug"}
	before := f.snapshot(t)

	// Act
	folder, err := f.machine.Apply(plan, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))

	// Assert
	if err == nil || folder != "" || !strings.Contains(err.Error(), "everything it made before was undone") {
		t.Fatalf("apply returned %q, %v; want a failure that undid everything", folder, err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the failed apply left the machine changed:\n%s", strings.Join(found, "\n"))
	}
}

func TestAFailedStepRemovesTheFoldersItMade(t *testing.T) {
	// Arrange
	top := t.TempDir()
	target := top
	machine := Machine{Root: filepath.Join(top, "home"), Link: func(string, string) error { return errors.New("no junction can be made here") }}
	plan := Plan{Changes: []Change{{Kind: KindSkill, Line: "a junction in folders not there yet", Steps: []Step{{Kind: "link", Key: filepath.Join(top, "skills", "nested", newName+"-afk"), After: &target}}}}}

	// Act
	folder, err := machine.Apply(plan, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))

	// Assert
	if err == nil || folder != "" {
		t.Fatalf("apply returned %q, %v; want the junction's failure", folder, err)
	}
	for _, gone := range []string{filepath.Join(top, "skills"), machine.Root} {
		if exists(gone) {
			t.Errorf("%s, which the failed apply made, is still there", gone)
		}
	}
}

// undeletable is Credential Manager refusing to delete one target.
type undeletable struct {
	fakeVault
	target string
}

func (v undeletable) Delete(target string) error {
	if target == v.target {
		return errors.New("Credential Manager refused to delete " + target)
	}
	return v.fakeVault.Delete(target)
}

func TestACredentialWhoseOldEntryStaysIsNotLeftUnderItsNewName(t *testing.T) {
	// Arrange
	f := newFixture(t)
	f.machine.Vault = undeletable{fakeVault: f.vault, target: oldName + ":GITHUB_TOKEN"}
	plan, err := f.machine.Plan()
	if err != nil {
		t.Fatal(err)
	}
	before := f.snapshot(t)

	// Act
	folder, err := f.machine.Apply(plan, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))

	// Assert
	if err == nil || folder != "" || !strings.Contains(err.Error(), "everything it made before was undone") {
		t.Fatalf("apply returned %q, %v; want a failure that undid everything", folder, err)
	}
	if found := before.differences(f.snapshot(t)); len(found) > 0 {
		t.Fatalf("the failed apply left the machine changed, the secret under both names among it: %v\n%s", f.vault, strings.Join(found, "\n"))
	}
}
