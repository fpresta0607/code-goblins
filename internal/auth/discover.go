package auth

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
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
func Discover(ctx context.Context, store Store, runner execx.Runner, manifest Manifest, projectDir string) ([]Adopted, ScanSkipped, error) {
	if store == nil {
		return nil, ScanSkipped{}, fmt.Errorf("auth: no credential store configured")
	}
	scope := ProjectName(projectDir)
	if scope == "" {
		scope = manifest.Project
	}
	if !ValidProjectName(scope) {
		return nil, ScanSkipped{}, fmt.Errorf("auth: %q cannot be a credential scope", scope)
	}
	wanted := wantedNames(store, scope, manifest)
	chains := manifest.CredentialChains()
	if len(wanted) == 0 && len(chains) == 0 {
		return nil, ScanSkipped{}, nil
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

	owned, skipped := envOwners(ctx, runner, projectDir)
	writes, err := planWrites(store, scope, chains, wanted, credentialRotations(chains, owned))
	if err != nil {
		return adopted, skipped, err
	}
	for _, write := range writes {
		if err := store.Set(write.key, write.rotation.value); err != nil {
			return adopted, skipped, err
		}
		if !write.refresh {
			delete(wanted, write.rotation.credential)
		}
		adopted = append(adopted, Adopted{
			Name:      write.rotation.credential,
			Key:       write.key,
			Origin:    write.rotation.path,
			Refreshed: write.refresh,
		})
	}

	if wanted["GITHUB_TOKEN"] && runner != nil {
		result, err := runner.Run(ctx, execx.Request{Name: "gh", Args: []string{"auth", "token"}})
		if err == nil && result.ExitCode == 0 {
			if err := adopt("GITHUB_TOKEN", strings.TrimSpace(string(result.Stdout)), "gh auth token"); err != nil {
				return adopted, skipped, err
			}
		}
	}

	if wanted["FLY_API_TOKEN"] {
		if token, path := flyAccessToken(); token != "" {
			if err := adopt("FLY_API_TOKEN", token, path); err != nil {
				return adopted, skipped, err
			}
		}
	}
	return adopted, skipped, nil
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

// gitIgnored reports which of these files git ignores, and which ones it
// could not answer for. A .env that git tracks is not a local secret, and
// adopting from it would take a value that anyone with the repository already
// has.
//
// Two rules govern the answer, and they pull in opposite directions:
//
//   - A file that cannot be shown to be ignored is never read. "Undetermined"
//     resolves to "do not adopt", never to "ignored", so a tracked .env can
//     never be mistaken for a local secret.
//   - One unanswerable path must cost only that path. Classifying the whole
//     set in one invocation is the fast path, but a batch that fails takes
//     every file with it, and adoption and refresh stopping project-wide is
//     the stale-credential defect this package exists to close. So a batch
//     that git could not answer falls back to asking per file, which is what
//     the per-file form always did.
//
// What the fallback still cannot answer is returned rather than dropped: a
// scan that read nothing has to be visible on the dispatch line, because a
// silent one is indistinguishable from a project that simply had nothing to
// rotate.
func gitIgnored(ctx context.Context, runner execx.Runner, projectDir string, paths []string) (map[string]bool, []string) {
	if runner == nil || len(paths) == 0 {
		return nil, nil
	}
	if ignored, answered := gitIgnoredBatch(ctx, runner, projectDir, paths); answered {
		return ignored, nil
	}
	ignored := map[string]bool{}
	var undetermined []string
	for _, path := range paths {
		matched, answered := gitIgnoresPath(ctx, runner, projectDir, path)
		switch {
		case !answered:
			undetermined = append(undetermined, path)
		case matched:
			ignored[path] = true
		}
	}
	return ignored, undetermined
}

// gitIgnoredBatch classifies every path in one invocation, reporting whether
// git answered at all. Exit 1 is "none of them are ignored" rather than a
// failure; anything else is unanswered and the caller asks again per file.
func gitIgnoredBatch(ctx context.Context, runner execx.Runner, projectDir string, paths []string) (map[string]bool, bool) {
	// quotePath off keeps a non-ASCII pathname raw rather than octal-escaped,
	// and -- ends the options so a pathname beginning with a dash is a path
	// rather than an unknown switch that would fail the whole batch.
	args := append([]string{"-c", "core.quotePath=false", "check-ignore", "--"}, paths...)
	result, err := runner.Run(ctx, execx.Request{Dir: projectDir, Name: "git", Args: args})
	if err != nil || (result.ExitCode != 0 && result.ExitCode != 1) {
		return nil, false
	}
	asked := make(map[string]bool, len(paths))
	for _, path := range paths {
		asked[path] = true
	}
	ignored := map[string]bool{}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		name := strings.TrimRight(line, "\r")
		if name == "" {
			continue
		}
		// A pathname git had to quote is C-quoted, which is the syntax
		// strconv.Unquote reads. Only a name that was actually asked about is
		// believed, so a line this failed to read back can never widen what
		// is treated as a local secret.
		if strings.HasPrefix(name, `"`) {
			unquoted, unquoteErr := strconv.Unquote(name)
			if unquoteErr != nil {
				continue
			}
			name = unquoted
		}
		if asked[name] {
			ignored[name] = true
		}
	}
	return ignored, true
}

// gitIgnoresPath asks about one file, reporting whether git answered. It is
// the isolation the batch gives up: a path git refuses - one behind a
// symlinked directory, say - costs itself and nothing else.
func gitIgnoresPath(ctx context.Context, runner execx.Runner, projectDir, path string) (matched, answered bool) {
	result, err := runner.Run(ctx, execx.Request{
		Dir:  projectDir,
		Name: "git",
		Args: []string{"check-ignore", "--quiet", "--", path},
	})
	if err != nil {
		return false, false
	}
	switch result.ExitCode {
	case 0:
		return true, true
	case 1:
		return false, true
	default:
		return false, false
	}
}

// IgnoreScanFailedLine names the local files whose gitignore status git could
// not answer, so an operator can see that adoption skipped them. They are not
// read - a file that cannot be shown to be gitignored may be one the
// repository tracks - and this line is what makes that refusal visible rather
// than a credential that silently never rotates.
//
// It names paths. No credential name and no value appears here, the same rule
// the adoption line keeps.
func IgnoreScanFailedLine(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("ignore check failed for %d local file(s) (%s); they were not read",
		len(paths), strings.Join(paths, ", "))
}

// ScanSkipped names the local files the scan declined to read, by cause. The
// two are kept apart because they send an operator to different places: one
// is a git problem to investigate, the other is an ordinary state that clears
// itself when a worktree is returned.
type ScanSkipped struct {
	// IgnoreUnknown are files git could not classify. A file that cannot be
	// shown to be gitignored may be one the repository tracks, so it is not
	// read.
	IgnoreUnknown []string
	// WorktreeShared are files a live goblin worktree shares by hardlink. A
	// goblin can write them, so the store does not follow them.
	WorktreeShared []string
	// LinkCheckFailed are files whose hard link count could not be read.
	// They are skipped exactly as a shared file is, because not knowing
	// whether a goblin can write a file is not the same answer as knowing it
	// cannot - but the cause is separate, since nothing an operator does to
	// worktrees will change it.
	LinkCheckFailed []string
}

// Any reports whether the scan skipped anything at all.
func (s ScanSkipped) Any() bool {
	return len(s.IgnoreUnknown) > 0 || len(s.WorktreeShared) > 0 || len(s.LinkCheckFailed) > 0
}

// WorktreeSharedLine names the local files a live goblin worktree shares, so
// an operator can see why a rotated value has not been picked up yet and what
// clears it. Provisioning hardlinks a project's .env into every worktree, and
// the worktree copy is the same inode, so a goblin that writes .env writes
// this file: following it would let a goblin rotate the fleet's stored
// credential. The count drops back to one when cfo cleanup returns the
// worktree, and the next dispatch adopts normally.
//
// It names paths. No credential name and no value appears here, the same rule
// the adoption line keeps.
func WorktreeSharedLine(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("adoption paused for %d local file(s) shared with a live goblin worktree (%s); run `cfo cleanup <id>` to release them",
		len(paths), strings.Join(paths, ", "))
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
			place = slices.Index(chain, name)
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

// linkCount is a variable so a test can force the unreadable case. Producing
// a genuine stat failure here is a race - the file has to vanish between the
// scan and the count - and the branch it guards is the one that decides
// whether a credential may be adopted, so it is worth testing directly.
var linkCount = hardLinkCount

// sharedIntoAWorktree reports whether more than one directory entry points at
// this file's data. An unreadable count is the caller's cue to skip the file
// rather than to trust it: "I cannot tell whether a goblin shares this" and
// "no goblin shares this" are different answers, and only one of them is safe
// to adopt a credential from.
func sharedIntoAWorktree(path string) (bool, error) {
	links, err := linkCount(path)
	if err != nil {
		return false, err
	}
	return links > 1, nil
}

// envOwners picks the one file that owns each name for this run, before
// anything is written. Choosing up front rather than letting the scan
// overwrite as it goes keeps a name written at most once and guarantees
// adoption and refresh read the same file: the loser here is a value that
// would otherwise overwrite a deliberately stored credential, so which file
// wins has to be decided once and by precedence rather than by scan order.
func envOwners(ctx context.Context, runner execx.Runner, projectDir string) (map[string]envClaim, ScanSkipped) {
	owned := map[string]envClaim{}
	root := filepath.Clean(projectDir)
	candidates := envFiles(projectDir)
	ignored, unknown := gitIgnored(ctx, runner, projectDir, candidates)
	skipped := ScanSkipped{IgnoreUnknown: unknown}
	for _, path := range candidates {
		if !ignored[path] {
			continue
		}
		// A file shared into a live goblin worktree is a file a running
		// goblin can write, so it is not an origin the store may follow.
		// Provisioning hardlinks a project's .env into every worktree, and
		// the worktree copy IS this file - same inode - so pruning the
		// .worktrees directory from the scan cannot tell them apart. The
		// link count can: it drops back to one when cfo cleanup returns the
		// worktree, and adoption resumes on its own.
		shared, err := sharedIntoAWorktree(path)
		if err != nil {
			// Skipped for the same reason, reported as a different fact: an
			// unreadable count is not evidence that a worktree holds the
			// file, and telling the operator to run cfo cleanup would send
			// them to a remedy that cannot help.
			skipped.LinkCheckFailed = append(skipped.LinkCheckFailed, path)
			continue
		}
		if shared {
			skipped.WorktreeShared = append(skipped.WorktreeShared, path)
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
	return owned, skipped
}

// envFileRank is a file's place in dotenv's layering, higher winning.
func envFileRank(base string) int {
	return slices.Index(envFileNames, base)
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
// This prune alone is not sufficient, and never was: worktree provisioning
// shares .env by hardlink, so <project>/.worktrees/<id>/.env is the same
// inode as <project>/.env. A goblin that writes its worktree .env in place -
// every ordinary redirect, Set-Content, or writeFileSync does - has written
// the Overlord's file, and the primary path is never pruned. Skipping the
// worktree path removes the second reading of one file; it cannot tell the
// two names apart.
//
// What closes it is the link count, checked in envOwners: a local file with
// more than one directory entry is shared with a live worktree, so it is not
// an origin the store follows. The count drops back to one when cfo cleanup
// returns the worktree, so the pause clears itself.
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

// envWrite is one store write a .env rotation resolved to: the key it lands
// on, and whether that key already held a different value.
type envWrite struct {
	rotation envRotation
	key      Key
	refresh  bool
}

// planWrites resolves each rotation to the single store key it would write,
// and keeps one write per key.
//
// The run deduplicates by credential and the store deduplicates by key, and
// those are not the same set. A refresh targets whichever key in a
// credential's chain already holds a value, so a manifest where one service's
// alias target is another service's declared name yields two credentials that
// land on one key. Writing both leaves the key holding whichever ran last and
// makes the dispatch line report a refresh that no longer survives, which is
// the one thing an operator has to be able to trust about this feature.
//
// Where two rotations contend for a key, the credential whose declared name
// IS that key wins, matching the order Resolver.lookup consults; otherwise the
// earlier rotation wins, which credentialRotations already made deterministic.
func planWrites(store Store, scope string, chains map[string][]string, wanted map[string]bool, rotations []envRotation) ([]envWrite, error) {
	var writes []envWrite
	at := map[string]int{}
	for _, rotation := range rotations {
		write, ok, err := resolveWrite(store, scope, chains, wanted, rotation)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		index, contested := at[write.key.String()]
		if !contested {
			at[write.key.String()] = len(writes)
			writes = append(writes, write)
			continue
		}
		held := writes[index]
		if write.key.Name == write.rotation.credential && held.key.Name != held.rotation.credential {
			writes[index] = write
		}
	}
	return writes, nil
}

// resolveWrite is the one key a rotation lands on: the declared name's key
// when nothing answers this credential yet, otherwise the key in its chain
// that already holds a value, walked in the order Resolver.lookup consults
// them so the live key is the one that changes. A key further down the chain
// is one resolution never reaches, and writing it would leave the live one
// stale. A value the store already holds is not a write at all.
func resolveWrite(store Store, scope string, chains map[string][]string, wanted map[string]bool, rotation envRotation) (envWrite, bool, error) {
	if strings.TrimSpace(rotation.value) == "" {
		return envWrite{}, false, nil
	}
	if wanted[rotation.credential] {
		return envWrite{rotation: rotation, key: Scoped(scope, rotation.credential)}, true, nil
	}
	for _, candidate := range chains[rotation.credential] {
		key := Scoped(scope, candidate)
		existing, found, err := store.Get(key)
		if err != nil {
			return envWrite{}, false, err
		}
		if !found || existing == "" {
			continue
		}
		if existing == rotation.value {
			return envWrite{}, false, nil
		}
		return envWrite{rotation: rotation, key: key, refresh: true}, true, nil
	}
	return envWrite{}, false, nil
}

// LinkCheckFailedLine names the local files whose hard link count could not be
// read, so an operator can see that adoption skipped them. They are skipped
// exactly as a worktree-shared file is - a file that cannot be shown to be
// private may be one a goblin can write - but the cause is reported separately
// because returning a worktree will not change it: the remedy is to find out
// why the file could not be inspected.
//
// It names paths. No credential name and no value appears here, the same rule
// the adoption line keeps.
func LinkCheckFailedLine(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return fmt.Sprintf("link check failed for %d local file(s) (%s); they were not read",
		len(paths), strings.Join(paths, ", "))
}
