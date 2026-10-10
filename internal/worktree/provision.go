package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ProvisionResult reports what one provisioning pass did, so spawn can merge
// the environment redirects into the launch and name what it linked, ran, and
// dropped.
type ProvisionResult struct {
	// Env redirects shared read-only caches into the pane environment.
	Env map[string]string
	// MCPConfig is where the token-authenticated subset of the project's
	// .mcp.json was materialized, under the task's temporary directory and
	// never inside the checkout. It is empty when nothing qualified. This is
	// the only path a harness may be handed: the worktree's own .mcp.json can
	// be the project's unfiltered file or one a goblin wrote itself.
	MCPConfig string
	// MCPProjectTracked reports that the project tracks .mcp.json in git, so
	// the filtered copy was withheld from the worktree to keep it clean and a
	// harness that reads its working directory still sees the project's own
	// unfiltered file.
	MCPProjectTracked bool
	// MCPWorktreeOccupied reports that the worktree root already held an
	// untracked .mcp.json that provisioning did not write, so the filtered
	// copy was withheld and a harness reading its working directory sees that
	// file instead. Never silent: an occupied path must not be mistaken for
	// the goblin using the filtered configuration.
	MCPWorktreeOccupied bool
	// MCPDropped names the OAuth-only servers withheld from the goblin.
	MCPDropped []string
	// MCPTokenUnset names, as "server (VARIABLE)", the servers withheld
	// because the bearerTokenEnvVar they authenticate with is not in the
	// goblin's environment.
	MCPTokenUnset []string
	// Linked names the config entries shared from the primary checkout.
	Linked []string
	// EnvHeld names the root-level env files the checkout holds that the
	// worktree was not given, so the spawn can say so by name: a dev server
	// that will not start is no way to learn it.
	EnvHeld []string
	// Install is the project's install commands, in order, which the goblin
	// runs in the worktree as its first step; empty when the project needs
	// none. See installCommands.
	Install []string
}

// Provision makes one freshly acquired worktree ready for its goblin: it
// shares the declared (or default) config files, materializes the
// token-authenticated subset of the project's .mcp.json, links dependencies
// for strategy link, and names the install commands for strategy install.
// Everything it places inside the worktree, and everything those commands
// will, is first registered in the clone's info/exclude when the project does
// not already ignore it, so the goblin's git status stays clean and cleanup's
// dirty-worktree refusal keeps meaning uncommitted goblin work. hasVariable
// reports whether a variable will be set in the goblin's environment, which
// decides whether a server that authenticates by bearerTokenEnvVar can be
// handed to it.
func (s Service) Provision(ctx context.Context, project, worktreePath, taskTmp string, hasVariable func(name string) bool) (ProvisionResult, error) {
	if s.Commands == nil {
		return ProvisionResult{}, errors.New("worktree: command runner is required for provisioning")
	}
	if strings.TrimSpace(taskTmp) == "" {
		return ProvisionResult{}, errors.New("worktree: task temporary directory is required for provisioning")
	}
	manifest, err := Resolve(s.DataDir, project)
	if err != nil {
		return ProvisionResult{}, err
	}
	git := RunnerGit{Commands: s.Commands, Sleep: s.Sleep}
	result := ProvisionResult{Env: manifest.Env}

	for _, name := range manifest.Link {
		linked, err := s.shareEntry(ctx, git, project, worktreePath, name, true)
		if err != nil {
			return result, err
		}
		if linked {
			result.Linked = append(result.Linked, name)
		}
	}
	if result.EnvHeld, err = envHeld(project, worktreePath); err != nil {
		return result, err
	}

	switch manifest.Dependencies.Strategy {
	case StrategyNone:
	case StrategyLink:
		for _, name := range manifest.Dependencies.Paths {
			if _, err := os.Stat(filepath.Join(project, name)); errors.Is(err, os.ErrNotExist) {
				return result, fmt.Errorf("worktree: dependency path %q does not exist in the primary checkout; nothing to link", name)
			}
			linked, err := s.shareEntry(ctx, git, project, worktreePath, name, false)
			if err != nil {
				return result, err
			}
			if linked {
				result.Linked = append(result.Linked, name)
			}
		}
	case StrategyInstall:
		install, err := s.installCommands(ctx, git, manifest, worktreePath)
		if err != nil {
			return result, err
		}
		result.Install = install
	}

	mcp, err := s.materializeMCP(ctx, git, project, worktreePath, taskTmp, hasVariable)
	result.MCPConfig = mcp.config
	result.MCPProjectTracked = mcp.projectTracked
	result.MCPWorktreeOccupied = mcp.worktreeOccupied
	result.MCPDropped = mcp.dropped
	result.MCPTokenUnset = mcp.unset
	if err != nil {
		return result, err
	}
	return result, nil
}

