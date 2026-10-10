package projectcheck

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// place is where a command runs, which decides whose files answer for it.
type place int

const (
	// inWorktree is a goblin's or a gate's worktree, which is cut from the
	// default branch: where a command of the gate file, of the record and of
	// a worktree's install runs.
	inWorktree place = iota
	// inProse is a worktree too, for a command read from an instruction
	// file, which does not say what folder it runs in and may name a path
	// its writer made up to show the shape of a command.
	inProse
	// inCheckout is the checkout itself, where the check of a services
	// manifest runs.
	inCheckout
)

// locate says why a path a command names is not where the command runs, or
// returns an empty why when it is there, with a note where that rests on
// more than the path being tracked. In a worktree the default branch
// answers for everything git tracks, since the folder's copy of a tracked
// file is as old as its last pull: a copy only the folder's own branch
// tracks is not in a worktree, and a file the default branch gained since
// is. A file git does not track is in a worktree only when the worktree is
// given it or its install step makes it, so what the checkout's own folder
// happens to hold does not answer for it.
func (c *checker) locate(s segment, name string, folder bool, where place) (note, why string) {
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err != nil {
			return "", name + " is not on this machine"
		}
		return "", ""
	}
	if c.repo == nil {
		return "not judged, since this machine has no checkout", ""
	}
	relative := path.Join(s.dir, filepath.ToSlash(name))
	info, err := os.Stat(filepath.Join(c.Checkout, filepath.FromSlash(relative)))
	held := err == nil && (!folder || info.IsDir())
	switch {
	case where == inCheckout && held:
		return "", ""
	case where == inCheckout:
		return "", relative + " is not in " + c.Checkout + ", where the command runs"
	case c.repo.tracks(relative, folder):
		return "", ""
	}
	plan := c.worktreePlan()
	top, _, _ := strings.Cut(relative, "/")
	for _, shared := range plan.shared {
		switch {
		case relative != shared && !strings.HasPrefix(relative, shared+"/"):
		case held:
			return "shared from the checkout by " + worktree.ManifestFileName, ""
		default:
			return "", relative + " is not in " + c.Checkout + ", and " + worktree.ManifestFileName + " shares " + shared + " from there"
		}
	}
	for _, command := range plan.install {
		// A command makes a folder by its program, as the fleet reads it, or
		// by naming one of the folders an install makes, as python -m venv
		// .venv does. A file it only reads is not made by it.
		if slices.Contains(worktree.InstallOutputs([]string{command}), top) || (installed[top] && slices.Contains(strings.Fields(command), top)) {
			return "made by the worktree's install step, " + command, ""
		}
	}
	switch {
	case installed[top] && len(plan.install) == 0:
		return "", relative + " is in no worktree: no install step of a worktree makes " + top + ", since " + plan.why
	case installed[top]:
		return "", relative + " is in no worktree: no install step of a worktree makes " + top + ", and its install is " + strings.Join(plan.install, ", ")
	case !held:
		return "", relative + " is neither tracked at " + c.repo.at() + " nor in " + c.Checkout
	case c.repo.indexTracks(relative):
		return "", relative + " is not tracked at " + c.repo.at() + ": the copy in " + c.Checkout + " belongs to the branch the folder is on, and a worktree is cut from " + c.repo.ref
	}
	return "", relative + " is in " + c.Checkout + " and git does not track it, so a worktree is not given it: " + worktree.ManifestFileName + " shares no such path"
}

// installed are the folders an install step makes, which a command reaches
// into for a program.
var installed = map[string]bool{".venv": true, "venv": true, "node_modules": true}

// worktreePlan is what a worktree of the project holds beside what git
// tracks.
type worktreePlan struct {
	// shared are the paths the worktree manifest gives a worktree from the
	// checkout: the files it links and the folders its link strategy joins.
	shared []string
	// install are the commands a goblin's first step runs in the worktree,
	// and why says where they came from when there are none.
	install []string
	why     string
}

// worktreePlan reads what a worktree of the project is given and what its
// install step runs, by the same code a spawn uses. A manifest the loader
// refuses gives nothing, as a spawn it refuses gives no worktree.
func (c *checker) worktreePlan() *worktreePlan {
	if c.plan != nil {
		return c.plan
	}
	c.plan = &worktreePlan{why: worktree.ManifestFileName + " is one the loader refuses"}
	manifest, err := worktree.Resolve(c.DataDir, c.project)
	if err != nil {
		return c.plan
	}
	c.plan.shared = append(c.plan.shared, manifest.Link...)
	switch manifest.Dependencies.Strategy {
	case worktree.StrategyLink:
		c.plan.shared = append(c.plan.shared, manifest.Dependencies.Paths...)
		c.plan.why = worktree.ManifestFileName + " shares its dependency folders and installs nothing"
	case worktree.StrategyNone:
		c.plan.why = worktree.ManifestFileName + " sets the dependency strategy none"
	default:
		c.plan.install = worktree.InstallPlan(manifest, func(name string) bool { return c.repo.tracked[name] })
		c.plan.why = worktree.ManifestFileName + " names no install command and " + c.repo.at() + " has no lockfile a spawn installs from"
	}
	return c.plan
}

