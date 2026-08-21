package auth

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// envFileNames are the local secret files a project keeps outside git, in
// increasing precedence: this is dotenv's own layering, where .env carries
// defaults a repository can share and .env.local is the local override the
// app itself loads last. They are read to adopt what is already there, never
// written.
//
// The order is load-bearing now that a .env may overwrite a stored value
// rather than only fill an empty slot. Reading it the other way round would
// let a project's dev default clobber the real credential on every dispatch,
// with no way to pin an override.
var envFileNames = []string{".env", ".env.development", ".env.local"}

// discoverDepth bounds how far into a project the scan walks. Real .env files
// live at the root or one level down in a workspace package; walking a whole
// node_modules tree to find more is waste, not thoroughness.
const discoverDepth = 2

// flyConfigRelative is where flyctl keeps the token it already holds, under
// the user's home.
var flyConfigRelative = filepath.Join(".fly", "config.yml")

// Adopted records one credential that was found already present somewhere and
// registered in this project's scope, so the Overlord is never asked for
// something the machine already has.
type Adopted struct {
	Name   string
	Key    Key
	Origin string
	// Refreshed marks a value that replaced a different one already held in
	// this project's scope, rather than one that filled an empty slot. The
	// two read differently in a report: a refresh means a credential the
	// Overlord rotated has just reached the store.
	Refreshed bool
}

// Discover registers credentials the machine already holds into this
// project's scope: values in a project's local .env files, the token gh
// already owns, and the token flyctl already holds.
//
// It is deliberately narrow, because adopting a credential from the wrong
// place is how a goblin was handed an unrelated project's database. It only
// adopts names this manifest declares; it never fills a name that already
// resolves for this project through any route the manifest allows; and it
// reads a .env file only after git confirms that file is ignored, so a value
// committed to a repository is never mistaken for a local secret.
//
// One origin may overwrite: a project's own gitignored .env file refreshes a
// value this project's scope already holds when the two differ, because the
// Overlord editing that file is the deliberate act of rotating a credential
// and every goblin dispatched afterwards would otherwise carry the dead one.
// The refreshed key is the one that actually answers the name, which may be a
// declared alias, so a credential stored under an alias rotates like any
// other instead of quietly outliving the file it came from.
// Tool-derived origins keep the never-overwrite rule: a token gh or flyctl
// happens to hold is not a decision about this project, and letting one
// rotate under a deliberately stored value is how a stored credential
// disappears without anyone choosing it.
func Discover(ctx context.Context, store Store, runner execx.Runner, manifest Manifest, projectDir string) ([]Adopted, error) {
	if store == nil {
		return nil, fmt.Errorf("auth: no credential store configured")
	}
	scope := ProjectName(projectDir)
	if scope == "" {
		scope = manifest.Project
	}
	if !ValidProjectName(scope) {
		return nil, fmt.Errorf("auth: %q cannot be a credential scope", scope)
	}
	wanted := wantedNames(store, scope, manifest)
	chains := manifest.CredentialChains()
	if len(wanted) == 0 && len(chains) == 0 {
		return nil, nil
	}

	var adopted []Adopted
	adopt := func(name, value, origin string) error {
		if !wanted[name] || strings.TrimSpace(value) == "" {
			return nil
		}
		key := Scoped(scope, name)
		if err := store.Set(key, value); err != nil {
			return err
		}
		delete(wanted, name)
		adopted = append(adopted, Adopted{Name: name, Key: key, Origin: origin})
		return nil
	}

	// refresh is bounded to this project's own scope, so a rotated secret
	// reaches the next dispatch while a name currently answered from the
	// shared scope never gains a project copy that would then drift from it.
	//
	// It rewrites the project-scope key that actually holds the value, which
	// may be a declared alias rather than the declared name. The candidates
	// are walked in the order Resolver.lookup consults them, so the key that
	// answers this name is the one that gets the rotated value; a key further
	// down the chain is one resolution never reaches, and writing it would
	// leave the live one stale.
	refresh := func(name, value, origin string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		for _, candidate := range chains[name] {
			key := Scoped(scope, candidate)
			existing, found, err := store.Get(key)
			if err != nil {
				return err
			}
			if !found || existing == "" {
				continue
			}
			if existing == value {
				return nil
			}
			if err := store.Set(key, value); err != nil {
				return err
			}
			adopted = append(adopted, Adopted{Name: name, Key: key, Origin: origin, Refreshed: true})
			return nil
		}
		return nil
	}

	for _, rotation := range credentialRotations(chains, envOwners(ctx, runner, projectDir)) {
		if err := adopt(rotation.credential, rotation.value, rotation.path); err != nil {
			return adopted, err
		}
		if err := refresh(rotation.credential, rotation.value, rotation.path); err != nil {
			return adopted, err
		}
	}

	if wanted["GITHUB_TOKEN"] && runner != nil {
		result, err := runner.Run(ctx, execx.Request{Name: "gh", Args: []string{"auth", "token"}})
		if err == nil && result.ExitCode == 0 {
			if err := adopt("GITHUB_TOKEN", strings.TrimSpace(string(result.Stdout)), "gh auth token"); err != nil {
				return adopted, err
			}
		}
	}

	if wanted["FLY_API_TOKEN"] {
		if token, path := flyAccessToken(); token != "" {
			if err := adopt("FLY_API_TOKEN", token, path); err != nil {
				return adopted, err
			}
		}
	}
	return adopted, nil
}

