package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadText(t *testing.T, s string) (Manifest, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(p, []byte(s), 0600)
	return Load(p)
}
func TestManifestValidAndArbitraryProvider(t *testing.T) {
	m, e := loadText(t, `{"project":"p","services":[{"name":"db","kind":"database","provider":"weird-cloud","env":["DATABASE_URL"]},{"name":"api","kind":"backend","depends_on":["db"]}],"deployment":{"required":true,"targets":[{"name":"api","provider":"fly","command":["flyctl","deploy"],"verify":["curl","-f","https://x/health"]}]}}`)
	if e != nil {
		t.Fatal(e)
	}
	if m.Services[0].Provider != "weird-cloud" {
		t.Fatal("provider restricted")
	}
	if strings.Contains(m.Capsule(), "secret-value") {
		t.Fatal("capsule leaked secret")
	}
}
func TestManifestRejectsUnknownField(t *testing.T) {
	_, e := loadText(t, `{"project":"p","oops":true}`)
	if e == nil {
		t.Fatal("wanted error")
	}
}
func TestManifestRejectsDuplicateService(t *testing.T) {
	_, e := loadText(t, `{"project":"p","services":[{"name":"db","kind":"database"},{"name":"db","kind":"redis"}]}`)
	if e == nil {
		t.Fatal("wanted error")
	}
}
func TestManifestRejectsMissingServiceName(t *testing.T) {
	_, e := loadText(t, `{"project":"p","services":[{"kind":"database"}]}`)
	if e == nil {
		t.Fatal("wanted error")
	}
}
func TestManifestRejectsInvalidDependency(t *testing.T) {
	_, e := loadText(t, `{"project":"p","services":[{"name":"api","kind":"backend","depends_on":["db"]}]}`)
	if e == nil {
		t.Fatal("wanted error")
	}
}
func TestSecurityNeedsDeep(t *testing.T) {
	s := Security{Mode: "risk", Triggers: []string{"auth", "migration"}}
	if !s.NeedsDeep([]string{"internal/auth/login.go"}) {
		t.Fatal("risk trigger missed")
	}
	if s.NeedsDeep([]string{"README.md"}) {
		t.Fatal("false trigger")
	}
}

// A tier reuses a pass only as its record declares what its checks read
// besides git's files, each an input inside the worktree, a tool command and
// a variable, and how old a pass it takes.
func TestManifestReadsAReuseDeclaration(t *testing.T) {
	// Act
	m, err := loadText(t, `{"project":"p","verification":{"fast":[["go","test"]],"reuse":{"inputs":["internal/boardweb/dist"],"tools":[["go","version"]],"environment":["GOFLAGS"],"max_age_hours":12}}}`)

	// Assert
	reuse := m.Verification.Reuse
	if err != nil || reuse == nil || len(reuse.Inputs) != 1 || len(reuse.Tools) != 1 || len(reuse.Environment) != 1 || reuse.MaxAge() != 12*time.Hour {
		t.Fatalf("reuse = %+v, %v; want the declaration read", reuse, err)
	}
}

// A declaration that names an input outside the worktree, an empty tool
// command, an empty variable or a negative age is refused.
func TestManifestRefusesAReuseDeclarationItCannotKeep(t *testing.T) {
	for name, reuse := range map[string]string{
		"an input outside the worktree": `{"inputs":["../other"]}`,
		"an input by an absolute path":  `{"inputs":["C:/other"]}`,
		"an empty input":                `{"inputs":[""]}`,
		"an empty tool command":         `{"tools":[[]]}`,
		"an empty variable":             `{"environment":[""]}`,
		"a negative age":                `{"max_age_hours":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			m, err := loadText(t, `{"project":"p","verification":{"fast":[["go","test"]],"reuse":`+reuse+`}}`)

			// Assert
			if err == nil {
				t.Errorf("loaded %+v; want the declaration refused", m.Verification.Reuse)
			}
		})
	}
}

// With no age declared, a pass is taken for a day.
func TestReuseTakesAPassForADayUnlessItSaysOtherwise(t *testing.T) {
	if age := (Reuse{}).MaxAge(); age != 24*time.Hour {
		t.Errorf("MaxAge = %s; want 24h", age)
	}
}
