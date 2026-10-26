package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/openrouter"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// KeySpend is one API key's spend at its provider, for the dial beside the
// subscriptions: a provider that bills by the key rather than by a
// subscription window, as OpenRouter does for Pi.
type KeySpend struct {
	Provider string `json:"provider"`
	openrouter.Reading
}

// keySpendSource is one credential source a live Pi session uses: the profile
// folder Pi reads and the environment its terminal started with. Env holds a
// credential and never reaches a snapshot.
type keySpendSource struct {
	// Key identifies the source among the readings: its folder and the
	// environment key its launch named, which is never stored itself.
	Key     string
	Dir     string
	Profile string
	Env     map[string]string
}

// keySpendSession is one live Pi session and what the fleet's records prove
// about where its credential comes from.
type keySpendSession struct {
	ID string
	// Provider is the provider it runs on, in the provider/model form Pi's
	// launch takes, or Pi's own default: empty when neither says.
	Provider string
	// Since is when its session started, and zero when the fleet recorded
	// none.
	Since time.Time
	// Source is the key of the credential source it reads.
	Source string
}

// keySpendPlan is what one snapshot found: every credential source a live Pi
// session uses, and the sessions bound to each.
type keySpendPlan struct {
	Sources  []keySpendSource
	Sessions []keySpendSession
}

// keySpendUsage is the dials' reading of each credential the live Pi sessions
// use, with what the fleet's records prove about it: the dollars spent, the
// limit they count against and the window that limit resets over. A Pi session
// on another provider is no dial, a session whose credential the records do
// not tie to it says so rather than claiming spend, and callers sharing one
// credential show one total.
func (s *Service) keySpendUsage(cfo cfoState, snapshot Snapshot) []KeySpend {
	plan := s.keySpendPlanFor(cfo, snapshot)
	s.mu.Lock()
	s.keySpendPlan = plan
	readings := maps.Clone(s.keySpendReadings)
	s.mu.Unlock()
	byCredential := map[string]KeySpend{}
	for _, source := range plan.Sources {
		reading, exists := readings[source.Key]
		if !exists {
			// The dials' timer has not read this source yet.
			continue
		}
		isUsed, isAttributed, problem := false, false, ""
		for _, session := range plan.Sessions {
			if session.Source != source.Key {
				continue
			}
			if session.Provider == "" {
				// The records name no provider for this session, so nothing
				// proves it spends this key's credit.
				isUsed = true
				if problem == "" {
					problem = "the fleet's records name no provider for this session"
				}
				continue
			}
			if session.Provider != openrouter.Provider {
				continue
			}
			isUsed = true
			attributed, why := attributeKeySpend(reading, session)
			if attributed {
				isAttributed = true
				break
			}
			if problem == "" {
				problem = why
			}
		}
		if !isUsed {
			continue
		}
		shown := KeySpend{Provider: openrouter.Provider, Reading: reading}
		switch {
		case !isAttributed:
			shown.Status, shown.Detail = openrouter.StatusUnattributed, problem
		case shown.Status == openrouter.StatusAvailable && (snapshot.At.Sub(shown.ReadAt) > quota.MaxAge || !shown.ResetsAt.IsZero() && !shown.ResetsAt.After(snapshot.At)):
			// A reading past its age, or one whose window has rolled over, is
			// stale rather than this period's.
			shown.Status = openrouter.StatusStale
		}
		identity := shown.KeyID
		if identity == "" {
			identity = source.Key
		}
		if held, exists := byCredential[identity]; exists && !isPreferredKeySpend(shown, held) {
			continue
		}
		byCredential[identity] = shown
	}
	dials := make([]KeySpend, 0, len(byCredential))
	for _, shown := range byCredential {
		dials = append(dials, shown)
	}
	slices.SortFunc(dials, func(a, b KeySpend) int {
		if order := strings.Compare(a.Profile, b.Profile); order != 0 {
			return order
		}
		return strings.Compare(a.KeyID, b.KeyID)
	})
	return dials
}

