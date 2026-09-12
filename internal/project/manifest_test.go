package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
