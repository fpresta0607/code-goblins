// Package services starts, shares, tracks and stops a project's local
// services for the goblins whose full-stack checks need them: one compose
// stack per project, held by the tasks that asked for it and started only
// when the machine's memory and commit stay above the fleet's floor with the
// stack's cost added. The last release stops the stack, and the Docker engine
// with it when cfo started the engine.
package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ManifestFileName is one project's local services declaration under the
// CFO home's data directory, beside its auth manifest.
const ManifestFileName = "services.json"

// Manifest is one project's declared local services.
type Manifest struct {
	// Project is the project directory name this manifest describes.
	Project string `json:"project"`
	// Compose is the compose file, relative to the project's checkout.
	Compose string `json:"compose"`
	// EnvFile is the env file compose reads its variables from, relative to
	// the checkout, or empty for none.
	EnvFile string `json:"env_file,omitempty"`
	// Services are the compose services a full-stack check needs. Compose
	// starts what they depend on with them.
	Services []string `json:"services"`
	// Check is a command run in the checkout before the stack starts, such
	// as the project's guard that its env file points at no production
	// service. A check that exits non-zero refuses the start.
	Check []string `json:"check,omitempty"`
	// MemoryEstimateGB is what the stack is expected to cost before cfo has
	// measured it: memory, and commit beside it.
	MemoryEstimateGB float64 `json:"memory_estimate_gb"`
	// Path is where the manifest was loaded from. It is not serialized.
	Path string `json:"-"`
}

// ManifestPath returns a project's services declaration under a CFO home's
// data directory.
func ManifestPath(dataDir, project string) string {
	return filepath.Join(dataDir, auth.ManifestDirName, auth.ProjectName(project), ManifestFileName)
}

// LoadManifest reads and validates the services declaration of the project
// whose checkout or name is project.
func LoadManifest(dataDir, project string) (Manifest, error) {
	path := ManifestPath(dataDir, project)
	data, err := fsx.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("services: %s: %w", path, err)
	}
	if decoder.More() {
		return Manifest{}, fmt.Errorf("services: %s: trailing JSON", path)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("services: %s: %w", path, err)
	}
	manifest.Path = path
	return manifest, nil
}

// Validate refuses a declaration cfo could not start a stack from.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Project) == "" {
		return errors.New("project is required")
	}
	if err := relativePath("compose", m.Compose); err != nil {
		return err
	}
	if m.EnvFile != "" {
		if err := relativePath("env_file", m.EnvFile); err != nil {
			return err
		}
	}
	if len(m.Services) == 0 {
		return errors.New("services names no compose service")
	}
	for _, service := range m.Services {
		if strings.TrimSpace(service) == "" || strings.HasPrefix(service, "-") {
			return fmt.Errorf("service %q is not a compose service name", service)
		}
	}
	if len(m.Check) > 0 && strings.TrimSpace(m.Check[0]) == "" {
		return errors.New("check names no program")
	}
	if m.MemoryEstimateGB <= 0 {
		return errors.New("memory_estimate_gb must be more than 0, what the stack costs before cfo has measured it")
	}
	return nil
}

// relativePath refuses a path that leaves the checkout.
func relativePath(field, path string) error {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if strings.TrimSpace(path) == "" || filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must be a path inside the checkout", field, path)
	}
	return nil
}