// program says where the program a command starts with is: a file of the
// repository when the command names it by path, otherwise wherever a shell
// would find it. Found is false when it is neither, and where then says what
// was looked at.
func (c *checker) program(s segment, where place) (found string, ok bool) {
	name := s.argv[0]
	if strings.ContainsAny(name, `/\`) {
		note, why := c.locate(s, name, false, where)
		switch {
		case why != "":
			return why, false
		case note != "":
			return note, true
		case filepath.IsAbs(name):
			return "at " + name, true
		case where != inCheckout:
			return "tracked at " + c.repo.at(), true
		}
		return "in " + c.Checkout, true
	}
	file, err := c.LookPath(name)
	if err != nil {
		return name + " is not on PATH", false
	}
	return "at " + file, true
}

// fileFlags are the flags whose next argument is a file the command reads,
// in lower case.
var fileFlags = map[string]bool{"-file": true, "--file": true, "--env-file": true, "--constraint": true}

// requirementFlags are pip's flags for a requirements file, which mean
// something else to other programs.
var requirementFlags = map[string]bool{"-r": true, "--requirement": true}

// scriptExtensions end the name of a file a command runs.
var scriptExtensions = map[string]bool{".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".sh": true, ".ps1": true, ".cmd": true, ".bat": true, ".rb": true}

// packageManagers run the scripts a package.json defines.
var packageManagers = map[string]bool{"npm": true, "pnpm": true, "yarn": true, "bun": true}

// redirects are the shell's words for sending output to the file named next.
var redirects = map[string]bool{">": true, ">>": true, "1>": true, "2>": true, "&>": true}

// writesLast are the programs whose last argument is what they write.
var writesLast = map[string]bool{"cp": true, "mv": true, "ln": true, "copy": true, "move": true, "rsync": true, "scp": true}

// writesAll are the programs that create or remove everything they name.
var writesAll = map[string]bool{"touch": true, "mkdir": true, "tee": true, "rm": true}

// writesNext reports whether the argument after flag is what the command
// writes: the target of -o, of a flag named for output such as --outfile or
// -outdir, or of a redirect. What a command writes does not have to exist
// before it runs. In every program this check judges, -o takes one argument
// that is an output or an option's value, never a file the command reads.
func writesNext(flag string) bool {
	if flag == "-o" || redirects[flag] {
		return true
	}
	return strings.HasPrefix(flag, "-") && !strings.Contains(flag, "=") && strings.HasPrefix(strings.ToLower(strings.TrimLeft(flag, "-")), "out")
}

// proof is what looking at a command found, with nothing started.
type proof struct {
	// faults say why the command cannot run as written.
	faults []string
	// notes say what it was found to rest on beside its program.
	notes []string
	// examples are the paths it names that are not there and were made up to
	// show the shape of a command.
	examples []string
}

// segment is one command of a command line, and the folder of the checkout
// it runs in after any cd before it.
type segment struct {
	dir  string
	argv []string
}

// segments splits a command line at && and follows each cd, so `cd web &&
// npm test` is npm test in web.
func segments(line string) []segment {
	var found []segment
	dir := ""
	for _, part := range strings.Split(line, "&&") {
		argv := strings.Fields(part)
		for index, arg := range argv {
			argv[index] = strings.Trim(arg, `"'`)
		}
		switch {
		case len(argv) == 0:
		case argv[0] == "cd" && len(argv) == 2:
			dir = path.Join(dir, filepath.ToSlash(argv[1]))
		default:
			found = append(found, segment{dir: dir, argv: argv})
		}
	}
	return found
}

// prove says why a command cannot run as written, starting nothing: its
// program is missing, a file or folder it reads is not where it runs, or the
// package script it runs is defined in no package file. A command read from
// prose does not say where it runs, so there a package script counts as
// defined when a package file one or two folders down defines it.
func (c *checker) prove(ctx context.Context, s segment, where place) proof {
	var p proof
	argv := s.argv
	if len(argv) == 0 {
		return p
	}
	judge := func(name string, folder bool) {
		note, why := c.locate(s, name, folder, where)
		switch {
		case why == "" && note != "":
			p.notes = append(p.notes, name+" "+note)
		case why == "":
		case where == inProse && c.madeUp(ctx, path.Join(s.dir, filepath.ToSlash(name))):
			p.examples = append(p.examples, name)
		default:
			p.faults = append(p.faults, why)
		}
	}
	if strings.ContainsAny(argv[0], `/\`) {
		judge(argv[0], false)
	} else if _, err := c.LookPath(argv[0]); err != nil {
		p.faults = append(p.faults, argv[0]+" is not on PATH")
	}
	if writesAll[argv[0]] {
		return p
	}
	pip := slices.Contains(argv, "pip") || slices.Contains(argv, "pip3")
	for index := 1; index < len(argv); index++ {
		arg := argv[index]
		flag := strings.ToLower(arg)
		switch {
		case writesNext(arg):
			index++
		case fileFlags[flag] || (pip && requirementFlags[flag]):
			if index+1 < len(argv) {
				index++
				judge(argv[index], false)
			}
		case strings.HasPrefix(arg, "-") || strings.Contains(arg, "://") || strings.ContainsRune(arg, '>'):
		case writesLast[argv[0]] && index == len(argv)-1:
		case scriptExtensions[strings.ToLower(path.Ext(arg))]:
			judge(arg, false)
		case argv[0] == "go" && strings.HasPrefix(arg, "./"):
			folder := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "..."), "/")
			if joined := path.Join(s.dir, folder); joined != "" && joined != "." {
				judge(folder, true)
			}
		}
	}
	if packageManagers[argv[0]] {
		c.proveScript(ctx, s, where, &p)
	}
	return p
}

// exampleWords are the words a file name is made of when its writer made it
// up to show the shape of a command.
var exampleWords = map[string]bool{"some": true, "example": true, "sample": true, "foo": true, "bar": true, "baz": true, "qux": true, "your": true, "my": true, "placeholder": true, "xxx": true, "xyz": true}

// nameWord matches one word of a file name: a run of lower-case letters with
// the capital that starts it, or a run of capitals.
var nameWord = regexp.MustCompile(`[A-Z]?[a-z]+|[A-Z]+`)

// madeUp reports whether a path that is not there was made up to show the
// shape of a command, as "run one test file with npx tsx --test
// tests/someFile.test.ts" does. It takes two things: the path's last name
// holds a placeholder word, and no commit the default branch can reach ever
// held the path. The second is what keeps a file that is really missing
// reported: one that was deleted, renamed or moved was once held, whatever
// its name. A folder above the file is never asked, so a real file under
// examples/ is not passed over.
func (c *checker) madeUp(ctx context.Context, name string) bool {
	placeholder := false
	for _, word := range nameWord.FindAllString(path.Base(name), -1) {
		placeholder = placeholder || exampleWords[strings.ToLower(word)]
	}
	if !placeholder {
		return false
	}
	held, known := c.repo.everHeld(ctx, name)
	return known && !held
}

// proveScript checks that the package script a command runs is defined, in
// the package file the place it runs in has.
func (c *checker) proveScript(ctx context.Context, s segment, where place, p *proof) {
	dir, script := s.dir, ""
	for index := 1; index < len(s.argv); index++ {
		switch arg := s.argv[index]; {
		case (arg == "--prefix" || arg == "--dir" || arg == "-C") && index+1 < len(s.argv):
			index++
			dir = path.Join(dir, filepath.ToSlash(s.argv[index]))
		case (arg == "run" || arg == "run-script") && index+1 < len(s.argv):
			script = s.argv[index+1]
		case arg == "test" && script == "":
			script = "test"
		}
	}
	if script == "" {
		return
	}
	files := []string{path.Join(dir, "package.json")}
	if where == inProse {
		for _, name := range sortedKeys(c.repo.tracked) {
			if path.Base(name) == "package.json" && strings.Count(name, "/") <= 2 && name != files[0] {
				files = append(files, name)
			}
		}
	}
	for _, file := range files {
		var data []byte
		var ok bool
		if where == inCheckout {
			data, _, ok = c.folderFile(ctx, file)
		} else {
			data, ok = c.repo.read(ctx, file)
		}
		if !ok {
			continue
		}
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &manifest) == nil && manifest.Scripts[script] != "" {
			p.notes = append(p.notes, "script "+script+" of "+file)
			return
		}
	}
	p.faults = append(p.faults, "no script "+script+" in "+strings.Join(files, " or "))
}

// commandFaults returns why a command run at the root of where cannot run
// as written.
func (c *checker) commandFaults(ctx context.Context, argv []string, where place) []string {
	return c.prove(ctx, segment{argv: argv}, where).faults
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
