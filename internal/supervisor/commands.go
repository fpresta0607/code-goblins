package supervisor

import (
	"cmp"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// commandSession is the session a request for commands names: the harness
// that publishes them, and the folder that harness runs in.
type commandSession struct {
	harness string
	folder  string
}

// commandsRefused is why a request names no session whose commands the board
// reads, with the status it answers.
type commandsRefused struct {
	status int
	reason string
}

func (refused commandsRefused) Error() string { return refused.reason }

// command is one entry of the composer's "/" list: the name the harness runs
// it by, what it does, and where it comes from, "builtin" for one the harness
// builds in, "command" for a command file or prompt template, and "skill".
type command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// commands answers the slash commands one session's harness offers it: its
// own command files and skills, read for that harness alone, from the
// session's own folder and the user's configuration of that same harness,
// then the harness's built-in commands, as the harness's own source lists
// them, never one harness's commands for another.
func (h *HTTP) commands(w http.ResponseWriter, r *http.Request) {
	session, ok := h.boardSession(w, r.URL.Query(), "commands")
	if !ok {
		return
	}
	respond(w, http.StatusOK, struct {
		Commands []command `json:"commands"`
	}{harnessCommands(session, harnessmap.Find(os.Getenv, userHome(h)))})
}

// boardSession reads the session a board request for one of its lists names,
// and answers the refusal itself when the request carries no board token or
// names no session the board reads.
func (h *HTTP) boardSession(w http.ResponseWriter, query url.Values, what string) (commandSession, bool) {
	if subtle.ConstantTimeCompare([]byte(query.Get("token")), []byte(h.Service.Instance)) != 1 {
		apiError(w, http.StatusForbidden, "Refresh the board before reading its "+what)
		return commandSession{}, false
	}
	session, err := h.commandSession(query)
	if err != nil {
		var refused commandsRefused
		if errors.As(err, &refused) {
			apiError(w, refused.status, refused.reason)
			return commandSession{}, false
		}
		apiError(w, http.StatusInternalServerError, "The session's "+what+" cannot be read")
		return commandSession{}, false
	}
	return session, true
}

// userHome is the user's home, where every harness keeps the configuration it
// is read from: the supervisor's own record of it, or this machine's when no
// reader was given one.
func userHome(h *HTTP) string {
	if h.Service.Options.Tree != nil && h.Service.Options.Tree.Home != "" {
		return h.Service.Options.Tree.Home
	}
	home, _ := os.UserHomeDir()
	return home
}

// commandSession reads the session a request names: a task's goblin of the
// generation the request names, or the CFO in the terminal it names. It reads
// the fleet's own record of each session, never a folder the browser names,
// so only a session's own folder and the user's configuration are ever read.
func (h *HTTP) commandSession(query url.Values) (commandSession, error) {
	if query.Has("path") || query.Has("node") {
		return commandSession{}, commandsRefused{http.StatusBadRequest, "Select a task's current session or the CFO."}
	}
	stateDir := h.Service.Store.Home.State
	if want := query.Get("cfo"); want != "" {
		cfo := readCFOState(stateDir)
		switch {
		case cfo.terminal == "":
			return commandSession{}, commandsRefused{http.StatusNotFound, "No CFO is running in a native terminal."}
		case want != "1" && want != cfo.terminal:
			return commandSession{}, commandsRefused{http.StatusConflict, "The CFO runs in another terminal now. Open the CFO again."}
		case query.Get("session") != "" && query.Get("session") != strings.TrimPrefix(cfo.session, cfo.harness+"/"):
			return commandSession{}, commandsRefused{http.StatusConflict, "The CFO is in another session now."}
		}
		// A CFO that has not registered yet names no harness, and starts as
		// Claude Code unless the board started it as another.
		return commandSession{harness: cmp.Or(cfo.harness, "claude"), folder: h.Service.Store.Home.Root}, nil
	}
	task := query.Get("task")
	if state.ValidTaskID(task) != nil {
		return commandSession{}, commandsRefused{http.StatusBadRequest, "Select a task's current session or the CFO."}
	}
	meta, err := state.ReadTaskMeta(stateDir, task)
	switch {
	case err != nil:
		return commandSession{}, commandsRefused{http.StatusNotFound, "Task metadata unavailable"}
	case query.Get("generation") != "" && query.Get("generation") != meta.SpawnGen:
		return commandSession{}, commandsRefused{http.StatusConflict, "This task restarted or was replaced. Select its current session."}
	}
	if selected := query.Get("session"); selected != "" {
		owned, err := fleettree.OwnedSession(stateDir, meta)
		if err != nil || selected != owned {
			return commandSession{}, commandsRefused{http.StatusConflict, "Select this task's current session."}
		}
	}
	return commandSession{harness: meta.Harness, folder: meta.Worktree}, nil
}

// harnessCommands reads the commands the session's harness offers it: first
// its own, as that harness offers them, from where the user's configuration
// of that harness keeps them: Claude Code's command files and skills, and
// pi's prompt templates and skills. Codex reads no command files of its own,
// and its skills are mentions rather than commands. Then the harness's
// built-in commands, but for one the session's own command of that name
// replaces.
func harnessCommands(session commandSession, roots harnessmap.Map) []command {
	root, _ := roots.Harness(session.harness)
	var own []command
	switch session.harness {
	case "claude":
		own = append(own, claudeCommands(filepath.Join(session.folder, ".claude"))...)
		if root.Present {
			own = append(own, claudeCommands(root.Path)...)
		}
	case "pi":
		own = append(own, piCommands(session.folder, root, roots.SharedSkills)...)
	}
	own = uniqueSorted(own)
	var builtins []command
	for name, description := range builtinCommands[session.harness] {
		if !slices.ContainsFunc(own, func(entry command) bool { return entry.Name == name }) {
			builtins = append(builtins, command{Name: name, Description: description, Source: "builtin"})
		}
	}
	slices.SortFunc(builtins, func(a, b command) int { return strings.Compare(a.Name, b.Name) })
	return append(own, builtins...)
}

// claudeCommands reads the commands and skills Claude Code offers under one
// folder: commands/*.md, whose files in a subfolder are namespaced with a
// colon as Claude Code offers them, and skills/*/SKILL.md.
func claudeCommands(root string) []command {
	return append(commandFiles(filepath.Join(root, "commands"), ""), skillCommands(filepath.Join(root, "skills"), false)...)
}

// commandFiles reads the .md command files under dir as the commands they
// are offered as, each in a subfolder behind its own name and a colon.
func commandFiles(dir, namespace string) []command {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var commands []command
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		switch {
		case entry.IsDir():
			commands = append(commands, commandFiles(filepath.Join(dir, entry.Name()), namespace+entry.Name()+":")...)
		case strings.EqualFold(filepath.Ext(entry.Name()), ".md"):
			if fields, err := commandFrontmatter(filepath.Join(dir, entry.Name())); err == nil && (fields.UserInvocable == nil || *fields.UserInvocable) {
				commands = append(commands, command{Name: namespace + name, Description: fields.Description, Source: "command"})
			}
		}
	}
	return commands
}

