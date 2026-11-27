package connections

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// RemovedFileName is the file beside a project's manifest that keeps, word for
// word, each service Remove took out of it, so it can be put back.
const RemovedFileName = "auth.removed.json"

// manifestFile is a manifest as its file holds it, each service kept as its
// own text so the one taken out is kept, and the others written back, as they
// were written.
type manifestFile struct {
	Project  string            `json:"project"`
	Services []json.RawMessage `json:"services"`
	Formats  json.RawMessage   `json:"formats,omitempty"`
	Logins   json.RawMessage   `json:"logins,omitempty"`
}

// removedService is one entry of the removed file.
type removedService struct {
	RemovedAt time.Time       `json:"removed_at"`
	Service   json.RawMessage `json:"service"`
}

// RemoveService takes the service name out of the project's manifest, the
// whole of what Remove does for a project's service: the credentials stored
// for it stay in the vault, and its entry is first added to the removed file
// beside the manifest, so a failure between the two writes loses nothing. It
// refuses a manifest that does not load and a service it does not declare.
func (e Environment) RemoveService(project, name string, now time.Time) error {
	manifest, err := auth.LoadManifest(e.DataDir, project)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New(project + " has no auth.json, so it declares no service to remove. Refresh connections.")
	}
	if err != nil {
		return errors.New(project + "'s auth.json could not be read, so nothing was removed.")
	}
	if !slices.ContainsFunc(manifest.Services, func(service auth.Service) bool { return service.Name == name }) {
		return errors.New(project + "'s auth.json no longer declares " + name + ". Refresh connections.")
	}
	data, err := fsx.ReadFile(manifest.Path)
	if err != nil {
		return errors.New(project + "'s auth.json could not be read, so nothing was removed.")
	}
	var file manifestFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return errors.New(project + "'s auth.json could not be read, so nothing was removed.")
	}
	index := slices.IndexFunc(file.Services, func(raw json.RawMessage) bool {
		var service struct {
			Name string `json:"name"`
		}
		return json.Unmarshal(raw, &service) == nil && service.Name == name
	})
	removed := file.Services[index]
	file.Services = slices.Delete(file.Services, index, index+1)
	if err := keepRemoved(filepath.Join(filepath.Dir(manifest.Path), RemovedFileName), removedService{RemovedAt: now.UTC(), Service: removed}); err != nil {
		return errors.New("The copy of " + name + " could not be kept beside " + project + "'s auth.json, so nothing was removed.")
	}
	written, err := json.MarshalIndent(file, "", "  ")
	if err == nil {
		err = fsx.AtomicWriteFile(manifest.Path, append(written, '\n'))
	}
	if err != nil {
		return errors.New(project + "'s auth.json could not be written, so nothing was removed.")
	}
	return nil
}

// RemoveUserCredential deletes a credential's variable from this PC's user
// environment, the whole of what Remove does for a credential every program
// carries: its value is first kept in the vault's shared scope under its own
// name, and read back, so it is stored somewhere before it leaves the
// environment. A vault that already holds another value under that name is a
// refusal, since the value would be stored nowhere else. No value is ever
// said in what it returns.
func (e Environment) RemoveUserCredential(name string) error {
	if !auth.ValidEnvName(name) || !credentialLike.MatchString(name) || auth.IsHarnessBillingKey(name) {
		return errors.New("Only a credential this page lists can be removed from this PC's user environment.")
	}
	value, isSet, err := e.UserEnv.Get(name)
	if err != nil {
		return errors.New("This PC's user environment could not be read, so nothing was removed.")
	}
	if !isSet {
		return errors.New(name + " is no longer in this PC's user environment. Refresh connections.")
	}
	store, err := e.OpenStore()
	if err != nil {
		return errors.New("The credential vault could not be opened, so nothing was removed.")
	}
	key := auth.Shared(name)
	stored, isStored, err := store.Get(key)
	if err != nil {
		return errors.New("The credential vault could not be read, so nothing was removed.")
	}
	if isStored && stored != value {
		return errors.New("The vault already holds another value under " + name + ", so removing it here would lose this one. Nothing was removed.")
	}
	if !isStored {
		if err := store.Set(key, value); err != nil {
			return errors.New(name + " could not be kept in the credential vault, so nothing was removed.")
		}
		if stored, isStored, err = store.Get(key); err != nil || !isStored || stored != value {
			return errors.New(name + " could not be kept in the credential vault, so it was not removed.")
		}
	}
	if err := e.UserEnv.Unset(name); err != nil {
		return errors.New(name + " could not be deleted from this PC's user environment. Its value is kept in the vault.")
	}
	// The variable is gone from the registry whatever the broadcast does, so
	// the removal stands: a broadcast that fails only leaves programs Explorer
	// starts with it until the next sign-in.
	_ = e.UserEnv.Broadcast()
	return nil
}

// Where a server a goblin's session named comes to it from.
const (
	// ServerGranted is a server the goblin's task record gives it by name.
	ServerGranted = "granted"
	// ServerProject is a server every goblin of its project gets from the
	// project's .mcp.json.
	ServerProject = "project"
	// ServerElsewhere is a server Code Goblins does not declare for it, such
	// as one of the Claude settings or account.
	ServerElsewhere = "elsewhere"
)

// GoblinServerSource says where the server a goblin's session named comes
// from, read from its task record and its project's .mcp.json, so Remove
// takes back only a server the record gives it.
func GoblinServerSource(meta state.TaskMeta, server string) (string, error) {
	data, err := readBounded(filepath.Join(meta.Project, ".mcp.json"), mcpConfigLimit)
	if errors.Is(err, os.ErrNotExist) {
		return ServerElsewhere, nil
	}
	if err != nil {
		return "", err
	}
	_, kept, _, _, held, err := worktree.FilterMCPServers(data, func(string) bool { return true }, func(string) bool { return false })
	switch {
	case err != nil:
		return "", err
	case slices.Contains(held, server) && slices.Contains(meta.MCPServers, server):
		return ServerGranted, nil
	case slices.Contains(kept, server):
		return ServerProject, nil
	}
	return ServerElsewhere, nil
}

// keepRemoved adds one entry to the removed file, making it on first use.
func keepRemoved(path string, entry removedService) error {
	var kept []removedService
	data, err := fsx.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &kept); err != nil {
			return err
		}
	}
	written, err := json.MarshalIndent(append(kept, entry), "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(written, '\n'))
}
