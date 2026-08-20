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
	// MCPDropped names the OAuth-only servers withheld from the goblin.
	MCPDropped []string
	// Linked names the config entries shared from the primary checkout.
	Linked []string
	// Installed is the dependency command that ran, empty when none ran.
	Installed string
}

// Provision makes one freshly acquired worktree runnable as if it were the
// project: it shares the declared (or default) config files, materializes the
// token-authenticated subset of the project's .mcp.json, and provisions
// dependencies per the project's strategy. Everything it places inside the
// worktree is first registered in the clone's info/exclude when the project
// does not already ignore it, so the goblin's git status stays clean and
// cleanup's dirty-worktree refusal keeps meaning uncommitted goblin work.
func (s Service) Provision(ctx context.Context, project, worktreePath, taskTmp string) (ProvisionResult, error) {
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
		linked, err := s.shareEntry(ctx, git, project, worktreePath, name)
		if err != nil {
			return result, err
		}
		if linked {
			result.Linked = append(result.Linked, name)
		}
	}

	switch manifest.Dependencies.Strategy {
	case StrategyNone:
	case StrategyLink:
		for _, name := range manifest.Dependencies.Paths {
			if _, err := os.Stat(filepath.Join(project, name)); errors.Is(err, os.ErrNotExist) {
				return result, fmt.Errorf("worktree: dependency path %q does not exist in the primary checkout; nothing to link", name)
			}
			linked, err := s.shareEntry(ctx, git, project, worktreePath, name)
			if err != nil {
				return result, err
			}
			if linked {
				result.Linked = append(result.Linked, name)
			}
		}
	case StrategyInstall:
		command, err := s.installDependencies(ctx, git, manifest, worktreePath)
		if err != nil {
			return result, err
		}
		result.Installed = command
	}

	mcp, err := s.materializeMCP(ctx, git, project, worktreePath, taskTmp)
	result.MCPConfig = mcp.config
	result.MCPProjectTracked = mcp.projectTracked
	result.MCPDropped = mcp.dropped
	if err != nil {
		return result, err
	}
	return result, nil
}

// shareEntry hardlinks one primary-checkout file into the worktree, or
// junctions one directory. A hardlinked file is the same file - same repo,
// same machine, same values by construction - and deleting either name only
// decrements the link count. A junction is removed as a link by Return before
// Git ever sees it, because Git for Windows would otherwise delete the primary
// checkout's directory through it. A missing source is skipped: the defaults
// name config a project may simply not have.
func (s Service) shareEntry(ctx context.Context, git RunnerGit, project, worktreePath, name string) (bool, error) {
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
		return false, fmt.Errorf("worktree: %q already exists in the worktree; refusing to cover it with a shared link", name)
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
	if err := os.Link(source, destination); err != nil {
		return false, fmt.Errorf("worktree: hardlink %q into the worktree: %w", name, err)
	}
	return true, nil
}

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

// installDependencies runs the project's own installer in the worktree against
// the shared per-user package cache. The installer is detected from the
// lockfile unless the manifest overrides it; a Go-only project needs nothing
// because the module cache is already shared per user.
func (s Service) installDependencies(ctx context.Context, git RunnerGit, manifest Manifest, worktreePath string) (string, error) {
	commands := manifest.Dependencies.Install
	if len(commands) == 0 {
		if detected := detectInstallCommand(worktreePath); detected != "" {
			commands = []string{detected}
		}
	}
	if len(commands) == 0 {
		return "", nil
	}
	for _, output := range installOutputs(commands) {
		if err := s.ensureIgnored(ctx, git, worktreePath, output); err != nil {
			return "", err
		}
	}
	for _, command := range commands {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			continue
		}
		result, err := s.Commands.Run(ctx, execx.Request{Dir: worktreePath, Name: fields[0], Args: fields[1:]})
		if err != nil {
			return command, fmt.Errorf("worktree: install dependencies (%q): %w", command, err)
		}
		if result.ExitCode != 0 {
			return command, fmt.Errorf("worktree: install dependencies: %s", commandFailure(command, result).Error())
		}
	}
	return strings.Join(commands, " && "), nil
}

// detectInstallCommand maps a lockfile to the install command that honors it.
func detectInstallCommand(worktreePath string) string {
	for _, candidate := range []struct{ lockfile, command string }{
		{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
		{"package-lock.json", "npm ci"},
		{"yarn.lock", "yarn install --frozen-lockfile"},
		{"uv.lock", "uv sync"},
	} {
		if _, err := os.Stat(filepath.Join(worktreePath, candidate.lockfile)); err == nil {
			return candidate.command
		}
	}
	return ""
}

// installOutputs names the directories a set of install commands
// materializes, so they can be excluded when the project itself does not
// ignore them.
func installOutputs(commands []string) []string {
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
	// dropped names the OAuth-only servers withheld from the goblin.
	dropped []string
}

// materializeMCP writes the token-authenticated subset of the project's
// .mcp.json to the task's temporary directory. It is materialized outside the
// checkout, never linked and never reported from inside it: the project's own
// config can carry OAuth connectors (its operator completes those flows
// interactively), a linked copy would hand a goblin an authentication prompt
// it can never satisfy, and a path inside the worktree could later be the
// project's own file or one the goblin wrote. Claude and codex receive that
// path through --mcp-config.
//
// The same bytes are additionally copied to <worktree>/.mcp.json, because
// kimi has no config flag and reads the project-scoped .mcp.json from its
// working directory. That copy is written only where it is safe: a path that
// does not already exist and that the project does not track. Overwriting a
// tracked .mcp.json would leave the worktree permanently modified, which the
// return path refuses to remove, so a tracked file is left exactly as it is
// and reported instead.
func (s Service) materializeMCP(ctx context.Context, git RunnerGit, project, worktreePath, taskTmp string) (mcpResult, error) {
	data, err := os.ReadFile(filepath.Join(project, ".mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return mcpResult{}, nil
	}
	if err != nil {
		return mcpResult{}, fmt.Errorf("worktree: read project .mcp.json: %w", err)
	}
	filtered, _, dropped, err := FilterMCPServers(data)
	if err != nil {
		return mcpResult{}, err
	}
	result := mcpResult{dropped: dropped}
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

	tracked, err := s.tracked(ctx, worktreePath, ".mcp.json")
	if err != nil {
		return result, err
	}
	result.projectTracked = tracked
	if tracked {
		return result, nil
	}
	if _, err := os.Lstat(filepath.Join(worktreePath, ".mcp.json")); err == nil {
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
// the index.
func (s Service) tracked(ctx context.Context, worktreePath, name string) (bool, error) {
	result, err := s.Commands.Run(ctx, execx.Request{Dir: worktreePath, Name: "git", Args: []string{"ls-files", "--error-unmatch", "--", name}})
	if err != nil {
		return false, fmt.Errorf("worktree: check whether %q is tracked: %w", name, err)
	}
	return result.ExitCode == 0, nil
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
