package gatetest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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

// A policy can say which packages' tests read a file the import graph cannot
// see, and which files no Go check reads, each with why. A policy that says
// neither, as every policy did before it could, classifies nothing.
func TestParsePolicyReadsContractsAndWhatIsOutsideTheGoChecks(t *testing.T) {
	// Act
	policy, err := ParsePolicy([]byte(`{"version": 1, "contracts": [{"paths": ["install.ps1", "install.cmd"], "packages": ["internal/installscript"], "why": "the install tests run these scripts"}], "outside": [{"paths": ["docs/**"], "why": "documentation no Go check reads"}]}`))
	bare, bareErr := ParsePolicy([]byte(`{"version": 1, "slow_packages": ["cmd/cfo"]}`))
	empty, emptyErr := ParsePolicy([]byte(`{"version": 1, "contracts": [], "outside": []}`))

	// Assert
	if err != nil || bareErr != nil || emptyErr != nil {
		t.Fatalf("ParsePolicy errors: %v, %v, %v; want none", err, bareErr, emptyErr)
	}
	wantContract := Contract{Paths: []string{"install.ps1", "install.cmd"}, Packages: []string{"internal/installscript"}, Why: "the install tests run these scripts"}
	if len(policy.Contracts) != 1 || !slices.Equal(policy.Contracts[0].Paths, wantContract.Paths) || !slices.Equal(policy.Contracts[0].Packages, wantContract.Packages) || policy.Contracts[0].Why != wantContract.Why {
		t.Errorf("contracts = %+v; want %+v", policy.Contracts, wantContract)
	}
	if len(policy.Outside) != 1 || !slices.Equal(policy.Outside[0].Paths, []string{"docs/**"}) || policy.Outside[0].Why != "documentation no Go check reads" {
		t.Errorf("outside = %+v; want docs/** as documentation no Go check reads", policy.Outside)
	}
	if !policy.Classifies() || bare.Classifies() || !empty.Classifies() {
		t.Errorf("Classifies = %v for a policy with contracts, %v for one without the fields, %v for one with them empty; want true, false, true", policy.Classifies(), bare.Classifies(), empty.Classifies())
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
	isPackage := func(dir, as string) {
		t.Helper()
		found, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), "*.go"))
		if err != nil || len(found) == 0 {
			t.Errorf("%s lists %q %s, and that directory holds no Go file", PolicyPath, dir, as)
		}
		if strings.Contains(dir, `\`) || strings.HasPrefix(dir, "./") || strings.HasSuffix(dir, "/") {
			t.Errorf("%s lists %q %s; a package is its directory from the repository root with forward slashes, such as cmd/cfo, or . for the root", PolicyPath, dir, as)
		}
	}
	for _, dir := range policy.SlowPackages {
		isPackage(dir, "as a slow package")
	}

	// A pattern that names no file this repository tracks is a contract or a
	// rule that has stopped meaning anything, and it says so here, not by
	// quietly selecting nothing.
	tracked := trackedFiles(t, root)
	namesAFile := func(pattern, in string) {
		t.Helper()
		for _, segment := range strings.Split(pattern, "/") {
			if _, err := path.Match(segment, ""); err != nil || segment == "" {
				t.Errorf("%s has the pattern %q %s, which is not a path pattern", PolicyPath, pattern, in)
				return
			}
		}
		if !slices.ContainsFunc(tracked, func(file string) bool { return matches(pattern, file) }) {
			t.Errorf("%s has the pattern %q %s, and it names no file this repository tracks", PolicyPath, pattern, in)
		}
	}
	if !policy.Classifies() {
		t.Fatalf("%s names no contract and nothing outside the Go checks, so a file no package owns selects nothing and nothing is ever unknown", PolicyPath)
	}
	for _, contract := range policy.Contracts {
		if contract.Why == "" || len(contract.Paths) == 0 || len(contract.Packages) == 0 {
			t.Errorf("%s has the contract %+v; a contract names its files, its packages and why", PolicyPath, contract)
		}
		for _, dir := range contract.Packages {
			isPackage(dir, "in the contract \""+contract.Why+"\"")
		}
		for _, pattern := range contract.Paths {
			namesAFile(pattern, "in the contract \""+contract.Why+"\"")
		}
	}
	for _, rule := range policy.Outside {
		if rule.Why == "" || len(rule.Paths) == 0 {
			t.Errorf("%s has the rule %+v; what is outside the Go checks names its files and why", PolicyPath, rule)
		}
		for _, pattern := range rule.Paths {
			namesAFile(pattern, "outside the Go checks (\""+rule.Why+"\")")
		}
	}
}

// Every file this repository tracks is a package's own, under a contract or
// listed as outside the Go checks. So no change to a file that exists today
// is unknown, which would send its run to the full level, and the change that
// adds a new kind of file says in the policy what it reaches.
func TestTheRepositoryPolicyAccountsForEveryTrackedFile(t *testing.T) {
	// Arrange
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root = fsx.LongPath(root)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(PolicyPath)))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ParsePolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listed, err := output(ctx, execx.OSRunner{}, root, "go", "list", "-e", "-json="+listFields, "./...")
	if err != nil {
		t.Fatal(err)
	}
	packages, err := decodePackages(strings.NewReader(listed))
	if err != nil {
		t.Fatal(err)
	}
	module, err := output(ctx, execx.OSRunner{}, root, "go", "list", "-m", "-f", "{{.Path}}")
	if err != nil {
		t.Fatal(err)
	}
	tracked := slices.DeleteFunc(trackedFiles(t, root), func(file string) bool { return file == "go.mod" || file == "go.sum" })

	// Act
	reach := Classify(root, strings.TrimSpace(module), tracked, packages, policy)

	// Assert
	if !policy.Classifies() || len(tracked) < 100 || len(packages) < 10 {
		t.Fatalf("the check read a policy that classifies: %v, %d tracked files and %d packages; it proves nothing unless the policy classifies and the repository was read", policy.Classifies(), len(tracked), len(packages))
	}
	if reach.Everything || len(reach.Unknown) != 0 {
		t.Errorf("%s accounts for all but %d of the %d files this repository tracks, and a change to one of these would require the full level: %q. Name each in that file: under contracts, with the packages whose tests read it, or under outside, with why no Go check reads it", PolicyPath, len(reach.Unknown), len(tracked), reach.Unknown)
	}
}

// trackedFiles lists the files the repository at root tracks, as slash paths
// from it.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}
