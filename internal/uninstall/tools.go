package uninstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// FetchedFile is the home's record of what the install fetched, which
// install.ps1 writes in the home's state folder, so the uninstall never
// guesses what was there before.
const FetchedFile = "fetched.json"

// Fetched is what the install fetched onto this machine.
type Fetched struct {
	// Tools are the tools the install installed because they were missing.
	Tools []FetchedTool `json:"tools"`
	// Skills are the skill folders the install added for those tools.
	Skills []string `json:"skills"`
	// Path are the entries the install added to the user's PATH.
	Path []string `json:"path"`
	// Places are the tools' own folders and files in the profile, such as
	// ~\.claude, that were not there before the first install.
	Places []string `json:"places"`
}

// FetchedTool is one tool the install fetched.
type FetchedTool struct {
	// Name is the command, such as codex.
	Name string `json:"name"`
	// Kind is how it was installed: winget, npm, npm-file, powershell or
	// release, as install.ps1 names its kinds.
	Kind string `json:"kind"`
	// Package is the winget id or the npm package.
	Package string `json:"package,omitempty"`
}

// toolNames are the tools by the names a person knows them by.
var toolNames = map[string]string{"git": "Git", "gh": "the GitHub CLI", "claude": "Claude Code", "codex": "Codex", "pi": "pi"}

func toolName(name string) string {
	if known, ok := toolNames[name]; ok {
		return known
	}
	return name
}

// ReadFetched reads the record in the home's state folder; a home whose
// install made none, as one from before the record, fetched nothing the
// uninstall knows of.
func ReadFetched(stateDir string) (Fetched, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, FetchedFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Fetched{}, nil
	}
	if err != nil {
		return Fetched{}, err
	}
	var fetched Fetched
	if err := json.Unmarshal(data, &fetched); err != nil {
		return Fetched{}, fmt.Errorf("uninstall: read what the install fetched, in %s: %w", FetchedFile, err)
	}
	return fetched, nil
}

// tools are what the install fetched: each tool, the skills and PATH entries
// it added for them, and the tools' own folders it made. They are left, and
// named, unless the person chose to remove them, since other programs may
// use them.
func (m Machine) tools(ctx context.Context, choices Choices) ([]Item, error) {
	fetched, err := ReadFetched(m.Home.State)
	if err != nil {
		return nil, err
	}
	action := Leave
	if choices.RemoveTools {
		action = Remove
	}
	var items []Item
	npmRoot := ""
	for _, tool := range fetched.Tools {
		if (tool.Kind == "npm" || tool.Kind == "npm-file") && npmRoot == "" {
			npmRoot = m.npmRoot(ctx)
		}
		where, others, isThere, remove, err := m.toolPlace(tool, npmRoot)
		if err != nil {
			return nil, err
		}
		if !isThere {
			continue
		}
		items = append(items, Item{Action: action, Group: GroupTools, What: toolName(tool.Name), Where: where, do: remove})
		// The rest of the tool goes with its removal, and each is named, so
		// what is left or removed is all there is of it.
		for _, other := range others {
			items = append(items, Item{Action: action, Group: GroupTools, What: toolName(tool.Name), Where: other})
		}
		if tool.Kind == "release" {
			tasks, err := m.daemonTasks(ctx, tool.Name)
			if err != nil {
				return nil, err
			}
			for _, task := range tasks {
				items = append(items, Item{Action: action, Group: GroupTools, What: tool.Name + "'s startup task", Where: task, do: func(ctx context.Context) error {
					_, err := m.Commands.Run(ctx, execx.Request{Name: "schtasks", Args: []string{"/delete", "/tn", task, "/f"}})
					return err
				}})
			}
		}
	}
	for _, skill := range fetched.Skills {
		if isThere, _ := there(skill); isThere {
			items = append(items, Item{Action: action, Group: GroupTools, What: "the skill " + filepath.Base(skill), Where: skill, do: func(context.Context) error { return fsx.RemoveAll(skill) }})
		}
	}
	for _, place := range fetched.Places {
		if isThere, _ := there(place); isThere {
			items = append(items, Item{Action: action, Group: GroupTools, What: "a place of the tools' own, " + filepath.Base(place), Where: place, Bytes: sizeOf(place), do: func(context.Context) error { return fsx.RemoveAll(place) }})
		}
	}
	if m.Install.Env != nil {
		raw, _, err := m.Install.Env.Get("Path")
		if err != nil {
			return nil, err
		}
		entries := strings.Split(raw, ";")
		for _, added := range fetched.Path {
			if slices.ContainsFunc(entries, func(entry string) bool { return strings.EqualFold(filepath.Clean(entry), filepath.Clean(added)) }) {
				items = append(items, Item{Action: action, Group: GroupTools, What: "its PATH entry " + added, Where: added, do: func(context.Context) error { return m.unpath(added) }})
			}
		}
	}
	return items, nil
}

