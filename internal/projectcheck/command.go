package projectcheck

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// program says where the program a command starts with is: a file of the
// checkout when the command names it by path, otherwise wherever a shell
// would find it. Found is false when it is neither, and where then says what
// was looked at.
func (c *checker) program(name string) (where string, found bool) {
	if strings.ContainsAny(name, `/\`) {
		file := name
		if !filepath.IsAbs(file) {
			file = filepath.Join(c.Checkout, filepath.FromSlash(name))
		}
		if _, err := os.Stat(file); err != nil {
			return name + " is no file at " + file, false
		}
		return file, true
	}
	file, err := c.LookPath(name)
	if err != nil {
		return name + " is not on PATH", false
	}
	return file, true
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

// proof is what looking at a command found, with nothing started.
type proof struct {
	// faults say why the command cannot run as written.
	faults []string
	// notes say what it was found to rest on beside its program.
	notes []string
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
// program is missing, a file or folder it names is not in the repository,
// or the package script it runs is defined in no package file. A command
// read from prose does not say where it runs, so with anywhere a package
// script counts as defined when a package file one or two folders down
// defines it.
func (c *checker) prove(ctx context.Context, s segment, anywhere bool) proof {
	var p proof
	argv := s.argv
	if len(argv) == 0 {
		return p
	}
	if where, found := c.program(argv[0]); !found {
		p.faults = append(p.faults, where)
	}
	missing := func(name string) {
		p.faults = append(p.faults, name+" is neither in "+c.Checkout+" nor tracked at "+c.repo.at())
	}
	pip := slices.Contains(argv, "pip") || slices.Contains(argv, "pip3")
	for index := 1; index < len(argv); index++ {
		arg := argv[index]
		flag := strings.ToLower(arg)
		switch {
		case fileFlags[flag] || (pip && requirementFlags[flag]):
			if index+1 < len(argv) {
				index++
				if file := argv[index]; !c.fileExists(s.dir, file) {
					missing(file)
				}
			}
		case strings.HasPrefix(arg, "-") || strings.Contains(arg, "://"):
		case scriptExtensions[strings.ToLower(path.Ext(arg))]:
			if !c.fileExists(s.dir, arg) {
				missing(arg)
			}
		case argv[0] == "go" && strings.HasPrefix(arg, "./"):
			folder := path.Join(s.dir, strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(arg, "./"), "..."), "/"))
			if folder != "." && !c.repo.hasFolder(folder) {
				p.faults = append(p.faults, folder+" is not a folder of the repository at "+c.repo.at())
			}
		}
	}
	if packageManagers[argv[0]] {
		c.proveScript(ctx, s, anywhere, &p)
	}
	return p
}

// proveScript checks that the package script a command runs is defined.
func (c *checker) proveScript(ctx context.Context, s segment, anywhere bool, p *proof) {
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
	if anywhere {
		for _, name := range sortedKeys(c.repo.tracked) {
			if path.Base(name) == "package.json" && strings.Count(name, "/") <= 2 && name != files[0] {
				files = append(files, name)
			}
		}
	}
	for _, file := range files {
		data, _, ok := c.folderFile(ctx, file)
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

// commandFaults returns why a command run at the checkout's root cannot run
// as written.
func (c *checker) commandFaults(ctx context.Context, argv []string) []string {
	return c.prove(ctx, segment{argv: argv}, false).faults
}

// fileExists reports whether a file a command names is there: anywhere on
// this machine for an absolute path, otherwise in the repository or the
// checkout's folder, under the folder the command runs in.
func (c *checker) fileExists(dir, name string) bool {
	if filepath.IsAbs(name) {
		_, err := os.Stat(name)
		return err == nil
	}
	return c.repo.has(path.Join(dir, filepath.ToSlash(name)))
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