// wantedNames is every declared name that does not already resolve for this
// project. Resolution is asked the same question the preflight asks, so a
// name already served by a declared alias or an allowed shared value is left
// alone instead of being shadowed by a second copy that can drift.
func wantedNames(store Store, scope string, manifest Manifest) map[string]bool {
	resolver := Resolver{Store: store, Project: scope}
	wanted := map[string]bool{}
	resolved := map[string]bool{}
	for _, service := range manifest.Services {
		resolution, err := resolver.Resolve(service)
		if err != nil {
			continue
		}
		for _, name := range resolution.Missing {
			wanted[name] = true
		}
		for name := range resolution.Values {
			resolved[name] = true
		}
	}
	// A name one service reads from the shared scope is not wanted just
	// because another service cannot: adopting it would write a project copy
	// that shadows the shared value and then drifts from it.
	for name := range resolved {
		delete(wanted, name)
	}
	return wanted
}

// gitIgnores reports whether git ignores this file. A .env that git tracks is
// not a local secret, and adopting from it would take a value that anyone
// with the repository already has.
func gitIgnores(ctx context.Context, runner execx.Runner, projectDir, path string) bool {
	if runner == nil {
		return false
	}
	result, err := runner.Run(ctx, execx.Request{
		Dir:  projectDir,
		Name: "git",
		Args: []string{"check-ignore", "--quiet", path},
	})
	return err == nil && result.ExitCode == 0
}

// flyAccessToken reads the token flyctl already holds. The file is a small
// YAML map whose only key that matters here is access_token, so it is read
// with a line scan rather than a dependency.
func flyAccessToken() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	path := filepath.Join(home, flyConfigRelative)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), "access_token:")
		if !found {
			continue
		}
		token := unquote(strings.TrimSpace(value))
		if token != "" {
			return token, path
		}
	}
	return "", ""
}

// envRotation is the one .env line that speaks for a credential this run.
//
// It carries the credential rather than the name the line happened to use,
// because the whole .env path keys on the credential:
//
//   - A credential is identified by the name the manifest declares, and its
//     chain is that declared name followed by its alias targets.
//   - Exactly one .env line wins per credential per run, chosen by file
//     precedence first and chain position as the within-file tiebreak.
//   - Adoption writes the winning value to the declared name's project key,
//     whichever name in the chain the line carried. An alias is a name a
//     stored value may be found under, not a second credential, so adopting
//     one into a key of its own would be the second key the chain exists to
//     avoid.
//   - Refresh instead rewrites the project key in the chain that already
//     holds a value, so a value deliberately stored under an alias is
//     updated in place rather than shadowed by a new declared-name key.
//
// One rule decides which line wins and one decides which key it is written
// to, and keying both on the credential is what keeps the two from
// disagreeing.
type envRotation struct {
	credential string
	value      string
	path       string
}

// credentialContender is one .env line offered as the rotation for a
// credential, with everything needed to rank it against another line.
type credentialContender struct {
	claim envClaim
	// place is the line's position in the credential's lookup chain: the
	// declared name is 0 and its alias targets follow in declared order.
	place int
}

// beats orders two lines competing to rotate one credential, and the two
// rules it composes are deliberately not peers.
//
// Which file the line came from dominates, decided by envClaim.beats: that is
// the rule that says which file the Overlord meant, and .env.local is by
// convention where a rotated value is written. Chain position only breaks a
// tie inside one file, where the manifest's own order applies and a declared
// name beats its aliases, matching Resolver.lookup.
//
// The other way round, a dev default sitting in .env under the declared name
// would outrank a rotation written to .env.local under an alias, and would
// silently clobber it on every dispatch.
func (c credentialContender) beats(other credentialContender) bool {
	if c.claim.rank != other.claim.rank || c.claim.depth != other.claim.depth {
		return c.claim.beats(other.claim)
	}
	if c.place != other.place {
		return c.place < other.place
	}
	return c.claim.path < other.claim.path
}

