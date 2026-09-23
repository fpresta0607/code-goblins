package supervisor

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// The browser supplies only task identity. Neither a path nor a command is
// accepted, and the native GUI is started directly rather than through a shim.
func (h *HTTP) openWorkspace(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Task       string `json:"task"`
		Generation string `json:"generation"`
		Target     string `json:"target"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if (input.Target != "vscode" && input.Target != "folder") || input.Generation == "" || state.ValidTaskID(input.Task) != nil {
		apiError(w, 400, "Select a current goblin workspace and VS Code.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, input.Task)
	if err != nil || meta.SpawnGen != input.Generation {
		apiError(w, 409, "Task restarted or was replaced. Select its current workspace.")
		return
	}
	if err := h.Service.verifyWorkspace(ctx, meta); err != nil {
		apiError(w, 409, err.Error())
		return
	}
	var executable string
	args := []string{"--new-window", "--", meta.Worktree}
	message := "Requested VS Code for this goblin working folder."
	if input.Target == "folder" {
		executable = filepath.Join(os.Getenv("SystemRoot"), "explorer.exe")
		info, statErr := os.Stat(executable)
		err = nil
		if !filepath.IsAbs(executable) || statErr != nil || !info.Mode().IsRegular() {
			err = errors.New("Windows file manager is unavailable.")
		}
		args = []string{meta.Worktree}
		message = "Requested File Explorer for this goblin working folder."
	} else {
		executable, err = findVSCode(h.editorLookup)
	}
	if err != nil {
		apiError(w, 503, err.Error())
		return
	}
	// Re-read routing after Git/editor lookup so replacement does not silently
	// open a different task folder. The existing folder is passed as one argv.
	current, err := state.ReadTaskMeta(h.Service.Store.Home.State, input.Task)
	if err != nil || current.SpawnGen != meta.SpawnGen || current.Worktree != meta.Worktree || current.Project != meta.Project {
		apiError(w, 409, "Workspace changed. Select the goblin again.")
		return
	}
	if err := h.editor.Start(ctx, execx.Request{Name: executable, Args: args, Env: editorEnvironment(os.Environ())}); err != nil {
		apiError(w, 503, "The selected application could not be started.")
		return
	}
	respond(w, 200, struct {
		Message string `json:"message"`
	}{message})
}

func (s *Service) verifyWorkspace(ctx context.Context, meta state.TaskMeta) error {
	if !filepath.IsAbs(meta.Worktree) || !filepath.IsAbs(meta.Project) || strings.ContainsAny(meta.Worktree, "\x00\r\n") {
		return errors.New("Goblin working folder is unavailable.")
	}
	if err := worktree.Validate(ctx, worktree.RunnerGit{Commands: execx.OSRunner{}}, meta.Project, meta.Worktree); err != nil {
		return errors.New("Goblin working folder is unavailable or is not an isolated Git worktree.")
	}
	common, err := s.Git.run(ctx, meta.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return errors.New("Workspace repository ownership is unavailable.")
	}
	project, err := s.Git.run(ctx, meta.Project, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !fsx.SamePath(strings.TrimSpace(common), strings.TrimSpace(project)) {
		return errors.New("Working folder no longer belongs to this task's repository.")
	}
	return nil
}

func editorEnvironment(inherited []string) []string {
	clean := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "ELECTRON_") || key == "VSCODE_DEV" || key == "VSCODE_CLI" || key == "NODE_OPTIONS" {
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}

func findVSCode(lookup func(string) (string, error)) (string, error) {
	shim, err := lookup("code")
	if err != nil || !filepath.IsAbs(shim) {
		return "", errors.New("VS Code is not available on this machine's PATH.")
	}
	var executable string
	switch strings.ToLower(filepath.Base(shim)) {
	case "code.exe":
		executable = shim
	case "code.cmd", "code":
		if !strings.EqualFold(filepath.Base(filepath.Dir(shim)), "bin") {
			return "", errors.New("VS Code installation could not be verified.")
		}
		executable = filepath.Join(filepath.Dir(filepath.Dir(shim)), "Code.exe")
	default:
		return "", errors.New("VS Code installation could not be verified.")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("VS Code executable is unavailable.")
	}
	return executable, nil
}
