// Package cutover moves an installed Code Goblins home, and the harnesses,
// settings, scheduled jobs and credentials around it, from the command's old
// name to ceo: a plan that lists every change and proves itself by its
// digest, an apply that first writes everything it needs to undo, and the
// undo. It never changes recorded history: archives, backups, logs, status
// logs, git history and the Supreme Overlord's quoted words keep theirs.
package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// Machine is the installed home the cutover moves and what around it names
// the old command.
type Machine struct {
	// Root is the home's folder, holding state, data, config and bin.
	Root string
	// UserHome is the user's profile folder, which holds the credential
	// file store's folder.
	UserHome string
	// LocalAppData holds the caches the old build kept outside the home,
	// which the plan lists for deletion and never changes.
	LocalAppData string
	// Harnesses are the harnesses' folders and the shared skills folder.
	Harnesses harnessmap.Map
	// ProjectsRoot is the folder of the Overlord's checkouts, whose files
	// naming the old command are listed and never changed; empty for none.
	ProjectsRoot string
	// Program is this build's ceo.exe, which the home's bin takes as ceo.exe
	// and goblins.exe, with the hook program and the desktop window beside it
	// when there are.
	Program string
	// Skills are the skills this build ships, by folder.
	Skills fs.FS
	// Contract is the contract this build writes into the home, by its path
	// in the home.
	Contract fs.FS
	// Commands are this build's commands: the old name followed by one of
	// them in a file is a call of the command.
	Commands []string
	Env      installbase.EnvStore
	Jobs     Scheduler
	Vault    Vault
	Labels   Labels
	Link     harnessmap.Linker
	// Git runs git in dir and returns what it printed.
	Git func(dir string, args ...string) ([]byte, error)
}

// Scheduler reads and writes the Task Scheduler's jobs, each by its full
// name, such as \Folder\Name.
type Scheduler interface {
	Jobs() ([]Job, error)
	Register(job Job) error
	Delete(name string) error
}

// Job is one scheduled job: its full name and its definition, as the Task
// Scheduler exports it.
type Job struct {
	Name string
	XML  string
}

// Vault reads and writes Credential Manager's generic entries.
type Vault interface {
	Entries(prefix string) ([]auth.VaultEntry, error)
	Write(entry auth.VaultEntry) error
	Delete(target string) error
}

// Labels reads and renames a GitHub repository's labels. A renamed label
// stays on every issue that carried it.
type Labels interface {
	Labels(repository string) ([]string, error)
	Rename(repository, from, to string) error
}

// Kinds of change, in the order a plan lists them.
const (
	KindProgram    = "program"
	KindContract   = "contract"
	KindSetting    = "setting"
	KindHook       = "harness file"
	KindSkill      = "skill"
	KindJob        = "scheduled job"
	KindCredential = "credential"
	KindState      = "state file"
	KindTask       = "task file"
	KindLabel      = "label"
	KindListed     = "listed, not changed"
	KindNote       = "note"
)

var kindOrder = []string{KindProgram, KindContract, KindSetting, KindHook, KindSkill, KindJob, KindCredential, KindState, KindTask, KindLabel, KindListed, KindNote}

// Change is one line of a plan and the steps that make it.
type Change struct {
	Kind  string
	Line  string
	Steps []Step
}

// Step is one thing the cutover changes, with what it was and what it
// becomes, which is everything the undo needs.
type Step struct {
	// Kind is file, move, link, setting, job, credential or label.
	Kind string `json:"kind"`
	// Key is the file's or link's path, the setting's or job's name, the
	// credential's target or the label's repository.
	Key string `json:"key"`
	// To is where a move, credential or label goes.
	To string `json:"to,omitempty"`
	// From is the label's name before.
	From string `json:"from,omitempty"`
	// User is the credential's user before.
	User string `json:"user,omitempty"`
	// Before and After are a setting's value, a job's definition or a link's
	// target; nil is none.
	Before *string `json:"before,omitempty"`
	After  *string `json:"after,omitempty"`

	// before and after are a file's content; isAbsent and isRemoved say it
	// is not there before or after.
	before, after       []byte
	isAbsent, isRemoved bool
	// readFrom is where a file's content before is read, when a move
	// before it in the plan puts it at its path.
	readFrom string
}

// Plan is every change the cutover makes, in the order it makes them.
type Plan struct {
	Changes []Change
}