// isPreferredKeySpend says which of two readings of one credential the dial
// shows: the one whose dollars are that credential's own for a session, then
// the newer reading.
func isPreferredKeySpend(candidate, held KeySpend) bool {
	isProven := func(shown KeySpend) bool { return shown.Status != openrouter.StatusUnattributed }
	if isProven(candidate) != isProven(held) {
		return isProven(candidate)
	}
	return candidate.ReadAt.After(held.ReadAt)
}

// attributeKeySpend reports whether the fleet's records prove that reading is
// the credential session started with. A key the environment names has no
// written time, and a key written after the session started may be another
// credential than the one it runs on, so neither is that session's on
// evidence.
func attributeKeySpend(reading openrouter.Reading, session keySpendSession) (bool, string) {
	if reading.CredentialSince.IsZero() {
		return false, "the key's value comes from the environment, which this session's start did not record"
	}
	if session.Since.IsZero() {
		return false, "the fleet did not record when this session started"
	}
	if reading.CredentialSince.After(session.Since) {
		return false, "the credential was written after this session started"
	}
	return true, ""
}

// keySpendPlanFor is every credential source this snapshot's live Pi sessions
// use, and the sessions bound to each, as the fleet's own launch records and
// Pi's configuration name them: the project's worktree manifest over the
// machine's user environment, and the profile that environment names.
func (s *Service) keySpendPlanFor(cfo cfoState, snapshot Snapshot) keySpendPlan {
	var plan keySpendPlan
	userEnv := s.userEnvironment()
	defaultDir := piProfileDefault()
	seen := map[string]bool{}
	bind := func(id, model string, since time.Time, project string) {
		dir, profile, env := s.keySpendProfile(project, userEnv, defaultDir)
		provider := modelProvider(model)
		if provider == "" {
			provider = strings.ToLower(openrouter.DefaultProvider(dir))
		}
		// A session whose provider is another's spends nothing of OpenRouter's:
		// it is no credential source and asks OpenRouter for nothing.
		if provider != "" && provider != openrouter.Provider {
			return
		}
		key := keySpendSourceKey(dir, env["OPENROUTER_API_KEY"])
		if !seen[key] {
			seen[key] = true
			plan.Sources = append(plan.Sources, keySpendSource{Key: key, Dir: dir, Profile: profile, Env: env})
		}
		plan.Sessions = append(plan.Sessions, keySpendSession{ID: id, Provider: provider, Since: since, Source: key})
	}
	if cfo.registered && cfo.harness == "pi" && cfo.problem == "" && snapshot.Registration == "" {
		bind(cfo.session, cfoModel(cfo, snapshot), cfo.since, "")
	}
	for _, task := range snapshotLiveTasks(snapshot) {
		if task.Harness != "pi" {
			continue
		}
		bind(task.ID, task.Model, task.Since, task.Project)
	}
	slices.SortFunc(plan.Sources, func(a, b keySpendSource) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(plan.Sessions, func(a, b keySpendSession) int { return strings.Compare(a.ID, b.ID) })
	return plan
}

// keySpendProfile is the Pi profile folder one session reads and the
// environment its terminal started with: the project's own worktree manifest
// overrides over the machine's user environment.
func (s *Service) keySpendProfile(project string, userEnv map[string]string, defaultDir string) (string, string, map[string]string) {
	env := maps.Clone(userEnv)
	if env == nil {
		env = map[string]string{}
	}
	if project != "" {
		if manifest, err := worktree.Resolve(s.Store.Home.Data, project); err == nil {
			for name, value := range manifest.Env {
				env[strings.ToUpper(name)] = value
			}
		}
	}
	dir := strings.TrimSpace(env["PI_CODING_AGENT_DIR"])
	if dir == "" {
		dir = defaultDir
	}
	return dir, keySpendProfileName(dir, defaultDir), env
}

// keySpendProfileName names a profile folder for a person: default for Pi's
// own folder under the user's profile, else the folder's own name.
func keySpendProfileName(dir, defaultDir string) string {
	if dir == "" {
		return ""
	}
	if defaultDir != "" && strings.EqualFold(filepath.Clean(dir), filepath.Clean(defaultDir)) {
		return "default"
	}
	return filepath.Base(dir)
}

// keySpendSourceKey identifies one credential source: its profile folder and
// a fingerprint of the environment key its launch named, which is never
// stored itself.
func keySpendSourceKey(dir, envKey string) string {
	sum := sha256.Sum256([]byte(envKey))
	return dir + "\x00" + hex.EncodeToString(sum[:8])
}

// modelProvider is the provider a Pi model names, in the provider/model form
// Pi's launch takes: openrouter for openrouter/deepseek/..., and empty for a
// model that names none, whose provider is Pi's own default.
func modelProvider(model string) string {
	provider, rest, found := strings.Cut(strings.TrimSpace(model), "/")
	if !found || provider == "" || rest == "" {
		return ""
	}
	return strings.ToLower(provider)
}

// cfoModel is the model the registered CFO's session in its harness reported,
// which is where a Pi CFO's provider is read from.
func cfoModel(cfo cfoState, snapshot Snapshot) string {
	for index := len(snapshot.Sessions) - 1; index >= 0; index-- {
		session := snapshot.Sessions[index]
		if session.Role != "cfo" || session.Harness != "pi" {
			continue
		}
		if cfo.session != "" && session.ID != cfo.session {
			continue
		}
		return session.Model
	}
	return ""
}

// piProfileDefault is the folder Pi reads when nothing names another: the
// agent folder under the user's profile.
func piProfileDefault() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// userEnvironment is the environment Windows gives a new process of this
// user, by name, which every goblin's terminal starts from: not this
// process's own, which cfo serve inherited from whatever started it.
func (s *Service) userEnvironment() map[string]string {
	read := s.Options.UserEnvironment
	if read == nil {
		read = spawn.UserEnvironment
	}
	env := map[string]string{}
	entries, err := read()
	if err != nil {
		return env
	}
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		if found && name != "" && value != "" {
			env[strings.ToUpper(name)] = value
		}
	}
	return env
}