// credentialRotations collapses the .env lines that name one credential down
// to the single line that speaks for it. envOwners already picks one owning
// file per name, but several names can answer one credential, and after the
// chains merge they all target the same store key - so without collapsing
// them one credential is written twice in a run and the second write wins on
// alphabetical order.
//
// A name the manifest does not mention is its own credential and is left
// alone.
func credentialRotations(chains map[string][]string, owned map[string]envClaim) []envRotation {
	names := make([]string, 0, len(owned))
	for name := range owned {
		names = append(names, name)
	}
	sort.Strings(names)

	best := map[string]credentialContender{}
	winner := map[string]envRotation{}
	var order []string
	for _, name := range names {
		credential, place := name, 0
		if chain := chains[name]; len(chain) > 0 {
			credential = chain[0]
			if place = slices.Index(chain, name); place < 0 {
				place = len(chain)
			}
		}
		contender := credentialContender{claim: owned[name], place: place}
		held, contested := best[credential]
		if contested && !contender.beats(held) {
			continue
		}
		if !contested {
			order = append(order, credential)
		}
		best[credential] = contender
		winner[credential] = envRotation{credential: credential, value: contender.claim.value, path: contender.claim.path}
	}

	rotations := make([]envRotation, 0, len(order))
	for _, credential := range order {
		rotations = append(rotations, winner[credential])
	}
	return rotations
}

// envClaim is one file's offer of a value for a name, with what it takes to
// rank that offer against another file's.
type envClaim struct {
	value string
	path  string
	rank  int
	depth int
}

// beats orders two files competing for the same name: dotenv precedence
// first, then the file nearer the project root, so a workspace package can
// never take a name from the project's own file. Path breaks the remaining
// tie so the winner does not depend on directory read order.
func (c envClaim) beats(other envClaim) bool {
	if c.rank != other.rank {
		return c.rank > other.rank
	}
	if c.depth != other.depth {
		return c.depth < other.depth
	}
	return c.path < other.path
}

// envOwners picks the one file that owns each name for this run, before
// anything is written. Choosing up front rather than letting the scan
// overwrite as it goes keeps a name written at most once and guarantees
// adoption and refresh read the same file: the loser here is a value that
// would otherwise overwrite a deliberately stored credential, so which file
// wins has to be decided once and by precedence rather than by scan order.
func envOwners(ctx context.Context, runner execx.Runner, projectDir string) map[string]envClaim {
	owned := map[string]envClaim{}
	root := filepath.Clean(projectDir)
	for _, path := range envFiles(projectDir) {
		if !gitIgnores(ctx, runner, projectDir, path) {
			continue
		}
		values, err := ParseEnvFile(path)
		if err != nil {
			continue
		}
		depth := 0
		if relative, relErr := filepath.Rel(root, path); relErr == nil {
			depth = strings.Count(filepath.ToSlash(relative), "/")
		}
		claim := envClaim{path: path, rank: envFileRank(filepath.Base(path)), depth: depth}
		for name, value := range values {
			if strings.TrimSpace(value) == "" {
				continue
			}
			claim.value = value
			if held, taken := owned[name]; taken && !claim.beats(held) {
				continue
			}
			owned[name] = claim
		}
	}
	return owned
}

// envFileRank is a file's place in dotenv's layering, higher winning.
func envFileRank(base string) int {
	for rank, name := range envFileNames {
		if name == base {
			return rank
		}
	}
	return -1
}

// envFiles lists the local secret files under a project, bounded in depth and
// skipping dependency and version-control directories.
//
// .worktrees is on that skip list for a different reason than the rest. The
// others are noise: generated trees whose .env files would be duplicates. A
// goblin's worktree is untrusted content a running agent writes inside the
// project, and git ignores it, so without this it satisfies both the
// gitignored test and the ownership rule and becomes an authorized origin in
// its own right.
//
// Known residual, not closed by this prune: worktree provisioning shares .env
// by hardlink, so <project>/.worktrees/<id>/.env is the same inode as
// <project>/.env. A goblin that writes its worktree .env in place - every
// ordinary redirect, Set-Content, or writeFileSync does - has written the
// Overlord's file, and the next preflight reads it from the primary path,
// which is never pruned. Skipping the worktree path removes the second
// reading of one file; it cannot tell the two names apart. The refresh origin
// is therefore only as trustworthy as write access to the project's own .env,
// and the dispatch line naming every refresh by name and origin is what
// catches it. The durable fix is for provisioning to copy rather than
// hardlink a credential-carrying file.
func envFiles(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	var found []string
	root := filepath.Clean(projectDir)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/"))
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", ".git", "dist", "build", "vendor", ".venv", ".worktrees":
				return filepath.SkipDir
			}
			if relative != "." && depth > discoverDepth {
				return filepath.SkipDir
			}
			return nil
		}
		for _, name := range envFileNames {
			if entry.Name() == name {
				found = append(found, path)
			}
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// ParseEnvFile reads KEY=VALUE lines. It accepts the shapes real .env files
// use - comments, blank lines, `export ` prefixes, and quoted values - and
// ignores anything else rather than guessing.
func ParseEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if !ValidEnvName(name) {
			continue
		}
		values[name] = unquote(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func unquote(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