// Digest is the plan's SHA-256 over every line and step, which the apply
// names to make exactly this plan.
func (p Plan) Digest() string {
	hash := sha256.New()
	for _, change := range p.Changes {
		fmt.Fprintf(hash, "%s\x00%s\x00", change.Kind, change.Line)
		for _, step := range change.Steps {
			encoded, _ := json.Marshal(step)
			hash.Write(encoded)
			fmt.Fprintf(hash, "\x00%t%t%x%x\x00", step.isAbsent, step.isRemoved, sha256.Sum256(step.before), sha256.Sum256(step.after))
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Listing is the plan's lines grouped by kind, in plan order within each.
func (p Plan) Listing() []string {
	var lines []string
	for _, kind := range kindOrder {
		var group []string
		for _, change := range p.Changes {
			if change.Kind == kind {
				group = append(group, "  "+change.Line)
			}
		}
		if len(group) > 0 {
			lines = append(lines, kind+":")
			lines = append(lines, group...)
		}
	}
	return lines
}

// planner builds a plan for one machine.
type planner struct {
	machine Machine
	rules   rules
	changes []Change
	// claimed are the paths a change already moves or rewrites.
	claimed map[string]bool
}

// Plan reads the machine and plans the cutover. It changes nothing.
func (m Machine) Plan() (Plan, error) {
	skills, err := fs.ReadDir(m.Skills, ".")
	if err != nil {
		return Plan{}, fmt.Errorf("cutover: read this build's skills: %w", err)
	}
	var skillNames []string
	for _, entry := range skills {
		if entry.IsDir() {
			skillNames = append(skillNames, strings.ToLower(entry.Name()))
		}
	}
	p := &planner{machine: m, rules: newRules(m.Commands, skillNames), claimed: map[string]bool{}}
	for _, part := range []func() error{p.programs, p.contract, p.settings, p.harnessFiles, p.skills, p.jobs, p.credentials, p.stateFiles, p.taskFiles, p.labels, p.elsewhere} {
		if err := part(); err != nil {
			return Plan{}, err
		}
	}
	p.add(KindNote, "each browser that opened the board shows the first-open layout and the quick tour once more, and forgets its pane width, font sizes and chat style, which the browser keeps, not the home")
	return Plan{Changes: p.changes}, nil
}

func (p *planner) add(kind, line string, steps ...Step) {
	p.changes = append(p.changes, Change{Kind: kind, Line: line, Steps: steps})
}

func (p *planner) state() string { return filepath.Join(p.machine.Root, "state") }
func (p *planner) data() string  { return filepath.Join(p.machine.Root, "data") }

// readFile is a file's content, or isAbsent when nothing is there.
func readFile(path string) (content []byte, isAbsent bool, err error) {
	content, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	return content, false, err
}

func writeStep(path string, before []byte, isAbsent bool, after []byte) Step {
	return Step{Kind: "file", Key: path, before: before, isAbsent: isAbsent, after: after}
}

func removeStep(path string, before []byte) Step {
	return Step{Kind: "file", Key: path, before: before, isRemoved: true}
}

func moveStep(from, to string) Step {
	return Step{Kind: "move", Key: from, To: to}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// programs puts this build in the home's bin as ceo.exe and goblins.exe, and
// the hook program and the desktop window beside it, and takes the old
// build's programs out.
func (p *planner) programs() error {
	bin := filepath.Join(p.machine.Root, "bin")
	program, err := os.ReadFile(p.machine.Program)
	if err != nil {
		return fmt.Errorf("cutover: read this build: %w", err)
	}
	install := func(name, source string, content []byte) error {
		path := filepath.Join(bin, name)
		before, isAbsent, err := readFile(path)
		if err != nil {
			return err
		}
		if !isAbsent && string(before) == string(content) {
			return nil
		}
		p.add(KindProgram, fmt.Sprintf("%s: this build, from %s", path, source), writeStep(path, before, isAbsent, content))
		return nil
	}
	if err := install(newName+".exe", p.machine.Program, program); err != nil {
		return err
	}
	if err := install("goblins.exe", p.machine.Program, program); err != nil {
		return err
	}
	for _, name := range []string{newName + "-hook.exe", "goblins-window.exe"} {
		source := filepath.Join(filepath.Dir(p.machine.Program), name)
		content, err := os.ReadFile(source)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := install(name, source, content); err != nil {
			return err
		}
	}
	for _, name := range []string{oldName + ".exe", oldName + "-hook.exe"} {
		path := filepath.Join(bin, name)
		before, isAbsent, err := readFile(path)
		if err != nil {
			return err
		}
		if !isAbsent {
			p.add(KindProgram, path+": removed, the old build", removeStep(path, before))
		}
	}
	return nil
}

// contract writes this build's contract into the home, each file whose
// content differs, as an install does. A code-goblins checkout keeps its own,
// which git tracks.
func (p *planner) contract() error {
	if exists(filepath.Join(p.machine.Root, "cmd", newName)) || exists(filepath.Join(p.machine.Root, "cmd", oldName)) {
		return nil
	}
	return fs.WalkDir(p.machine.Contract, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(p.machine.Contract, name)
		if err != nil {
			return err
		}
		path := filepath.Join(p.machine.Root, filepath.FromSlash(name))
		before, isAbsent, err := readFile(path)
		if err != nil {
			return err
		}
		if !isAbsent && string(before) == string(content) {
			return nil
		}
		p.claimed[path] = true
		p.add(KindContract, path+": this build's", writeStep(path, before, isAbsent, content))
		return nil
	})
}

// settings moves every user setting of the old name's to the new name's.
func (p *planner) settings() error {
	all, err := p.machine.Env.Names()
	if err != nil {
		return fmt.Errorf("cutover: list the user's settings: %w", err)
	}
	prefix := strings.ToUpper(oldName) + "_"
	for _, name := range all {
		if !strings.HasPrefix(strings.ToUpper(name), prefix) {
			continue
		}
		value, _, err := p.machine.Env.Get(name)
		if err != nil {
			return err
		}
		to := strings.ToUpper(newName) + "_" + name[len(prefix):]
		current, isSet, err := p.machine.Env.Get(to)
		if err != nil {
			return err
		}
		var before *string
		if isSet {
			before = &current
		}
		line := fmt.Sprintf("%s=%s becomes %s", name, value, to)
		if isSet && current != value {
			line += fmt.Sprintf(", in place of its %s", current)
		}
		p.add(KindSetting, line,
			Step{Kind: "setting", Key: to, Before: before, After: &value},
			Step{Kind: "setting", Key: name, Before: &value})
	}
	return nil
}

// harnessSettings are the files in a harness's folder that hold the hooks
// and the allow rules the old build wrote.
var harnessSettings = []string{"settings.json", "settings.local.json", "hooks.json", "config.toml"}

// harnessFiles rewrites the hooks and rules each harness's settings hold,
// and renames the files the old build wrote in a harness's folder: a
// settings file's backups and rules, the native hooks' helper and backups,
// and pi's extension.
func (p *planner) harnessFiles() error {
	for _, root := range p.machine.Harnesses.Roots {
		for _, folder := range []string{root.Path, filepath.Join(root.Path, "extensions")} {
			entries, err := os.ReadDir(folder)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			for _, entry := range entries {
				name := entry.Name()
				renamed := p.rules.renamedFile(name)
				if entry.IsDir() || renamed == name && !slices.Contains(harnessSettings, name) {
					continue
				}
				if err := p.rewrite(KindHook, filepath.Join(folder, name), filepath.Join(folder, renamed), nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// rewrite rewrites the file at path by the rules and moves it to to, when
// either changes anything. Text that protected matches keeps its words.
func (p *planner) rewrite(kind, path, to string, protected *regexp.Regexp) error {
	if p.claimed[path] {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return err
	}
	if exists(to) && to != path {
		p.add(KindListed, fmt.Sprintf("%s: kept, since %s is already there", path, to))
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	after := string(content)
	if !isBinary(content) {
		switch {
		case strings.EqualFold(filepath.Ext(path), ".json"):
			after = p.rules.json(after)
		default:
			after = p.rules.text(after, protected)
		}
	}
	var steps []Step
	var said []string
	if after != string(content) {
		steps = append(steps, writeStep(path, content, false, []byte(after)))
		count := changedLines(string(content), after)
		unit := "lines"
		if count == 1 {
			unit = "line"
		}
		said = append(said, fmt.Sprintf("%d %s rewritten", count, unit))
	}
	if to != path {
		steps = append(steps, moveStep(path, to))
		said = append(said, "renamed "+filepath.Base(to))
	}
	if len(steps) > 0 {
		p.claimed[path] = true
		p.add(kind, path+": "+strings.Join(said, ", "), steps...)
	}
	return nil
}

func isBinary(content []byte) bool {
	return slices.Contains(content, 0)
}

func changedLines(before, after string) int {
	beforeLines, afterLines := strings.Split(before, "\n"), strings.Split(after, "\n")
	changed := 0
	for index := range beforeLines {
		if index >= len(afterLines) || beforeLines[index] != afterLines[index] {
			changed++
		}
	}
	return changed
}

// skills makes each skill Code Goblins installed in the shared skills folder
// this build's: one of the old name's becomes this build's skill of the new
// name, with Claude Code's junction to it, and every file of a skill whose
// content differs from this build's is written, and every file this build
// does not ship is removed.
func (p *planner) skills() error {
	shared := p.machine.Harnesses.SharedSkills
	entries, err := os.ReadDir(shared)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	claude, _ := p.machine.Harnesses.Harness("claude")
	for _, entry := range entries {
		name := entry.Name()
		renamed := p.rules.renamed(name)
		if !entry.IsDir() || !slices.Contains(p.rules.skills, strings.ToLower(renamed)) {
			continue
		}
		from, to := filepath.Join(shared, name), filepath.Join(shared, renamed)
		isOwned := exists(filepath.Join(from, ".code-goblins"))
		switch {
		case !isOwned && renamed == name:
			continue
		case !isOwned:
			p.add(KindListed, from+": kept, since Code Goblins did not install it")
			continue
		case renamed != name && exists(to):
			p.add(KindListed, fmt.Sprintf("%s: kept, since %s is already there", from, to))
			continue
		}
		files, err := harnessmap.SkillFiles(p.machine.Skills, renamed)
		if err != nil {
			return err
		}
		if renamed != name {
			var steps []Step
			if claude.Skills != "" {
				link := filepath.Join(claude.Skills, name)
				if target, err := os.Readlink(link); err == nil && strings.EqualFold(filepath.Clean(target), from) {
					steps = append(steps, Step{Kind: "link", Key: link, Before: &target})
				}
			}
			steps = append(steps, moveStep(from, to))
			if claude.Skills != "" {
				link := filepath.Join(claude.Skills, renamed)
				if !exists(link) {
					target := to
					steps = append(steps, Step{Kind: "link", Key: link, After: &target})
				}
			}
			p.claimed[from] = true
			p.add(KindSkill, fmt.Sprintf("%s: becomes %s, with Claude Code's junction to it", from, to), steps...)
		}
		err = filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			rel, err := filepath.Rel(from, path)
			if err != nil {
				return err
			}
			if _, isShipped := files[filepath.ToSlash(rel)]; !isShipped {
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				step := removeStep(filepath.Join(to, rel), content)
				step.readFrom = path
				p.add(KindSkill, step.Key+": removed, since this build does not ship it", step)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, rel := range sortedKeys(files) {
			before, isAbsent, err := readFile(filepath.Join(from, filepath.FromSlash(rel)))
			if err != nil {
				return err
			}
			if isAbsent || string(before) != string(files[rel]) {
				step := writeStep(filepath.Join(to, filepath.FromSlash(rel)), before, isAbsent, files[rel])
				step.readFrom = filepath.Join(from, filepath.FromSlash(rel))
				p.add(KindSkill, step.Key+": this build's", step)
			}
		}
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jobs renames each scheduled job named for the old command and rewrites
// each whose definition calls it.
func (p *planner) jobs() error {
	jobs, err := p.machine.Jobs.Jobs()
	if err != nil {
		return fmt.Errorf("cutover: list the scheduled jobs: %w", err)
	}
	for _, job := range jobs {
		folder, name := job.Name[:strings.LastIndex(job.Name, `\`)+1], job.Name[strings.LastIndex(job.Name, `\`)+1:]
		renamed := name
		if jobName.MatchString(name) {
			renamed = newSpelling(name)
		}
		definition := p.rules.text(job.XML, nil)
		if renamed != name {
			definition = strings.ReplaceAll(definition, job.Name, folder+renamed)
		}
		if renamed == name && definition == job.XML {
			continue
		}
		before, after := job.XML, definition
		if renamed == name {
			p.add(KindJob, job.Name+": its definition rewritten", Step{Kind: "job", Key: job.Name, Before: &before, After: &after})
			continue
		}
		p.add(KindJob, fmt.Sprintf("%s: becomes %s", job.Name, folder+renamed),
			Step{Kind: "job", Key: folder + renamed, After: &after},
			Step{Kind: "job", Key: job.Name, Before: &before})
	}
	return nil
}

// jobName is a job's name that names the old command as a word.
var jobName = regexp.MustCompile(`(?i)(^|[^a-z0-9])` + oldName + `($|[^a-z0-9])`)

// credentials moves each credential the old build kept in Credential
// Manager to the new build's name for it, and the folder of the file store
// it falls back to.
func (p *planner) credentials() error {
	prefix, user := oldSpelling(auth.VaultPrefix), oldSpelling(auth.VaultUser)
	entries, err := p.machine.Vault.Entries(prefix)
	if err != nil {
		return fmt.Errorf("cutover: list the credentials: %w", err)
	}
	newEntries, err := p.machine.Vault.Entries(auth.VaultPrefix)
	if err != nil {
		return fmt.Errorf("cutover: list the credentials: %w", err)
	}
	for _, entry := range entries {
		if entry.User != user {
			continue
		}
		to := auth.VaultPrefix + entry.Target[len(prefix):]
		if slices.ContainsFunc(newEntries, func(held auth.VaultEntry) bool { return strings.EqualFold(held.Target, to) }) {
			p.add(KindListed, fmt.Sprintf("credential %s: kept, since %s is already there", entry.Target, to))
			continue
		}
		p.add(KindCredential, fmt.Sprintf("%s becomes %s", entry.Target, to), Step{Kind: "credential", Key: entry.Target, To: to, User: entry.User})
	}
	from, to := filepath.Join(p.machine.UserHome, "."+oldName), filepath.Join(p.machine.UserHome, "."+newName)
	if exists(from) && !exists(to) {
		p.add(KindCredential, fmt.Sprintf("%s: the credential file store's folder, becomes %s", from, to), moveStep(from, to))
	}
	return nil
}

// stateFiles renames the home's files and markers the old build named, the
// CEO's own terminal's files and the handoff notes, rewrites the identifiers
// the state's records hold, and lists the old build's caches outside the home.
func (p *planner) stateFiles() error {
	move := func(folder string) error {
		entries, err := os.ReadDir(folder)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			renamed := p.rules.renamed(name)
			if renamed == name {
				continue
			}
			from, to := filepath.Join(folder, name), filepath.Join(folder, renamed)
			if exists(to) {
				p.add(KindListed, fmt.Sprintf("%s: kept, since %s is already there", from, to))
				continue
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(name), ".json") {
				if err := p.rewrite(KindState, from, to, nil); err != nil {
					return err
				}
				continue
			}
			p.claimed[from] = true
			p.add(KindState, fmt.Sprintf("%s: renamed %s", from, renamed), moveStep(from, to))
		}
		return nil
	}
	for _, folder := range []string{p.machine.Root, p.state(), p.data()} {
		if err := move(folder); err != nil {
			return err
		}
	}
	host := func(name string) bool { return strings.HasPrefix(strings.ToLower(name), oldName+".") }
	hosts := filepath.Join(p.state(), "hosts")
	entries, err := os.ReadDir(hosts)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !host(name) {
			continue
		}
		from, to := filepath.Join(hosts, name), filepath.Join(hosts, newName+name[len(oldName):])
		if exists(to) {
			p.add(KindListed, fmt.Sprintf("%s: kept, since %s is already there", from, to))
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".json") {
			if err := p.rewrite(KindState, from, to, nil); err != nil {
				return err
			}
			continue
		}
		p.claimed[from] = true
		p.add(KindState, fmt.Sprintf("%s: renamed %s, the CEO's own terminal's", from, filepath.Base(to)), moveStep(from, to))
	}
	if caches := filepath.Join(p.machine.LocalAppData, oldName); p.machine.LocalAppData != "" && exists(caches) {
		p.add(KindListed, caches+": the old build's caches outside the home, left for you to delete, since this build makes its own")
	}
	for _, folder := range []string{p.state(), filepath.Join(p.state(), "tickets")} {
		entries, err := os.ReadDir(folder)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") && !strings.EqualFold(filepath.Ext(entry.Name()), ".meta") {
				continue
			}
			path := filepath.Join(folder, entry.Name())
			if err := p.rewrite(KindState, path, path, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