// toolPlace is where a fetched tool is, the other paths of its own that are
// there, such as the commands npm made for it, whether it is still there,
// and how it is removed: by the installer that put it there where there is
// one.
func (m Machine) toolPlace(tool FetchedTool, npmRoot string) (string, []string, bool, func(context.Context) error, error) {
	run := func(name string, args ...string) func(context.Context) error {
		return func(ctx context.Context) error {
			result, err := m.Commands.Run(ctx, execx.Request{Name: name, Args: args})
			if err != nil {
				return err
			}
			if result.ExitCode != 0 {
				return fmt.Errorf("%s %s exited with code %d", name, strings.Join(args, " "), result.ExitCode)
			}
			return nil
		}
	}
	switch tool.Kind {
	case "npm", "npm-file":
		where := filepath.Join(npmRoot, filepath.FromSlash(tool.Package))
		isThere, _ := there(where)
		commands, err := npmCommands(where, npmRoot)
		if err != nil {
			return "", nil, false, nil, err
		}
		uninstall := run("npm.cmd", "uninstall", "--global", tool.Package)
		return where, commands, isThere, func(ctx context.Context) error {
			if err := uninstall(ctx); err != nil {
				return err
			}
			// A command npm did not take with the package runs nothing now.
			for _, command := range commands {
				if err := os.Remove(command); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			// npm leaves a scope's folder, such as @openai, once its last
			// package has gone.
			if scope := filepath.Dir(where); !strings.EqualFold(scope, filepath.Clean(npmRoot)) {
				removeIfEmpty(scope)
			}
			return nil
		}, nil
	case "winget":
		return tool.Package, nil, true, run("winget", "uninstall", "--exact", "--id", tool.Package, "--silent", "--accept-source-agreements", "--disable-interactivity"), nil
	case "powershell":
		// Claude Code's own installer keeps its program and versions here.
		program := filepath.Join(m.Profile, ".local", "bin", "claude.exe")
		isThere, _ := there(program)
		var others []string
		for _, place := range []string{filepath.Join(m.Profile, ".local", "share", "claude"), filepath.Join(m.Profile, ".local", "state", "claude"), filepath.Join(m.Profile, ".cache", "claude")} {
			if isOther, _ := there(place); isOther {
				others = append(others, place)
			}
		}
		return program, others, isThere, func(context.Context) error {
			for _, place := range append([]string{program}, others...) {
				if err := fsx.RemoveAll(place); err != nil {
					return err
				}
			}
			for _, folder := range []string{"bin", "share", "state", ""} {
				removeIfEmpty(filepath.Join(m.Profile, ".local", folder))
			}
			removeIfEmpty(filepath.Join(m.Profile, ".cache"))
			return nil
		}, nil
	case "release":
		folder := filepath.Join(m.LocalAppData, tool.Name)
		program := filepath.Join(folder, tool.Name+".exe")
		isThere, _ := there(program)
		return folder, nil, isThere, func(ctx context.Context) error {
			// Its daemon holds the program, so it stops first, with no update
			// check: no-mistakes runs a stale one as a program of its own in
			// the background, which would hold the program and write
			// ~\.no-mistakes again once both had gone.
			_, _ = m.Commands.Run(ctx, execx.Request{Name: program, Args: []string{"daemon", "stop"}, Env: append(os.Environ(), "NO_MISTAKES_NO_UPDATE_CHECK=1")})
			return fsx.RemoveAll(folder)
		}, nil
	}
	return tool.Name, nil, false, nil, nil
}

// npmCommands are the commands npm made for the package at where, in the
// folder above npm's root, such as codex, codex.cmd and codex.ps1, that are
// there. npm names them by the package's bin: each name it gives, or the
// package's own name, without its scope, for a bin of one program.
func npmCommands(where, npmRoot string) ([]string, error) {
	data, err := fsx.ReadFile(filepath.Join(where, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("uninstall: read which commands npm made for %s: %w", where, err)
	}
	var names []string
	var one string
	var many map[string]string
	switch {
	case len(manifest.Bin) == 0:
	case json.Unmarshal(manifest.Bin, &one) == nil:
		names = []string{path.Base(manifest.Name)}
	case json.Unmarshal(manifest.Bin, &many) == nil:
		names = slices.Sorted(maps.Keys(many))
	default:
		return nil, fmt.Errorf("uninstall: read which commands npm made for %s: its bin is neither a name nor a list of them", where)
	}
	var commands []string
	for _, name := range names {
		for _, suffix := range []string{"", ".cmd", ".ps1"} {
			command := filepath.Join(filepath.Dir(npmRoot), name+suffix)
			if isThere, _ := there(command); isThere {
				commands = append(commands, command)
			}
		}
	}
	return commands, nil
}

// daemonTasks are the scheduled tasks a fetched tool's daemon registers for
// itself, such as \no-mistakes-daemon-<id>, which go with the tool.
func (m Machine) daemonTasks(ctx context.Context, tool string) ([]string, error) {
	result, err := m.Commands.Run(ctx, execx.Request{Name: "schtasks", Args: []string{"/query", "/fo", "csv", "/nh"}})
	if err != nil {
		return nil, err
	}
	var tasks []string
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), ",")
		name = strings.Trim(name, `"`)
		if strings.HasPrefix(strings.ToLower(name), `\`+strings.ToLower(tool)+"-daemon-") && !slices.Contains(tasks, name) {
			tasks = append(tasks, name)
		}
	}
	return tasks, nil
}

// npmRoot is the folder npm keeps its global packages in, as npm itself
// says, which is %APPDATA%\npm\node_modules unless npm was told another.
func (m Machine) npmRoot(ctx context.Context) string {
	result, err := m.Commands.Run(ctx, execx.Request{Name: "npm.cmd", Args: []string{"root", "--global"}})
	if root := strings.TrimSpace(string(result.Stdout)); err == nil && result.ExitCode == 0 && root != "" && filepath.IsAbs(root) {
		return root
	}
	return filepath.Join(m.AppData, "npm", "node_modules")
}

// unpath takes entry off the user's PATH.
func (m Machine) unpath(entry string) error {
	raw, _, err := m.Install.Env.Get("Path")
	if err != nil {
		return err
	}
	var kept []string
	for _, existing := range strings.Split(raw, ";") {
		if existing != "" && !strings.EqualFold(filepath.Clean(existing), filepath.Clean(entry)) {
			kept = append(kept, existing)
		}
	}
	if err := m.Install.Env.Set("Path", strings.Join(kept, ";")); err != nil {
		return err
	}
	return m.Install.Env.Broadcast()
}
