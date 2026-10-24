package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestReadsIssueRepository(t *testing.T) {
	for _, repository := range []string{"", "acme/ops", "Acme-Org/ops_reports.v2", "a/.github"} {
		t.Run(repository, func(t *testing.T) {
			// Arrange
			text := fmt.Sprintf(`{"project":"p","issue_repository":%q}`, repository)
			if repository == "" {
				text = `{"project":"p"}`
			}

			// Act
			manifest, err := loadText(t, text)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if repository == "" {
				if _, exists := fields["issue_repository"]; exists {
					t.Fatal("unset issue_repository was serialized")
				}
			} else if string(fields["issue_repository"]) != fmt.Sprintf("%q", repository) {
				t.Fatalf("issue_repository = %s, want %q", fields["issue_repository"], repository)
			}
		})
	}
}

func TestManifestRejectsMalformedIssueRepository(t *testing.T) {
	for _, repository := range []string{
		" ", "acme", "/ops", "acme/", "acme/ops/extra", "https://github.com/acme/ops",
		" acme/ops", "acme/ops ", "acme/ops?private=true", "acme/ops#issues", "acme/ops\n",
		`acme\ops`, "../ops", "acme/..", "acme/.", "acme/%2e%2e", "acme/ops name", "ac_me/ops",
	} {
		t.Run(repository, func(t *testing.T) {
			// Act
			_, err := loadText(t, fmt.Sprintf(`{"project":"p","issue_repository":%q}`, repository))

			// Assert
			if err == nil || !strings.Contains(err.Error(), "issue_repository") {
				t.Fatalf("error = %v, want malformed issue_repository refused", err)
			}
		})
	}
}

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

// A tier reuses a pass only as its record declares which tiers, what their
// checks read besides git's files, each an input inside the worktree, a tool
// command and a variable, and how old a pass it takes.
func TestManifestReadsAReuseDeclaration(t *testing.T) {
	// Act
	m, err := loadText(t, `{"project":"p","verification":{"fast":[["go","test"]],"reuse":{"tiers":["fast"],"inputs":["internal/boardweb/dist"],"tools":[["go","version"]],"environment":["GOFLAGS"],"max_age_hours":12}}}`)

	// Assert
	reuse := m.Verification.Reuse
	if err != nil || reuse == nil || len(reuse.Tiers) != 1 || len(reuse.Inputs) != 1 || len(reuse.Tools) != 1 || len(reuse.Environment) != 1 || reuse.MaxAge() != 12*time.Hour {
		t.Fatalf("reuse = %+v, %v; want the declaration read", reuse, err)
	}
}

// A declaration that names no tier or one of no other name, an input outside
// the worktree, an empty tool command, a variable that is no name, or an age
// under nothing or past the week a pass is kept is refused.
func TestManifestRefusesAReuseDeclarationItCannotKeep(t *testing.T) {
	for name, reuse := range map[string]string{
		"no tier":                       `{}`,
		"an unknown tier":               `{"tiers":["nightly"]}`,
		"an input outside the worktree": `{"tiers":["fast"],"inputs":["../other"]}`,
		"an input by an absolute path":  `{"tiers":["fast"],"inputs":["C:/other"]}`,
		"an empty input":                `{"tiers":["fast"],"inputs":[""]}`,
		"an empty tool command":         `{"tiers":["fast"],"tools":[[]]}`,
		"an empty variable":             `{"tiers":["fast"],"environment":[""]}`,
		"a variable with a space":       `{"tiers":["fast"],"environment":[" GOFLAGS"]}`,
		"a negative age":                `{"tiers":["fast"],"max_age_hours":-1}`,
		"an age past a week":            `{"tiers":["fast"],"max_age_hours":169}`,
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
	// Act
	age := (Reuse{}).MaxAge()

	// Assert
	if age != 24*time.Hour {
		t.Errorf("MaxAge = %s; want 24h", age)
	}
}

func TestManifestReadsWhetherATrainLandsALoneRiderOnItsOwnRun(t *testing.T) {
	cases := map[string]struct {
		text      string
		isLanding bool
	}{
		"left out":  {text: `{"project":"p"}`},
		"turned on": {text: `{"project":"p","trains":{"land_lone_rider_on_its_run":true}}`, isLanding: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := loadText(t, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if m.Trains.LandLoneRiderOnItsRun != c.isLanding {
				t.Fatalf("LandLoneRiderOnItsRun = %v, want %v", m.Trains.LandLoneRiderOnItsRun, c.isLanding)
			}
		})
	}
}

func TestManifestReadsALoneRidersWaitWithinItsBounds(t *testing.T) {
	cases := map[string]struct {
		text      string
		wait      int
		isRefused bool
	}{
		"left out":       {text: `{"project":"p"}`},
		"half an hour":   {text: `{"project":"p","trains":{"lone_rider_wait_minutes":30}}`, wait: 30},
		"the bound":      {text: `{"project":"p","trains":{"lone_rider_wait_minutes":240}}`, wait: 240},
		"past the bound": {text: `{"project":"p","trains":{"lone_rider_wait_minutes":241}}`, isRefused: true},
		"negative":       {text: `{"project":"p","trains":{"lone_rider_wait_minutes":-1}}`, isRefused: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := loadText(t, c.text)
			if (err != nil) != c.isRefused {
				t.Fatalf("error = %v, want refused %v", err, c.isRefused)
			}
			if !c.isRefused && m.Trains.LoneRiderWaitMinutes != c.wait {
				t.Fatalf("LoneRiderWaitMinutes = %d, want %d", m.Trains.LoneRiderWaitMinutes, c.wait)
			}
		})
	}
}