// readKeySpends reads each credential the last snapshot found a live Pi
// session using, within timeout, outside every lock, and keeps it for the
// dial. The read is the only thing the dial waits on: a board's snapshot never
// makes one.
func (s *Service) readKeySpends(ctx context.Context, timeout time.Duration) {
	if s.Options.KeySpend == nil {
		return
	}
	s.mu.Lock()
	plan := s.keySpendPlan
	before := maps.Clone(s.keySpendReadings)
	s.keySpendReadAt = time.Now()
	s.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	readings := make(map[string]openrouter.Reading, len(plan.Sources))
	for _, source := range plan.Sources {
		reading := s.Options.KeySpend(probe, openrouter.Source{Dir: source.Dir, Profile: source.Profile, Env: source.Env})
		readings[source.Key] = keptKeySpend(before[source.Key], reading)
	}
	s.mu.Lock()
	// A source no live session uses any more is no reading the dial keeps.
	s.keySpendReadings = readings
	s.mu.Unlock()
	s.notify()
}

// keptKeySpend is the reading the dial keeps of a credential: the one just
// taken when it answered, and the dollars of the reading before it only while
// that was the same credential, so a key that was rotated never shows the
// spend of the key it replaced. A read that did not answer keeps those dollars
// beside the reason it could not read them, and the dial shows them by their
// age.
func keptKeySpend(before, reading openrouter.Reading) openrouter.Reading {
	if reading.Status == openrouter.StatusAvailable || before.KeyID == "" || before.KeyID != reading.KeyID {
		return reading
	}
	kept := before
	kept.Status, kept.Detail = reading.Status, reading.Detail
	return kept
}
