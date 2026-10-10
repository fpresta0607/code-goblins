package projectcheck

import (
	"os"
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
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.Checkout, filepath.FromSlash(name))
		}
		if _, err := os.Stat(path); err != nil {
			return name + " is no file at " + path, false
		}
		return path, true
	}
	path, err := c.LookPath(name)
	if err != nil {
		return name + " is not on PATH", false
	}
	return path, true
}

// fileFlags are the flags whose next argument is a file the command reads,
// in lower case.
var fileFlags = map[string]bool{"-file": true, "--file": true, "--env-file": true, "--constraint": true}

// requirementFlags are pip's flags for a requirements file, which mean
// something else to other programs.
var requirementFlags = map[string]bool{"-r": true, "--requirement": true}

// commandFaults returns why a command cannot run as written: its program is
// missing, or a file it is told to read is not in the repository. It starts
// nothing.
func (c *checker) commandFaults(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	var faults []string
	if where, found := c.program(argv[0]); !found {
		faults = append(faults, where)
	}
	pip := slices.Contains(argv, "pip") || slices.Contains(argv, "pip3")
	for index := 1; index+1 < len(argv); index++ {
		flag := strings.ToLower(argv[index])
		if !fileFlags[flag] && !(pip && requirementFlags[flag]) {
			continue
		}
		if file := argv[index+1]; !c.fileExists(file) {
			faults = append(faults, file+" is neither in "+c.Checkout+" nor tracked at "+c.repo.at())
		}
	}
	return faults
}

// fileExists reports whether a file a command names is there: anywhere on
// this machine for an absolute path, in the repository or the checkout's
// folder for a relative one.
func (c *checker) fileExists(name string) bool {
	if filepath.IsAbs(name) {
		_, err := os.Stat(name)
		return err == nil
	}
	return c.repo.has(name)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
