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
	// HasMCP reports that a token-authenticated subset of the project's
	// .mcp.json was materialized at <worktree>/.mcp.json.
	HasMCP bool
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
func (s Service) Provision(ctx context.Context, project, worktreePath string) (ProvisionResult, error) {
	if s.Commands == nil {
		return ProvisionResult{}, errors.New("worktree: command runner is required for provisioning")
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

	hasMCP, dropped, err := s.materializeMCP(ctx, git, project, worktreePath)
	if err != nil {
		return result, err
	}
	result.HasMCP = hasMCP
	result.MCPDropped = dropped
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

// materializeMCP writes the token-authenticated subset of the project's
// .mcp.json to the worktree root. It is materialized, never linked: the
// project's own config can carry OAuth connectors (its operator completes
// those flows interactively), and a linked copy would hand a goblin an
// authentication prompt it can never satisfy. Claude receives the same file
// through --mcp-config; kimi loads the worktree-root .mcp.json on its own.
func (s Service) materializeMCP(ctx context.Context, git RunnerGit, project, worktreePath string) (bool, []string, error) {
	data, err := os.ReadFile(filepath.Join(project, ".mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("worktree: read project .mcp.json: %w", err)
	}
	filtered, _, dropped, err := FilterMCPServers(data)
	if err != nil {
		return false, nil, err
	}
	if filtered == nil {
		return false, dropped, nil
	}
	if err := s.ensureIgnored(ctx, git, worktreePath, ".mcp.json"); err != nil {
		return false, dropped, err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(worktreePath, ".mcp.json"), filtered); err != nil {
		return false, dropped, fmt.Errorf("worktree: materialize goblin .mcp.json: %w", err)
	}
	return true, dropped, nil
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
