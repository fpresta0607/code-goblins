// Package openrouter reads the spend and budget an OpenRouter API key reports
// at the provider, for the board's usage dial. It reads only: no budget, key
// or spending policy is changed here, and the key itself never leaves this
// package. It is the key's own spend, shared by every caller of that key,
// never a sum of what the fleet's goblins estimate they used.
package openrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// KeyURL is OpenRouter's current-key endpoint, which answers with the usage
// and budget of the key its Authorization header carries.
const KeyURL = "https://openrouter.ai/api/v1/key"

// EnvKey is the variable Pi reads a key from when its auth file names none.
const EnvKey = "OPENROUTER_API_KEY"

// Provider is the provider's name on the board.
const Provider = "openrouter"

// Status is how usable a reading is on the board.
const (
	// StatusAvailable is a reading this pass took.
	StatusAvailable = "available"
	// StatusMissing is no key to read: the profile has none, or its entry
	// names none.
	StatusMissing = "missing"
	// StatusAuthRequired is a key OpenRouter refused: it must be replaced.
	StatusAuthRequired = "auth_required"
	// StatusRateLimited is OpenRouter refusing the reading itself for now.
	StatusRateLimited = "rate_limited"
	// StatusUnavailable is anything else that kept the reading from being
	// taken, from an answer this build cannot read to no answer at all.
	StatusUnavailable = "unavailable"
	// StatusStale is the supervisor's verdict on an available reading older
	// than quota.MaxAge, or one whose window has since rolled over, which the
	// reader itself never returns.
	StatusStale = "stale"
	// StatusUnattributed is the supervisor's verdict that the credential a
	// reading was made with is not proved to be the one a running session
	// started with, which the reader itself never returns.
	StatusUnattributed = "unattributed"
)

// The windows a key's own limit resets over, in the words the board shows:
// a lifetime limit never resets, and an empty period is a window this build
// cannot count, which shows the limit without a spend.
const (
	PeriodDaily    = "daily"
	PeriodWeekly   = "weekly"
	PeriodMonthly  = "monthly"
	PeriodLifetime = "lifetime"
)

// keyScopes are the budget layers that describe this key's own spend. Every
// other scope, a member or workspace budget, spends for that scope and never
// for the key that draws on it.
var keyScopes = map[string]bool{"key_limit": true, "api_key": true, "workspace_default_key": true}

// Reading is one key's spend as OpenRouter last reported it: the dollars
// spent in the window the key's own limit counts over, against that limit.
// The dollars are OpenRouter's own credit usage: the external BYOK spend is
// reported beside them and is never added to them.
type Reading struct {
	Status string `json:"status"`
	// SpentUSD is the key's spend in Period, nil when nothing usable was
	// read: a status that is not available, or a window this build cannot
	// count.
	SpentUSD *float64 `json:"spent_usd"`
	// LimitUSD is the key's own spending limit, nil when it has none.
	LimitUSD *float64 `json:"limit_usd"`
	// RemainingUSD is what is left of LimitUSD, as OpenRouter reports it.
	RemainingUSD *float64 `json:"remaining_usd"`
	// Period is the window LimitUSD counts SpentUSD over: daily, weekly,
	// monthly, or lifetime for a limit that never resets.
	Period string `json:"period"`
	// ResetsAt is when that window rolls over: as a key-scoped budget states
	// it, else at the documented UTC boundary of the period, and zero when
	// the key never resets.
	ResetsAt time.Time `json:"resets_at"`
	// ReadAt is when this reading was taken, which the board shows beside
	// the dollars so an old one is never read as a fresh one.
	ReadAt time.Time `json:"read_at"`
	// ByokUSD is the external BYOK spend in the same window, which the key's
	// limit counts only when IncludesByok says so, and which is never added
	// to SpentUSD.
	ByokUSD *float64 `json:"byok_usd"`
	// IncludesByok is whether the key's limit counts that BYOK spend.
	IncludesByok bool `json:"includes_byok"`
	// Blocking is a budget layer that is not this key's own and that stops
	// requests before the key's limit does, absent when the key's own limit
	// is what blocks.
	Blocking *BlockingBudget `json:"blocking,omitempty"`
	// KeyID is a fingerprint of the credential the reading was made with,
	// never the credential or part of it. A reading whose KeyID differs from
	// the one before it belongs to another key, and neither may be shown for
	// the other.
	KeyID string `json:"key_id"`
	// Profile names the Pi profile the credential came from: default for
	// Pi's own folder under the user's profile, else that folder's own name.
	Profile string `json:"profile"`
	// Detail says in a few words why the status is what it is, and never
	// holds a key, an authorization header, a provider document or any text
	// a configuration left to be resolved.
	Detail string `json:"detail"`
	// CredentialSince is when the credential's own file was last written, and
	// zero for a key the environment names: it says whether a session that
	// started earlier can be reading this credential, and it never reaches
	// the snapshot.
	CredentialSince time.Time `json:"-"`
}