// shareEntry gives the worktree one of the primary checkout's root-level
// entries. A config file becomes the worktree's own read-only copy. It used
// to be a hard link, one file under two names, so a goblin that edited .env in
// its worktree edited the Overlord's real file in place, and a write to the
// copy is refused so that no tool believes it changed the project's file. A
// dependency file is still hardlinked, and a directory junctioned: both are
// shared on purpose. A junction is removed as a link by Return before Git
// ever sees it, because Git for Windows would otherwise delete the primary
// checkout's directory through it. A missing source is skipped: the defaults
// name config a project may simply not have. An occupied destination returns
// errDestinationOccupied; Provision decides whether that is fatal.
func (s Service) shareEntry(ctx context.Context, git RunnerGit, project, worktreePath, name string, isConfig bool) (bool, error) {
	source := filepath.Join(project, name)
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("worktree: inspect %q: %w", source, err)
	}
	destination := filepath.Join(worktreePath, name)
	if _, err := os.Lstat(destination); err == nil {
		return false, fmt.Errorf("worktree: %q already exists in the worktree; refusing to cover it with a shared link: %w", name, errDestinationOccupied)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("worktree: inspect worktree %q: %w", name, err)
	}
	if err := s.ensureIgnored(ctx, git, worktreePath, name); err != nil {
		return false, err
	}
	if info.IsDir() {
		if err := s.junction(ctx, source, destination); err != nil {
			return false, err
		}
		return true, nil
	}
	if isConfig {
		if err := copyReadOnly(source, destination); err != nil {
			return false, fmt.Errorf("worktree: copy %q into the worktree: %w", name, err)
		}
		return true, nil
	}
	if err := os.Link(source, destination); err != nil {
		return false, fmt.Errorf("worktree: hardlink %q into the worktree: %w", name, err)
	}
	return true, nil
}

// copyReadOnly writes destination as a read-only copy of source. The copy is
// written beside its place and renamed into it, so a destination that is a
// hard link to source is replaced as a name and never written through, and a
// copy that fails leaves what was there.
func copyReadOnly(source, destination string) error {
	data, err := fsx.ReadFile(source)
	if err != nil {
		return err
	}
	// A staged copy an interrupted run left is read-only, so it is removed
	// before it is written again, and whatever this run leaves staged goes
	// with its failure: an untracked file would make the worktree read dirty.
	staged := destination + ".cfo-copy"
	unstage := func() error {
		if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := unstage(); err != nil {
		return err
	}
	if err := os.WriteFile(staged, data, 0o400); err != nil {
		return errors.Join(err, unstage())
	}
	if err := os.Rename(staged, destination); err != nil {
		return errors.Join(err, unstage())
	}
	return nil
}

// envHeld names the root-level env files the checkout holds and the
// worktree does not: .env and every .env.<suffix>. One the manifest names was
// shared and one the repository tracks is checked out, so what is left is
// what the worktree was not given.
func envHeld(project, worktreePath string) ([]string, error) {
	entries, err := os.ReadDir(project)
	if err != nil {
		return nil, fmt.Errorf("worktree: list the checkout's env files: %w", err)
	}
	var held []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (name != ".env" && !strings.HasPrefix(name, ".env.")) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(worktreePath, name)); errors.Is(err, os.ErrNotExist) {
			held = append(held, name)
		}
	}
	return held, nil
}

