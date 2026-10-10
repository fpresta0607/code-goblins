package projectcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// The check assesses one project, so nothing named the checkouts the home
// holds no folder for. A spawn can be sent into any of them. Each is named
// with what a spawn there is given, which since a worktree shares no env
// file by default is no env file and no credential.
func TestCheckoutsTheHomeHoldsNoFolderForAreNamedWithWhatASpawnThereIsGiven(t *testing.T) {
	// Arrange
	base := t.TempDir()
	root, data := filepath.Join(base, "dev"), filepath.Join(base, "home", "data")
	for _, file := range []string{
		"northwind/.git/HEAD",
		"southwind/.git/HEAD", "southwind/.env", "southwind/.env.local", "southwind/.env.example", "southwind/web/.env",
		"eastwind/.git",
		"notes/README.md",
		"readme.txt",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(file)), "KEY=held by the checkout\n")
	}
	writeFile(t, filepath.Join(data, "projects", "northwind", "auth.json"), `{"project":"northwind","services":[]}`)
	writeFile(t, filepath.Join(data, "projects", "westwind", "auth.json"), `{"project":"westwind","services":[]}`)

	// Act
	lines, err := Unfiled(UnfiledOptions{DataDir: data, ProjectsRoot: root})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	report := Report{Lines: lines}
	var unfiled []Finding
	for _, line := range lines {
		if line.Check == "checkout-unfiled" {
			unfiled = append(unfiled, line)
		}
	}
	if len(unfiled) != 2 {
		t.Fatalf("%d checkouts are named as unfiled, want eastwind and southwind:\n%s", len(unfiled), report.Text())
	}
	east, south := unfiled[0], unfiled[1]
	for _, line := range unfiled {
		if line.Severity != OK || line.Area != AreaCheckout {
			t.Errorf("checkout-unfiled is %s in %s, want ok in checkout", line.Severity, line.Area)
		}
		contains(t, "says", line.Says, "no env file", "no credential", "no record")
	}
	contains(t, "evidence", east.Evidence, filepath.Join(root, "eastwind"), filepath.Join(data, "projects", "eastwind"), "holds no env file at its root")
	contains(t, "evidence", south.Evidence, filepath.Join(root, "southwind"), ".env, .env.local", "stay in the checkout")
	for _, wrong := range []string{".env.example", "web/.env", "held by the checkout"} {
		if strings.Contains(south.Evidence, wrong) {
			t.Errorf("evidence %q names %s, which is an example, is below the root or is a value", south.Evidence, wrong)
		}
	}
	examined := only(t, report, "checkouts-examined")
	contains(t, "says", examined.Says, "3 checkouts", "2 of them")
	contains(t, "evidence", examined.Evidence, root, "northwind", "westwind")
	if strings.Contains(examined.Evidence, "notes") {
		t.Errorf("evidence %q names notes, which is no checkout", examined.Evidence)
	}
}

// A projects root that cannot be read is an error, never a list that says
// every checkout is filed.
func TestAProjectsRootThatCannotBeReadIsAnError(t *testing.T) {
	// Arrange
	base := t.TempDir()

	// Act
	lines, err := Unfiled(UnfiledOptions{DataDir: filepath.Join(base, "data"), ProjectsRoot: filepath.Join(base, "gone")})

	// Assert
	if err == nil {
		t.Errorf("a projects root that is no folder gave %d lines and no error", len(lines))
	}
}