// BlockingBudget is a budget layer that outranks the key's own limit: a
// member or workspace budget, whose spend is that scope's and not this key's.
type BlockingBudget struct {
	Scope    string  `json:"scope"`
	SpentUSD float64 `json:"spent_usd"`
	LimitUSD float64 `json:"limit_usd"`
	Period   string  `json:"period"`
}

// Source is one Pi profile and the environment its session's terminal started
// with, where its OpenRouter credential comes from. Env is the environment
// the fleet's launch records name for that session, a project's own overrides
// over the machine's user environment, and it holds a credential: it is never
// serialized.
type Source struct {
	// Dir is Pi's own configuration folder, where auth.json and settings.json
	// are.
	Dir string
	// Profile names Dir for a person, as Reading.Profile does.
	Profile string
	// Env holds the environment the session started with, by name, or nil.
	Env map[string]string
}

// Reader resolves a source's credential and reads its spend. Client and Now
// exist for tests; a nil Client is a client with a timeout that follows no
// redirect, and nil Now is the real clock. URL is the endpoint, and empty is
// KeyURL.
type Reader struct {
	Source
	Client *http.Client
	Now    func() time.Time
	URL    string
}

// maxAnswer bounds what OpenRouter's answer may cost to read.
const maxAnswer = 1 << 16

// Read resolves the source's OpenRouter credential and reads what OpenRouter
// says about it, within ctx. It resolves the key the way Pi does, never runs a
// command a configuration names, and never returns the key it read with.
func (r Reader) Read(ctx context.Context) Reading {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	reading := Reading{Status: StatusUnavailable, ReadAt: now().UTC(), Profile: r.Source.Profile}
	key, written, problem := r.credential()
	if problem != nil {
		reading.Status = StatusMissing
		reading.Detail = problem.Error()
		reading.CredentialSince = written
		return reading
	}
	if key == "" {
		reading.Status = StatusMissing
		reading.Detail = "Pi has no OpenRouter key in this profile"
		reading.CredentialSince = written
		return reading
	}
	reading.KeyID, reading.CredentialSince = fingerprint(key), written
	answer, status, detail := r.fetch(ctx, key)
	if status != StatusAvailable {
		reading.Status, reading.Detail = status, detail
		return reading
	}
	if answer.Data.IsManagementKey != nil && *answer.Data.IsManagementKey {
		reading.Status, reading.Detail = StatusUnavailable, "this credential is an OpenRouter management key, which spends nothing of its own"
		return reading
	}
	readSpend(&reading, *answer.Data)
	return reading
}