// OwnConfig ends every hard link worktreePath still holds to a file of its
// primary checkout, which is how a build before this one shared config files:
// one file under two names, so an edit in the worktree edited the checkout's
// own file. A file the project's manifest names in link becomes the
// worktree's own read-only copy. One no manifest names is removed from the
// worktree, since a worktree is given only what a manifest names, and the
// checkout's file stays where it is. It names what it copied and what it
// removed.
//
// It finds the links by what they are, a root-level file of the worktree that
// is the checkout's file of the same name, and not by a list of names, so a
// link made under any name is found. A file the repository tracks is the
// worktree's own, and a declared dependency path is linked on purpose: both
// are left. A relaunch calls it once the task's last harness has ended, so a
// goblin already running stops holding the Overlord's own file at its next
// terminal.
func (s Service) OwnConfig(project, worktreePath string) (copied, removed []string, err error) {
	manifest, err := Resolve(s.DataDir, project)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(worktreePath)
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: list %q for links to the checkout: %w", worktreePath, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || slices.Contains(manifest.Dependencies.Paths, name) {
			continue
		}
		source, destination := filepath.Join(project, name), filepath.Join(worktreePath, name)
		sourceInfo, err := os.Stat(source)
		if err != nil || sourceInfo.IsDir() {
			continue
		}
		destinationInfo, err := os.Lstat(destination)
		if err != nil || !os.SameFile(sourceInfo, destinationInfo) {
			continue
		}
		if !slices.Contains(manifest.Link, name) {
			if err := os.Remove(destination); err != nil {
				return copied, removed, fmt.Errorf("worktree: remove the worktree's link to the checkout's %q: %w", name, err)
			}
			removed = append(removed, name)
			continue
		}
		if err := copyReadOnly(source, destination); err != nil {
			return copied, removed, fmt.Errorf("worktree: give the worktree its own copy of %q: %w", name, err)
		}
		copied = append(copied, name)
	}
	return copied, removed, nil
}

// errDestinationOccupied marks a share whose worktree path already exists.
var errDestinationOccupied = errors.New("destination occupied")