// skillCommands reads the skills kept under dir, each named for its folder:
// one for each folder holding a SKILL.md, at any depth when recursive and
// among the folder's own children otherwise, as Claude Code and pi read them.
func skillCommands(dir string, recursive bool) []command {
	return readSkillCommands(dir, recursive, true, map[string]bool{}, nil)
}

func readSkillCommands(dir string, recursive, isRoot bool, visited map[string]bool, isEnabled func(string) bool) []command {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || visited[canonical] {
		return nil
	}
	visited[canonical] = true
	if recursive {
		if fields, err := commandFrontmatter(filepath.Join(dir, "SKILL.md")); err == nil {
			if strings.TrimSpace(fields.Description) != "" && (isEnabled == nil || isEnabled(filepath.Join(dir, "SKILL.md"))) {
				return []command{{Name: cmp.Or(fields.Name, filepath.Base(dir)), Description: fields.Description, Source: "skill"}}
			}
			return nil
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var skills []command
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}
		folder := filepath.Join(dir, entry.Name())
		info, err := os.Stat(folder)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if recursive {
				skills = append(skills, readSkillCommands(folder, true, false, visited, isEnabled)...)
			} else if fields, err := commandFrontmatter(filepath.Join(folder, "SKILL.md")); err == nil && (fields.UserInvocable == nil || *fields.UserInvocable) {
				skills = append(skills, command{Name: cmp.Or(fields.Name, entry.Name()), Description: fields.Description, Source: "skill"})
			}
		} else if recursive && isRoot && strings.HasSuffix(entry.Name(), ".md") && (isEnabled == nil || isEnabled(folder)) {
			if fields, err := commandFrontmatter(folder); err == nil && strings.TrimSpace(fields.Description) != "" {
				skills = append(skills, command{Name: cmp.Or(fields.Name, filepath.Base(dir)), Description: fields.Description, Source: "skill"})
			}
		}
	}
	return skills
}

