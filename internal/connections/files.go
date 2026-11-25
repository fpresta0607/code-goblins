package connections

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// readBounded reads a file of at most limit bytes. A larger file is an error,
// never a partial read.
func readBounded(path string, limit int64) ([]byte, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the file is larger than a connection configuration is")
	}
	return data, nil
}

// manifestProjects names every project with an auth manifest under dataDir,
// sorted.
func manifestProjects(dataDir string) []string {
	entries, err := os.ReadDir(filepath.Join(dataDir, auth.ManifestDirName))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if info, err := os.Stat(auth.ManifestPath(dataDir, entry.Name())); err == nil && info.Mode().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	return names
}

// projectName is the name a goblin's project is listed under: the name its
// path ends in, or on Windows, whose folders ignore case, the folder of the
// manifest that names it in any case. There a checkout named Acme and a
// manifest folder named acme are one project; elsewhere they are two.
func projectName(dataDir, project string) string {
	name := auth.ProjectName(project)
	if runtime.GOOS != "windows" {
		return name
	}
	for _, listed := range manifestProjects(dataDir) {
		if strings.EqualFold(listed, name) {
			return listed
		}
	}
	return name
}
