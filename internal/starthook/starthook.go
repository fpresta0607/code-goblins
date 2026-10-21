// Package starthook finds the code a Python environment runs at every
// interpreter start, before any module a program imports: an executable line
// of a .pth file, and a sitecustomize or usercustomize module. A hijacked
// release ships one so that it runs even where its own package is never
// imported: on 2026-10-21 a release of litellm carried a .pth file that
// started a stealer at every Python start.
package starthook

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Hook is one start hook that no known package ships.
type Hook struct {
	// Path is the file or module folder that holds it.
	Path string
	// Package is the distribution whose RECORD installs it, as its
	// .dist-info folder names it, such as litellm-1.107.0, or empty when no
	// installed distribution names it.
	Package string
}

// knownHooks are the start hooks an environment carries with no release
// hijacked: each by its file, its executable line, and the distribution
// whose RECORD installs that file, so a release that writes one of these
// lines into a file of its own, or over the file of the package that ships
// it, is still found.
var knownHooks = []struct{ file, owner, line string }{
	// uv and virtualenv write it when they make the environment; no
	// distribution installs it.
	{file: "_virtualenv.pth", line: "import _virtualenv"},
	{file: "distutils-precedence.pth", owner: "setuptools", line: "import os; var = 'SETUPTOOLS_USE_DISTUTILS'; enabled = os.environ.get(var, 'local') == 'local'; enabled and __import__('_distutils_hack').add_shim();"},
	{file: "pywin32.pth", owner: "pywin32", line: "import pywin32_bootstrap"},
	{file: "a1_coverage.pth", owner: "coverage", line: `import sys; exec('import os\n\nif os.getenv("COVERAGE_PROCESS_START") or os.getenv("COVERAGE_PROCESS_CONFIG"):\n try:\n  import coverage\n except:\n  pass\n else:\n  coverage.process_startup(slug="pth")')`},
}

// Find returns the start hooks no known package ships in the Python
// environments of worktree: each folder at its root that holds pyvenv.cfg,
// and the .venv of each folder at its root, where uv makes one for a project
// in a subfolder. A worktree with neither costs one listing of its root and
// two looks at each of its entries.
func Find(worktree string) ([]Hook, error) {
	entries, err := os.ReadDir(worktree)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hooks []Hook
	for _, entry := range entries {
		// A linked dependency folder is a junction, which is no directory
		// entry, and is looked into all the same.
		for _, environment := range []string{filepath.Join(worktree, entry.Name()), filepath.Join(worktree, entry.Name(), ".venv")} {
			if _, err := os.Lstat(filepath.Join(environment, "pyvenv.cfg")); err != nil {
				continue
			}
			sites, _ := filepath.Glob(filepath.Join(environment, "lib", "python*", "site-packages"))
			for _, site := range append([]string{filepath.Join(environment, "Lib", "site-packages")}, sites...) {
				found, err := siteHooks(site)
				if err != nil {
					return nil, err
				}
				hooks = append(hooks, found...)
			}
		}
	}
	return hooks, nil
}

// siteHooks are the start hooks of one site-packages folder, which Python
// reads at start: each .pth file there, and a customize module there or in a
// folder a .pth file's path line adds.
func siteHooks(site string) ([]Hook, error) {
	entries, err := os.ReadDir(site)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hooks := customizeModules(site, entries)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".pth") {
			continue
		}
		data, err := fsx.ReadFile(filepath.Join(site, name))
		if err != nil {
			return nil, err
		}
		isHook := false
		// Python reads a .pth file with universal newlines, and from 3.13
		// drops a byte order mark first.
		for _, line := range strings.FieldsFunc(strings.TrimPrefix(string(data), string(rune(0xFEFF))), func(r rune) bool { return r == '\n' || r == '\r' }) {
			switch {
			case strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "":
			case strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "import\t"):
				isHook = isHook || !known(site, entries, name, strings.TrimRight(line, " \t"))
			default:
				added := strings.TrimRight(line, " \t")
				if !filepath.IsAbs(added) {
					added = filepath.Join(site, added)
				}
				if listed, err := os.ReadDir(added); err == nil {
					hooks = append(hooks, customizeModules(added, listed)...)
				}
			}
		}
		if isHook {
			hooks = append(hooks, Hook{Path: filepath.Join(site, name)})
		}
	}
	for index := range hooks {
		if relative, err := filepath.Rel(site, hooks[index].Path); err == nil && !strings.HasPrefix(relative, "..") {
			hooks[index].Package = owner(site, entries, filepath.ToSlash(relative))
		}
	}
	return hooks, nil
}

// customizeModules are the sitecustomize and usercustomize modules of one
// folder, in any form Python imports: a source file, compiled code or a
// package folder.
func customizeModules(folder string, entries []os.DirEntry) []Hook {
	var hooks []Hook
	for _, entry := range entries {
		stem, _, _ := strings.Cut(entry.Name(), ".")
		if strings.EqualFold(stem, "sitecustomize") || strings.EqualFold(stem, "usercustomize") {
			hooks = append(hooks, Hook{Path: filepath.Join(folder, entry.Name())})
		}
	}
	return hooks
}

// known reports whether line of the .pth file named file is a start hook a
// known package ships, from that package: one of knownHooks, or the finder
// setuptools writes for a project installed editable from a folder, which
// its distribution's direct_url.json says it is.
func known(site string, entries []os.DirEntry, file, line string) bool {
	for _, hook := range knownHooks {
		if file == hook.file && line == hook.line {
			return hook.owner == "" || owner(site, distributions(entries, hook.owner+"-"), file) != ""
		}
	}
	distribution, isEditable := strings.CutPrefix(strings.TrimSuffix(file, ".pth"), "__editable__.")
	module, rest, _ := strings.Cut(strings.TrimPrefix(line, "import "), "; ")
	if !isEditable || !strings.HasPrefix(module, "__editable___") || !strings.HasSuffix(module, "_finder") || rest != module+".install()" {
		return false
	}
	if owner(site, distributions(entries, distribution+".dist-info"), file) == "" {
		return false
	}
	data, err := fsx.ReadFile(filepath.Join(site, distribution+".dist-info", "direct_url.json"))
	var direct struct {
		DirInfo struct {
			Editable bool `json:"editable"`
		} `json:"dir_info"`
	}
	return err == nil && json.Unmarshal(data, &direct) == nil && direct.DirInfo.Editable
}

// distributions are the .dist-info folders among entries whose name starts
// with prefix, without regard to case. A folder's name spells its
// distribution's with underscores for dashes, so a prefix that ends with the
// dash before a version names one distribution.
func distributions(entries []os.DirEntry, prefix string) []os.DirEntry {
	return slices.DeleteFunc(slices.Clone(entries), func(entry os.DirEntry) bool {
		name := strings.ToLower(entry.Name())
		return !entry.IsDir() || !strings.HasPrefix(name, strings.ToLower(prefix)) || !strings.HasSuffix(name, ".dist-info")
	})
}

// owner is the distribution among the .dist-info folders of entries whose
// RECORD installs relative, a path under site written with slashes, or
// empty when none does.
func owner(site string, entries []os.DirEntry, relative string) string {
	for _, entry := range distributions(entries, "") {
		data, err := fsx.ReadFile(filepath.Join(site, entry.Name(), "RECORD"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			path, _, _ := strings.Cut(line, ",")
			if path == relative || strings.HasPrefix(path, relative+"/") {
				return strings.TrimSuffix(entry.Name(), ".dist-info")
			}
		}
	}
	return ""
}