type commandFields struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	UserInvocable *bool  `yaml:"user-invocable"`
}

func commandFrontmatter(path string) (commandFields, error) {
	var fields commandFields
	data, err := fsx.ReadFile(path)
	if err != nil {
		return fields, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if body, hasHeader := strings.CutPrefix(text, "---\n"); hasHeader {
		header, _, hasEnd := strings.Cut(body, "\n---")
		if !hasEnd {
			return fields, errors.New("unterminated command frontmatter")
		}
		var metadata struct {
			Name          yaml.Node `yaml:"name"`
			Description   yaml.Node `yaml:"description"`
			UserInvocable *bool     `yaml:"user-invocable"`
		}
		err = yaml.Unmarshal([]byte(header), &metadata)
		if metadata.Name.Tag == "!!str" {
			fields.Name = metadata.Name.Value
		}
		if metadata.Description.Tag == "!!str" {
			fields.Description = metadata.Description.Value
		}
		fields.UserInvocable = metadata.UserInvocable
	}
	return fields, err
}

// promptCommands reads pi's prompt templates under dir, each direct .md child
// offered as the command its file name spells.
func promptCommands(dir string) []command {
	if info, err := os.Stat(dir); err == nil && info.Mode().IsRegular() {
		if strings.HasSuffix(dir, ".md") {
			if fields, err := commandFrontmatter(dir); err == nil {
				return []command{{Name: strings.TrimSuffix(filepath.Base(dir), ".md"), Description: fields.Description, Source: "command"}}
			}
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var prompts []command
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			prompts = append(prompts, promptCommands(filepath.Join(dir, entry.Name()))...)
		}
	}
	return prompts
}

// piSkill offers a skill as the command pi offers it by.
func piSkill(skill command) command {
	skill.Name = "skill:" + skill.Name
	return skill
}

// piCommands reads the commands pi publishes for a session running in folder:
// the prompt templates and skills in the session's own project configuration,
// in the user's agent folder, and in the shared skills folder pi reads for
// itself. A skill is offered as the command pi offers it by, and none at all
// while pi's own settings turn skill commands off.
func piCommands(folder string, root harnessmap.Root, sharedSkills string) []command {
	user, err := readPiCommandSettings(root.Path)
	if err != nil {
		return nil
	}
	project := piCommandSettings{}
	isTrusted := piCommandProjectTrusted(folder, root.Path, user.DefaultProjectTrust)
	if isTrusted {
		project, err = readPiCommandSettings(filepath.Join(folder, ".pi"))
		if err != nil {
			return nil
		}
	}
	isSkillEnabled := true
	for _, setting := range []*bool{user.EnableSkillCommands, project.EnableSkillCommands} {
		if setting != nil {
			isSkillEnabled = *setting
		}
	}
	profile := filepath.Dir(filepath.Dir(sharedSkills))
	var commands []command
	for _, scope := range []struct {
		base     string
		settings piCommandSettings
	}{{root.Path, user}, {filepath.Join(folder, ".pi"), project}} {
		if scope.base != root.Path && !isTrusted {
			continue
		}
		for _, path := range piResourcePaths(scope.base, "prompts", scope.settings.Prompts, profile) {
			commands = append(commands, promptCommands(path)...)
		}
		if isSkillEnabled {
			isEnabled := func(path string) bool { return piResourceEnabled(path, scope.base, scope.settings.Skills, profile) }
			for _, path := range piResourcePaths(scope.base, "skills", scope.settings.Skills, profile) {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					for _, skill := range readSkillCommands(path, true, true, map[string]bool{}, isEnabled) {
						commands = append(commands, piSkill(skill))
					}
				} else if fields, err := commandFrontmatter(path); err == nil && strings.TrimSpace(fields.Description) != "" {
					commands = append(commands, piSkill(command{Name: cmp.Or(fields.Name, filepath.Base(filepath.Dir(path))), Description: fields.Description, Source: "skill"}))
				}
			}
		}
	}
	if isSkillEnabled {
		isUserEnabled := func(path string) bool {
			return piResourceEnabled(path, filepath.Dir(sharedSkills), user.Skills, profile)
		}
		for _, skill := range readSkillCommands(sharedSkills, true, false, map[string]bool{}, isUserEnabled) {
			commands = append(commands, piSkill(skill))
		}
		if isTrusted {
			for current := folder; ; current = filepath.Dir(current) {
				base := filepath.Join(current, ".agents")
				isProjectEnabled := func(path string) bool { return piResourceEnabled(path, base, project.Skills, profile) }
				for _, skill := range readSkillCommands(filepath.Join(base, "skills"), true, false, map[string]bool{}, isProjectEnabled) {
					commands = append(commands, piSkill(skill))
				}
				if _, err := os.Stat(filepath.Join(current, ".git")); err == nil || filepath.Dir(current) == current {
					break
				}
			}
		}
	}
	return commands
}