// junction links a directory through cmd's mklink /J, which needs no
// privilege elevation on Windows, unlike directory symlinks.
func (s Service) junction(ctx context.Context, source, destination string) error {
	result, err := s.Commands.Run(ctx, execx.Request{Name: "cmd", Args: []string{"/c", "mklink", "/J", destination, source}})
	if err != nil {
		return fmt.Errorf("worktree: junction %q: %w", destination, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("worktree: junction %q: %s", destination, commandFailure("mklink /J", result).Error())
	}
	return nil
}

// installCommands names the project's own installer for the goblin to run in
// the worktree as its first step, against the shared package caches its
// terminal's environment names. The installer is detected from the lockfile
// unless the manifest overrides it; a Go-only project needs nothing because
// the module cache is already shared per user.
//
// Provisioning never runs it. An install writes tens of thousands of files,
// which under on-access scanning took one spawn over 30 minutes, and a spawn
// holds the home's spawn lock until its terminal is up, so every other start
// and resume waited behind that one install. In the goblin's own terminal it
// holds up nothing but that goblin, its output is on the goblin's screen, and
// the goblin is the one that can repair a failing lockfile.
//
// What the commands will create is registered as ignored now, while
// provisioning still owns the clone's info/exclude, so the goblin's git status
// stays clean once they have run.
func (s Service) installCommands(ctx context.Context, git RunnerGit, manifest Manifest, worktreePath string) ([]string, error) {
	commands := InstallPlan(manifest, func(name string) bool {
		_, err := os.Stat(filepath.Join(worktreePath, name))
		return err == nil
	})
	for _, output := range InstallOutputs(commands) {
		if err := s.ensureIgnored(ctx, git, worktreePath, output); err != nil {
			return nil, err
		}
	}
	return commands, nil
}

// InstallPlan returns the install commands a goblin runs as its first step
// in a worktree under the install strategy: the manifest's own commands, or
// the one the lockfile at the worktree's root names. Has reports whether
// the worktree holds a file at its root. It is what a spawn names and what
// `cfo project check` reads, so the two cannot disagree about what a
// worktree will hold.
func InstallPlan(manifest Manifest, has func(name string) bool) (commands []string) {
	for _, command := range manifest.Dependencies.Install {
		if strings.TrimSpace(command) != "" {
			commands = append(commands, strings.TrimSpace(command))
		}
	}
	if len(commands) == 0 {
		if detected := detectInstallCommand(has); detected != "" {
			commands = []string{detected}
		}
	}
	return commands
}

// detectInstallCommand maps a lockfile to the install command that honors it.
// Every command is pinned to its lockfile, because a worktree only holds
// tracked files and a rewritten lockfile is uncommitted work no goblin
// authored - which Return then refuses to remove, stranding the worktree.
//
// uv takes --locked rather than --frozen deliberately: --frozen installs from
// the lockfile without checking it, hiding drift, while --locked asserts the
// lockfile is up to date and exits non-zero when it is not. That is the exact
// analogue of pnpm --frozen-lockfile and npm ci, so all four detected
// installers now fail on drift instead of resolving it. The goblin runs the
// command, so drift reaches it as a named failure on its own screen rather
// than as a silently rewritten uv.lock.
func detectInstallCommand(has func(name string) bool) string {
	for _, candidate := range []struct{ lockfile, command string }{
		{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
		{"package-lock.json", "npm ci"},
		{"yarn.lock", "yarn install --frozen-lockfile"},
		{"uv.lock", "uv sync --locked"},
	} {
		if has(candidate.lockfile) {
			return candidate.command
		}
	}
	return ""
}

// InstallOutputs names the directories a set of install commands
// materializes, so they can be excluded when the project itself does not
// ignore them, and so `cfo project check` knows what a worktree will hold.
func InstallOutputs(commands []string) []string {
	outputs := map[string]bool{}
	for _, command := range commands {
		switch fields := strings.Fields(command); {
		case len(fields) == 0:
		case fields[0] == "uv":
			outputs[".venv"] = true
		case fields[0] == "pnpm" || fields[0] == "npm" || fields[0] == "yarn":
			outputs["node_modules"] = true
		}
	}
	names := []string{}
	for name := range outputs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// mcpResult is what one MCP materialization pass produced.
type mcpResult struct {
	// config is the filtered configuration's path under the task's temporary
	// directory, empty when no server qualified.
	config string
	// projectTracked reports that the project commits .mcp.json, so the
	// worktree copy was skipped.
	projectTracked bool
	// worktreeOccupied reports that the worktree root already held a
	// .mcp.json provisioning did not write, so the copy was skipped.
	worktreeOccupied bool
	// dropped names the OAuth-only servers withheld from the goblin.
	dropped []string
	// unset names the servers withheld because their token variable is not
	// in the goblin's environment.
	unset []string
}

// materializeMCP writes the token-authenticated subset of the project's
// .mcp.json to the task's temporary directory. It is materialized outside the
// checkout, never linked and never reported from inside it: the project's own
// config can carry OAuth connectors (its operator completes those flows
// interactively), a linked copy would hand a goblin an authentication prompt
// it can never satisfy, and a path inside the worktree could later be the
// project's own file or one the goblin wrote. Claude is the only harness that
// receives that path, through --mcp-config; the codex adapter ignores
// LaunchSpec.MCPConfig, so a codex goblin uses the operator's own codex MCP
// configuration and this filter does not reach it.
//
// The same bytes are additionally copied to <worktree>/.mcp.json, because
// kimi has no config flag and reads the project-scoped .mcp.json from its
// working directory. That copy is written only where it is safe: a path that
// does not already exist and that the project does not track. Overwriting a
// tracked .mcp.json would leave the worktree permanently modified, which the
// return path refuses to remove, so a tracked file is left exactly as it is
// and reported instead.
//
// Trackedness is probed before anything is filtered, because the disclosure
// is about what the worktree already holds, not about what qualified. A
// project whose committed .mcp.json is entirely OAuth connectors materializes
// no filtered config at all, and that is exactly when a cwd-reading harness
// sees the most withheld servers.
func (s Service) materializeMCP(ctx context.Context, git RunnerGit, project, worktreePath, taskTmp string, hasVariable func(string) bool) (mcpResult, error) {
	data, err := fsx.ReadFile(filepath.Join(project, ".mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return mcpResult{}, nil
	}
	if err != nil {
		return mcpResult{}, fmt.Errorf("worktree: read project .mcp.json: %w", err)
	}
	filtered, _, dropped, unset, err := FilterMCPServers(data, hasVariable)
	if err != nil {
		return mcpResult{}, err
	}
	result := mcpResult{dropped: dropped, unset: unset}
	tracked, err := s.tracked(ctx, worktreePath, ".mcp.json")
	if err != nil {
		return result, err
	}
	result.projectTracked = tracked
	if filtered == nil {
		return result, nil
	}
	if err := os.MkdirAll(taskTmp, 0o755); err != nil {
		return result, fmt.Errorf("worktree: create task temporary directory: %w", err)
	}
	config := filepath.Join(taskTmp, "mcp.json")
	if err := fsx.AtomicWriteFile(config, filtered); err != nil {
		return result, fmt.Errorf("worktree: materialize goblin MCP configuration: %w", err)
	}
	result.config = config

	if tracked {
		return result, nil
	}
	if _, err := os.Lstat(filepath.Join(worktreePath, mcpFileName)); err == nil {
		result.worktreeOccupied = true
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("worktree: inspect worktree .mcp.json: %w", err)
	}
	if err := s.ensureIgnored(ctx, git, worktreePath, ".mcp.json"); err != nil {
		return result, err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(worktreePath, ".mcp.json"), filtered); err != nil {
		return result, fmt.Errorf("worktree: materialize goblin .mcp.json: %w", err)
	}
	return result, nil
}

// tracked reports whether the worktree has name in its index. Trackedness is
// what decides whether a path is safe to write, and only `git ls-files` can
// answer it: `git check-ignore` reports exit 1 for a tracked path exactly as
// it does for an unignored one, and no ignore rule ever applies to a file in
// the index. It fails closed: exit 1 is the untracked answer, but anything
// else means git could not answer, and an unanswerable probe must never
// resolve to the permissive reading that lets the write proceed.
func (s Service) tracked(ctx context.Context, worktreePath, name string) (bool, error) {
	result, err := s.Commands.Run(ctx, execx.Request{Dir: worktreePath, Name: "git", Args: []string{"ls-files", "--error-unmatch", "--", name}})
	if err != nil {
		return false, fmt.Errorf("worktree: check whether %q is tracked: %w", name, err)
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("worktree: check whether %q is tracked: %s", name, commandFailure("git ls-files --error-unmatch", result).Error())
	}
}

// ensureIgnored guarantees name is ignored inside the worktree: the project's
// own rules win when they cover it, otherwise it lands in the clone's
// info/exclude, the per-clone ignore file no tracked .gitignore has to change
// for.
func (s Service) ensureIgnored(ctx context.Context, git RunnerGit, worktreePath, name string) error {
	result, err := s.Commands.Run(ctx, execx.Request{Dir: worktreePath, Name: "git", Args: []string{"check-ignore", "-q", "--", name}})
	if err != nil {
		return fmt.Errorf("worktree: check ignore rules for %q: %w", name, err)
	}
	switch result.ExitCode {
	case 0:
		return nil
	case 1:
		return git.ensureExcluded(ctx, worktreePath, name)
	default:
		return fmt.Errorf("worktree: check ignore rules for %q: %s", name, commandFailure("git check-ignore", result).Error())
	}
}
