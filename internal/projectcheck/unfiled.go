package projectcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// UnfiledOptions name where to look for checkouts the home knows nothing of.
type UnfiledOptions struct {
	// DataDir is the home's data folder, which holds one folder for each
	// project it knows under projects.
	DataDir string
	// ProjectsRoot is the folder that holds the machine's checkouts.
	ProjectsRoot string
}

// Unfiled names each checkout directly under the projects root that the
// home holds no folder for, with what a spawn there would be given: no env
// file, since a worktree is given one only when a worktree manifest names
// it, and no credential, since a project with no auth manifest has no
// service to grant. Its last line counts what was looked at, so a root that
// held nothing does not read as one where every checkout is filed. It reads
// folder names alone and opens no file.
func Unfiled(o UnfiledOptions) ([]Finding, error) {
	entries, err := os.ReadDir(o.ProjectsRoot)
	if err != nil {
		return nil, fmt.Errorf("projectcheck: read the projects root %s: %w", o.ProjectsRoot, err)
	}
	home := filepath.Join(o.DataDir, "projects")
	var filed []string
	if folders, err := os.ReadDir(home); err == nil {
		for _, folder := range folders {
			if folder.IsDir() {
				filed = append(filed, folder.Name())
			}
		}
	}
	isFiled := func(checkout string) bool {
		for _, name := range filed {
			if sameName(name, checkout) {
				return true
			}
		}
		return false
	}

	var lines []Finding
	var checkouts, known []string
	for _, entry := range entries {
		checkout := filepath.Join(o.ProjectsRoot, entry.Name())
		// Stat and not the entry's own type, so a junction to a checkout kept
		// on another drive counts as the folder it is.
		if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(checkout, ".git")); err != nil {
			continue
		}
		checkouts = append(checkouts, entry.Name())
		if isFiled(entry.Name()) {
			known = append(known, entry.Name())
			continue
		}
		held := "The checkout holds no env file at its root"
		switch files := rootEnvFiles(checkout); {
		case len(files) == 1:
			held = "The checkout holds " + files[0] + ", which stays in the checkout"
		case len(files) > 1:
			held = "The checkout holds " + strings.Join(files, ", ") + ", which stay in the checkout"
		}
		lines = append(lines, Finding{Area: AreaCheckout, Check: "checkout-unfiled", Severity: OK,
			Says:     "the home holds no folder for this checkout, so a spawn there is given no env file and no credential from the credential store, and no record steers its routing or its verification",
			Evidence: checkout + ". No folder at " + filepath.Join(home, entry.Name()) + ". " + held + ": a worktree is given an env file only when a " + worktree.ManifestFileName + " names it in link"})
	}
	var gone []string
	for _, name := range filed {
		isHere := false
		for _, checkout := range checkouts {
			isHere = isHere || sameName(name, checkout)
		}
		if !isHere {
			gone = append(gone, name)
		}
	}
	evidence := fmt.Sprintf("%s holds %s: %s. Filed under %s: %s", o.ProjectsRoot, count(len(checkouts), "checkout"), orNone(checkouts), home, orNone(known))
	if len(gone) > 0 {
		evidence += ". Folders there with no checkout directly under the root, which may be kept elsewhere: " + strings.Join(gone, ", ")
	}
	lines = append(lines, Finding{Area: AreaCheckout, Check: "checkouts-examined", Severity: OK,
		Says:     fmt.Sprintf("examined %s directly under the projects root, and the home holds no folder for %d of them", count(len(checkouts), "checkout"), len(checkouts)-len(known)),
		Evidence: evidence + ". A folder counts as a checkout when it holds a .git, and a checkout below another folder is not looked for"})
	return lines, nil
}

// rootEnvFiles names the env files a checkout holds at its root, examples
// left out. It reads the folder's names and opens none of them.
func rootEnvFiles(checkout string) []string {
	entries, err := os.ReadDir(checkout)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if name := entry.Name(); !entry.IsDir() && isEnvFile(name) && !isExample(name) {
			files = append(files, name)
		}
	}
	return files
}