type piCommandSettings struct {
	Prompts             []string `json:"prompts"`
	Skills              []string `json:"skills"`
	EnableSkillCommands *bool    `json:"enableSkillCommands"`
	DefaultProjectTrust string   `json:"defaultProjectTrust"`
}

func readPiCommandSettings(dir string) (piCommandSettings, error) {
	var settings piCommandSettings
	data, err := fsx.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &settings)
	}
	return settings, err
}

// Only persisted trust is knowable outside pi. A session-only choice is not
// inferred and no project trust file is changed by discovery.
func piCommandProjectTrusted(folder, agentDir, policy string) bool {
	data, err := fsx.ReadFile(filepath.Join(agentDir, "trust.json"))
	trust := map[string]*bool{}
	if err == nil {
		if json.Unmarshal(data, &trust) != nil {
			return false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	canonical, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return false
	}
	for current := canonical; ; current = filepath.Dir(current) {
		for path, decision := range trust {
			if decision != nil && strings.EqualFold(filepath.Clean(path), current) {
				return *decision
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return policy == "always"
}

func piResourcePaths(base, kind string, configured []string, profile string) []string {
	var paths []string
	entries, _ := os.ReadDir(filepath.Join(base, kind))
	for _, entry := range entries {
		paths = append(paths, filepath.Join(base, kind, entry.Name()))
	}
	for _, path := range configured {
		if path == "" || strings.HasPrefix(path, "!") || strings.HasPrefix(path, "-") {
			continue
		}
		paths = append(paths, piResourcePath(base, strings.TrimPrefix(path, "+"), profile))
	}
	return slices.DeleteFunc(paths, func(path string) bool { return !piResourceEnabled(path, base, configured, profile) })
}

func piResourcePath(base, path, profile string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(profile, path[2:])
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(base, path)
	}
	return filepath.Clean(path)
}

func piResourceEnabled(path, base string, configured []string, profile string) bool {
	candidates := []string{path}
	if filepath.Base(path) == "SKILL.md" {
		candidates = append(candidates, filepath.Dir(path))
	}
	isExcluded, isIncluded, isForcedOut := false, false, false
	for _, rule := range configured {
		if len(rule) < 2 {
			continue
		}
		for _, candidate := range candidates {
			switch rule[0] {
			case '+', '-':
				if strings.EqualFold(piResourcePath(base, rule[1:], profile), candidate) {
					if rule[0] == '+' {
						isIncluded = true
					} else {
						isForcedOut = true
					}
				}
			case '!':
				relative, err := filepath.Rel(base, candidate)
				if err != nil {
					continue
				}
				for _, name := range []string{relative, filepath.Base(candidate), candidate} {
					if piResourceGlob(strings.Split(filepath.ToSlash(rule[1:]), "/"), strings.Split(filepath.ToSlash(name), "/")) {
						isExcluded = true
					}
				}
			}
		}
	}
	return !isForcedOut && (!isExcluded || isIncluded)
}

func piResourceGlob(pattern, parts []string) bool {
	if len(pattern) == 0 {
		return len(parts) == 0
	}
	if pattern[0] == "**" {
		for consumed := 0; consumed <= len(parts); consumed++ {
			if piResourceGlob(pattern[1:], parts[consumed:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	matched, err := filepath.Match(pattern[0], parts[0])
	return err == nil && matched && piResourceGlob(pattern[1:], parts[1:])
}

// uniqueSorted sorts commands by name and keeps the first of each name.
func uniqueSorted(commands []command) []command {
	commands = append([]command{}, slices.DeleteFunc(commands, func(entry command) bool { return entry.Name == "" })...)
	slices.SortStableFunc(commands, func(a, b command) int { return strings.Compare(a.Name, b.Name) })
	return slices.CompactFunc(commands, func(a, b command) bool { return a.Name == b.Name })
}