// fetch asks OpenRouter about key, and reports the answer's status and why it
// is not available, in words this build fixes: a provider's reason phrase, a
// document and a configuration's own text never reach a dial.
func (r Reader) fetch(ctx context.Context, key string) (answer, string, string) {
	var empty answer
	endpoint := r.URL
	if endpoint == "" {
		endpoint = KeyURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return empty, StatusUnavailable, "the reading could not be made"
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")
	response, err := r.client().Do(request)
	if err != nil {
		return empty, StatusUnavailable, "OpenRouter did not answer"
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil {
		return empty, StatusUnavailable, "OpenRouter's answer could not be read"
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return empty, StatusAuthRequired, "OpenRouter refused this key"
	case http.StatusTooManyRequests:
		return empty, StatusRateLimited, "OpenRouter rate limited the reading"
	default:
		return empty, StatusUnavailable, "OpenRouter answered HTTP " + strconv.Itoa(response.StatusCode)
	}
	if err := json.Unmarshal(body, &empty); err != nil || empty.Data == nil || empty.Data.Usage == nil {
		return empty, StatusUnavailable, "OpenRouter answered with no key reading"
	}
	return empty, StatusAvailable, ""
}

// client is the reader's client, which follows no redirect: OpenRouter's
// answer comes from the host the key was read for, and a redirect elsewhere
// would send the key to a host nobody named.
func (r Reader) client() *http.Client {
	client := http.Client{Timeout: 10 * time.Second}
	if r.Client != nil {
		client = *r.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

// credential resolves the key Pi would send to OpenRouter: the provider's
// entry in the profile's auth.json, which Pi puts ahead of the environment,
// then the environment's OPENROUTER_API_KEY. It also reports when the file it
// read was last written, which is zero for an environment key.
func (r Reader) credential() (string, time.Time, error) {
	key, written, err := r.authFileKey()
	if err != nil || key != "" {
		return key, written, err
	}
	return environmentValue(r.Source.Env, EnvKey), time.Time{}, nil
}

// DefaultProvider is the provider Pi starts with under dir: its settings'
// defaultProvider, and empty when the settings cannot be read or name none.
func DefaultProvider(dir string) string {
	if dir == "" {
		return ""
	}
	data, err := fsx.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		DefaultProvider string `json:"defaultProvider"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return strings.TrimSpace(settings.DefaultProvider)
}

// authFileKey reads the OpenRouter entry of the profile's auth file and
// resolves the key it holds, as Pi's own resolver does. An entry that
// resolves the key by running a command is refused rather than run: a usage
// indicator never executes a command a configuration names.
func (r Reader) authFileKey() (string, time.Time, error) {
	if r.Source.Dir == "" {
		return "", time.Time{}, nil
	}
	path := filepath.Join(r.Source.Dir, "auth.json")
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, errors.New("Pi's auth file cannot be read")
	}
	// The file's own time dates what the file holds. A key it holds as an
	// expression of an environment variable is dated by neither: the value
	// may have changed since, so it carries no written time at all.
	var written time.Time
	if info, err := os.Stat(path); err == nil {
		written = info.ModTime().UTC()
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil {
		return "", written, errors.New("Pi's auth file is not a JSON object")
	}
	entryJSON, exists := document[Provider]
	if !exists {
		return "", written, nil
	}
	var entry struct {
		Type string            `json:"type"`
		Key  *string           `json:"key"`
		Env  map[string]string `json:"env"`
	}
	if json.Unmarshal(entryJSON, &entry) != nil {
		return "", written, errors.New("Pi's OpenRouter entry is not an object")
	}
	// Pi resolves a key only for an api_key credential: it hands back any
	// other entry without one, so this board has no key to read there.
	if entry.Type != "api_key" {
		return "", written, errors.New("Pi's OpenRouter credential is not an API key")
	}
	if entry.Key == nil || *entry.Key == "" {
		return "", written, errors.New("Pi's OpenRouter entry names no key")
	}
	if strings.HasPrefix(*entry.Key, "!") {
		return "", written, errors.New("Pi's OpenRouter key is resolved by a command, which the board does not run")
	}
	key, fromSessionEnv, err := resolveKeyValue(*entry.Key, entry.Env, r.Source.Env)
	if err != nil {
		return "", written, err
	}
	if key == "" {
		return "", written, errors.New("Pi's OpenRouter entry resolves no key")
	}
	if fromSessionEnv {
		written = time.Time{}
	}
	return key, written, nil
}

// environmentName is the name an environment variable may have, as Pi holds
// it: a name that does not match is no reference at all, and the text stands
// as written.
var (
	environmentName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	environmentNamePrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// resolveKeyValue resolves a credential's key expression the way Pi resolves
// it: $$ and $! are the characters they name, ${NAME} and $NAME read the
// entry's own environment and then the session's, an empty value in either
// falls through, and text that is no environment reference stays as it is. A
// variable neither source holds leaves the key unresolved, and the problem
// then names that variable and nothing else of the expression. It also
// reports whether any part of the key came from the session's environment,
// whose value at a session's start no file's time records.
func resolveKeyValue(expression string, scope, env map[string]string) (string, bool, error) {
	var out strings.Builder
	var missing []string
	fromSessionEnv := false
	for index := 0; index < len(expression); {
		dollar := strings.IndexByte(expression[index:], '$')
		if dollar < 0 {
			out.WriteString(expression[index:])
			break
		}
		dollar += index
		out.WriteString(expression[index:dollar])
		if dollar+1 >= len(expression) {
			out.WriteByte('$')
			index = dollar + 1
			continue
		}
		switch next := expression[dollar+1]; {
		case next == '$' || next == '!':
			out.WriteByte(next)
			index = dollar + 2
		case next == '{':
			end := strings.IndexByte(expression[dollar+2:], '}')
			if end < 0 {
				out.WriteByte('$')
				index = dollar + 1
				continue
			}
			name := expression[dollar+2 : dollar+2+end]
			index = dollar + 2 + end + 1
			if !environmentName.MatchString(name) {
				out.WriteString(expression[dollar:index])
				continue
			}
			value, fromSession, held := resolveEnvironment(name, scope, env)
			if !held {
				missing = append(missing, name)
				continue
			}
			fromSessionEnv = fromSessionEnv || fromSession
			out.WriteString(value)
		default:
			name := environmentNamePrefix.FindString(expression[dollar+1:])
			if name == "" {
				out.WriteByte('$')
				index = dollar + 1
				continue
			}
			index = dollar + 1 + len(name)
			value, fromSession, held := resolveEnvironment(name, scope, env)
			if !held {
				missing = append(missing, name)
				continue
			}
			fromSessionEnv = fromSessionEnv || fromSession
			out.WriteString(value)
		}
	}
	if len(missing) > 0 {
		return "", false, errors.New("Pi's OpenRouter key names an environment variable that is not set: " + strings.Join(uniqueNames(missing), ", "))
	}
	return out.String(), fromSessionEnv, nil
}

// resolveEnvironment reads name from the credential's own environment and
// then the session's, where an empty value falls through, as Pi resolves one.
// It reports a value the session's environment held, which the credential's
// file does not date.
func resolveEnvironment(name string, scope, env map[string]string) (string, bool, bool) {
	if value := scope[name]; value != "" {
		return value, false, true
	}
	if value := environmentValue(env, name); value != "" {
		return value, true, true
	}
	return "", false, false
}

// environmentValue reads name from env the way Windows does, where names
// compare without case.
func environmentValue(env map[string]string, name string) string {
	if value := env[name]; value != "" {
		return value
	}
	for held, value := range env {
		if value != "" && strings.EqualFold(held, name) {
			return value
		}
	}
	return ""
}

// uniqueNames is names without a repeat, in the order they were met.
func uniqueNames(names []string) []string {
	seen := map[string]bool{}
	unique := names[:0]
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		unique = append(unique, name)
	}
	return unique
}

// fingerprint is the reading's handle on the credential it was made with: a
// hash of the key, which tells two keys apart and is no part of either.
func fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// readSpend fills reading with the dollars, limit and window OpenRouter's
// answer states, and says in the reading when the key's window is one this
// build cannot count.
func readSpend(reading *Reading, data keyData) {
	reading.Status = StatusAvailable
	reading.LimitUSD = data.Limit
	if data.IncludeByokInLimit != nil {
		reading.IncludesByok = *data.IncludeByokInLimit
	}
	// A member or workspace budget outranks the key's own limit whether or
	// not the key has one.
	reading.Blocking = blockingBudget(data.EffectiveBudget)
	if data.Limit == nil {
		// A key with no limit resets nothing, so the total it reports is the
		// only figure no window cuts.
		reading.Period = PeriodLifetime
		reading.SpentUSD = data.Usage
		reading.ByokUSD = data.ByokUsage
		return
	}
	reading.Period = periodOf(data.LimitReset)
	reading.SpentUSD = counter(reading.Period, data)
	if reading.SpentUSD == nil {
		if reading.Period == "" {
			reading.Detail = "the key's limit resets over a window this board cannot count"
		} else {
			reading.Detail = "OpenRouter answered with no " + reading.Period + " spend"
		}
		return
	}
	reading.ByokUSD = byokCounter(reading.Period, data)
	if reading.RemainingUSD = data.LimitRemaining; reading.RemainingUSD == nil && !reading.IncludesByok {
		remaining := *data.Limit - *reading.SpentUSD
		reading.RemainingUSD = &remaining
	}
	if reading.ResetsAt = keyResetsAt(data, reading.Period); reading.ResetsAt.IsZero() {
		// A key with a period and no budget row still rolls over on the
		// documented boundary, so a reading taken yesterday never reads as
		// this period's.
		reading.ResetsAt = rollover(reading.Period, reading.ReadAt)
	}
}

// periodOf names the window a key's own limit resets over. A limit with no
// reset word never resets, which OpenRouter counts in total; a word this build
// does not know is no window it can count, so it is "".
func periodOf(reset *string) string {
	if reset == nil {
		return PeriodLifetime
	}
	switch strings.ToLower(strings.TrimSpace(*reset)) {
	case "":
		return PeriodLifetime
	case PeriodDaily:
		return PeriodDaily
	case PeriodWeekly:
		return PeriodWeekly
	case PeriodMonthly:
		return PeriodMonthly
	}
	return ""
}

// counter is the key's own credit usage in period, and nil for a window this
// build cannot count or a figure OpenRouter left out.
func counter(period string, data keyData) *float64 {
	switch period {
	case PeriodDaily:
		return data.UsageDaily
	case PeriodWeekly:
		return data.UsageWeekly
	case PeriodMonthly:
		return data.UsageMonthly
	case PeriodLifetime:
		return data.Usage
	}
	return nil
}

// byokCounter is the external BYOK usage in the same window, kept apart from
// the key's own credit usage.
func byokCounter(period string, data keyData) *float64 {
	switch period {
	case PeriodDaily:
		return data.ByokUsageDaily
	case PeriodWeekly:
		return data.ByokUsageWeekly
	case PeriodMonthly:
		return data.ByokUsageMonthly
	case PeriodLifetime:
		return data.ByokUsage
	}
	return nil
}

// rollover is when the documented window of a key's own limit next rolls
// over: the next UTC midnight for a daily window, the next Monday's UTC
// midnight for a weekly one, and the first UTC midnight of the next month for
// a monthly one. A lifetime or uncountable window has none.
func rollover(period string, at time.Time) time.Time {
	utc := at.UTC()
	midnight := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case PeriodDaily:
		return midnight.AddDate(0, 0, 1)
	case PeriodWeekly:
		days := (8 - int(midnight.Weekday())) % 7
		if days == 0 {
			days = 7
		}
		return midnight.AddDate(0, 0, days)
	case PeriodMonthly:
		return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	}
	return time.Time{}
}

// keyResetsAt is when the window the key's limit counts over rolls over, as
// the key-scoped budget for that same window says. The key's own limit fields
// carry no time, so a budget for another window names no time this reading
// can show.
func keyResetsAt(data keyData, period string) time.Time {
	for _, layer := range data.Budgets {
		if !keyScopes[layer.Scope] || layer.ResetsAt == nil || periodOf(layer.ResetInterval) != period || period == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339, strings.TrimSpace(*layer.ResetsAt)); err == nil {
			return at.UTC()
		}
	}
	return time.Time{}
}

// blockingBudget is the budget layer enforcement stops on first, when it is
// not the key's own: its spend is that scope's, and the board names it by its
// scope rather than as this key's.
func blockingBudget(effective *budget) *BlockingBudget {
	if effective == nil || keyScopes[effective.Scope] {
		return nil
	}
	return &BlockingBudget{Scope: effective.Scope, SpentUSD: effective.SpendUSD, LimitUSD: effective.LimitUSD, Period: periodOf(effective.ResetInterval)}
}

// keyData is the part of OpenRouter's key document the dial reads. Every
// figure is a pointer, so a field the answer left out is no zero the dial
// would show.
type keyData struct {
	Usage              *float64 `json:"usage"`
	UsageDaily         *float64 `json:"usage_daily"`
	UsageWeekly        *float64 `json:"usage_weekly"`
	UsageMonthly       *float64 `json:"usage_monthly"`
	ByokUsage          *float64 `json:"byok_usage"`
	ByokUsageDaily     *float64 `json:"byok_usage_daily"`
	ByokUsageWeekly    *float64 `json:"byok_usage_weekly"`
	ByokUsageMonthly   *float64 `json:"byok_usage_monthly"`
	Limit              *float64 `json:"limit"`
	LimitRemaining     *float64 `json:"limit_remaining"`
	LimitReset         *string  `json:"limit_reset"`
	IncludeByokInLimit *bool    `json:"include_byok_in_limit"`
	IsManagementKey    *bool    `json:"is_management_key"`
	Budgets            []budget `json:"budgets"`
	EffectiveBudget    *budget  `json:"effective_budget"`
}

// answer is OpenRouter's current-key document: the key's figures under data,
// which no payload at all leaves empty and unreadable.
type answer struct {
	Data *keyData `json:"data"`
}

// budget is one layer enforcement checks, as the key document states it.
type budget struct {
	Scope         string  `json:"scope"`
	LimitUSD      float64 `json:"limit_usd"`
	SpendUSD      float64 `json:"spend_usd"`
	RemainingUSD  float64 `json:"remaining_usd"`
	ResetInterval *string `json:"reset_interval"`
	ResetsAt      *string `json:"resets_at"`
	IncludeByok   bool    `json:"include_byok"`
}
