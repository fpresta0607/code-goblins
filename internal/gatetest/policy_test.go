package gatetest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A policy names the slow packages, and a field a later build added is
// ignored, so the build a gate has installed keeps reading a policy that a
// newer one extended.
func TestParsePolicyReadsTheSlowPackagesAndIgnoresALaterBuildsField(t *testing.T) {
	// Act
	policy, err := ParsePolicy([]byte(`{"version": 1, "slow_packages": [".", "cmd/cfo"], "budgets": {"cmd/cfo": "30m"}}`))

	// Assert
	if err != nil || policy.Version != 1 || !slices.Equal(policy.SlowPackages, []string{".", "cmd/cfo"}) {
		t.Errorf("ParsePolicy = %+v, %v; want version 1 with . and cmd/cfo slow", policy, err)
	}
}

// A policy this build cannot be sure it understands is refused, so the caller
// widens the run rather than applying half of it.
func TestParsePolicyRefusesWhatItCannotBeSureItUnderstands(t *testing.T) {
	for name, text := range map[string]string{
		"not JSON":            `version: 1`,
		"no version":          `{"slow_packages": ["cmd/cfo"]}`,
		"a later version":     `{"version": 2, "slow_packages": ["cmd/cfo"]}`,
		"a mistyped field":    `{"version": 1, "slow_packages": "cmd/cfo"}`,
		"more than one value": `{"version": 1} {"version": 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if policy, err := ParsePolicy([]byte(text)); err == nil {
				t.Errorf("ParsePolicy(%s) = %+v with no error", text, policy)
			}
		})
	}
}

// The repository's own policy holds only fields this build reads and names
// only packages that exist, so a mistyped field or a package that moved fails
// here and not as a slow package quietly tested at the fast level.
func TestTheRepositoryPolicyNamesOnlyFieldsAndPackagesThatExist(t *testing.T) {
	// Arrange
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(PolicyPath)))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var strict Policy
	strictErr := decoder.Decode(&strict)
	policy, err := ParsePolicy(data)

	// Assert
	if strictErr != nil || err != nil {
		t.Fatalf("%s: strict read %v, ParsePolicy %v", PolicyPath, strictErr, err)
	}
	if len(policy.SlowPackages) == 0 {
		t.Fatalf("%s names no slow package; the fast level would run every changed package's tests", PolicyPath)
	}
	for _, dir := range policy.SlowPackages {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "*.go"))
		if err != nil || len(matches) == 0 {
			t.Errorf("%s lists %q as a slow package, and that directory holds no Go file", PolicyPath, dir)
		}
		if strings.Contains(dir, `\`) || strings.HasPrefix(dir, "./") || strings.HasSuffix(dir, "/") {
			t.Errorf("%s lists %q; a slow package is its directory from the repository root with forward slashes, such as cmd/cfo, or . for the root", PolicyPath, dir)
		}
	}
}
