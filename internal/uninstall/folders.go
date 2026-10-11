package uninstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// folders are Code Goblins' own files and folders outside the home: what its
// programs keep in the profile and what the install and the window leave in
// the temp folder.
func (m Machine) folders(context.Context) ([]Item, error) {
	named := []struct{ what, path string }{
		{"its console host and verify reports", filepath.Join(m.LocalAppData, "cfo")},
		{"the window's browser profile", filepath.Join(m.AppData, "goblins-window.exe")},
		{"the window an earlier install kept", filepath.Join(m.LocalAppData, "CodeGoblinsWindow")},
		{"the install's log", filepath.Join(m.Temp, "CodeGoblinsInstall.log")},
		{"the setup's browser profile", filepath.Join(m.Temp, "CodeGoblinsSetup")},
	}
	var items []Item
	for _, n := range named {
		isThere, err := there(n.path)
		if err != nil {
			return nil, err
		}
		if isThere {
			items = append(items, removal(GroupFolders, n.what, n.path))
		}
	}
	items = append(items, m.tidied()...)
	for _, aside := range Asides(m.Home.Root) {
		if !strings.EqualFold(aside, m.Aside) {
			items = append(items, removal(GroupFolders, "a copy of the uninstall an earlier run left", aside))
		}
	}
	// What the install, the setup and the window put in the temp folder
	// under names of their own: the install's downloads and builds, the
	// pictures each notification showed, and pi's compiled copy of the
	// board's pi extension.
	globs := []struct{ what, pattern string }{
		{"a download of the install", filepath.Join(m.Temp, "code-goblins-*")},
		{"a notification's picture", filepath.Join(m.Temp, "Code Goblins{*}.png")},
		{"pi's compiled copy of the board's extension", filepath.Join(m.Temp, "jiti", "extensions-cfo-native.*")},
	}
	for _, g := range globs {
		matches, err := filepath.Glob(g.pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			items = append(items, removal(GroupFolders, g.what, match))
		}
	}
	return items, nil
}

// credentialsFolder is the file store, and the folder it is in where that is
// the store's alone.
func (m Machine) credentialsFolder() (string, string) {
	root := filepath.Join(m.Profile, ".cfo", "credentials")
	if dir := os.Getenv("CFO_CREDENTIAL_DIR"); dir != "" {
		return dir, ""
	}
	return root, filepath.Dir(root)
}

// credentials are the credentials Code Goblins stored: the entries in
// Windows Credential Manager, and the file store where it kept them in files
// instead.
func (m Machine) credentials(context.Context) ([]Item, error) {
	targets, err := m.Vault.Targets()
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, target := range targets {
		what := "a saved credential"
		if strings.HasPrefix(strings.TrimPrefix(target, "cfo:"), ".env-seen/") {
			what = "a record of what a .env offered"
		}
		items = append(items, Item{Action: Remove, Group: GroupCredentials, What: what, Where: target, do: func(context.Context) error {
			return m.Vault.Delete(target)
		}})
	}
	root, parent := m.credentialsFolder()
	isThere, err := there(root)
	if err != nil {
		return nil, err
	}
	if isThere {
		item := removal(GroupCredentials, "saved credentials in files", root)
		remove := item.do
		item.do = func(ctx context.Context) error {
			if err := remove(ctx); err != nil {
				return err
			}
			if parent != "" {
				removeIfEmpty(parent)
			}
			return nil
		}
		items = append(items, item)
	}
	return items, nil
}
