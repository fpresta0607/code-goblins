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
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// KeyURL is OpenRouter's current-key endpoint, which answers with the usage
// and budget of the key its Authorization header carries.
const KeyURL = "https://openrouter.ai/api/v1/key"

// EnvKey is the variable Pi and OpenRouter read a key from when the auth file
// names none.
const EnvKey = "OPENROUTER_API_KEY"

// Provider is the provider's name on the board.
const Provider = "openrouter"

// Status is how usable a reading is on the board.
const (
	// StatusAvailable is a reading this pass took.
	StatusAvailable = "available"
	// StatusMissing is no key to read: Pi has none, or its entry names none.
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
	// ResetsAt is when that window rolls over, zero while OpenRouter gives
	// no time for it.
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
	// Detail says in a few words why the status is what it is, and never
	// holds a key, an authorization header or a provider document.
	Detail string `json:"detail"`
	// Configured is whether the Pi configuration this machine holds names
	// OpenRouter as its provider, whether or not a key resolves today.
	Configured bool `json:"configured"`
}

// BlockingBudget is a budget layer that outranks the key's own limit: a
// member or workspace budget, whose spend is that scope's and not this key's.
type BlockingBudget struct {
	Scope    string  `json:"scope"`
	SpentUSD float64 `json:"spent_usd"`
	LimitUSD float64 `json:"limit_usd"`
	Period   string  `json:"period"`
}

// Reader resolves the key Pi uses and reads its spend. Dir is Pi's own
// configuration folder, ~/.pi/agent unless PI_CODING_AGENT_DIR names another,
// where its auth.json and settings.json are.
type Reader struct {
	Dir string
	// Client makes the one request; nil is a client with a timeout that
	// follows no redirect, so the key reaches the host it was read for and
	// no other.
	Client *http.Client
	// Env reads the process environment, and nil is os.Getenv.
	Env func(string) string
	// Now is the clock, and nil is the real one.
	Now func() time.Time
	// URL is the endpoint, and empty is KeyURL: a test answers from its own.
	URL string
}

// maxAnswer bounds what OpenRouter's answer may cost to read.
const maxAnswer = 1 << 16

// Read resolves Pi's OpenRouter credential and reads what OpenRouter says
// about it, within ctx. It never runs a command a configuration names, and it
// never returns the key it read with.
func (r Reader) Read(ctx context.Context) Reading {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	reading := Reading{Status: StatusUnavailable, ReadAt: now().UTC()}
	key, configured, problem := r.credential()
	reading.Configured = configured
	if problem != nil {
		reading.Status = StatusMissing
		reading.Detail = problem.Error()
		return reading
	}
	if key == "" {
		reading.Status = StatusMissing
		reading.Detail = "Pi has no OpenRouter key on this machine"
		return reading
	}
	reading.KeyID = fingerprint(key)
	answer, status, detail := r.fetch(ctx, key)
	if status != StatusAvailable {
		reading.Status, reading.Detail = status, detail
		return reading
	}
	if answer.Data.IsManagementKey != nil && *answer.Data.IsManagementKey {
		reading.Status, reading.Detail = StatusUnavailable, "this credential is an OpenRouter management key, which spends nothing of its own"
		return reading
	}
	readSpend(&reading, answer)
	return reading
}

// fetch asks OpenRouter about key, and reports the answer's status and why it
// is not available, in words that never carry the key.
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
		return empty, StatusUnavailable, "OpenRouter answered " + response.Status
	}
	if err := json.Unmarshal(body, &empty); err != nil {
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

// credential resolves the key Pi would send to OpenRouter, the way Pi does:
// the provider's entry in auth.json, which Pi puts ahead of the environment,
// then OPENROUTER_API_KEY. It also reports whether Pi's own settings name
// OpenRouter at all.
func (r Reader) credential() (string, bool, error) {
	getenv := r.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	configured := r.configuresOpenRouter()
	key, err := r.authFileKey(getenv)
	if err != nil {
		return "", configured, err
	}
	if key != "" {
		return key, configured, nil
	}
	return strings.TrimSpace(getenv(EnvKey)), configured, nil
}

// configuresOpenRouter reports whether Pi's settings name OpenRouter as the
// provider it starts with. A settings file it cannot read names none.
func (r Reader) configuresOpenRouter() bool {
	if r.Dir == "" {
		return false
	}
	data, err := fsx.ReadFile(filepath.Join(r.Dir, "settings.json"))
	if err != nil {
		return false
	}
	var settings struct {
		DefaultProvider string `json:"defaultProvider"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(settings.DefaultProvider), Provider)
}

// authFileKey reads the OpenRouter entry of Pi's auth file and resolves the
// key it holds. An entry that resolves the key by running a command is
// refused rather than run: a usage indicator never executes a command a
// configuration names.
func (r Reader) authFileKey(getenv func(string) string) (string, error) {
	if r.Dir == "" {
		return "", nil
	}
	data, err := fsx.ReadFile(filepath.Join(r.Dir, "auth.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("Pi's auth file cannot be read")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil {
		return "", errors.New("Pi's auth file is not a JSON object")
	}
	entryJSON, exists := document[Provider]
	if !exists {
		return "", nil
	}
	var entry struct {
		Type string            `json:"type"`
		Key  *string           `json:"key"`
		Env  map[string]string `json:"env"`
	}
	if json.Unmarshal(entryJSON, &entry) != nil {
		return "", errors.New("Pi's OpenRouter entry is not an object")
	}
	if entry.Type != "" && entry.Type != "api_key" {
		return "", errors.New("Pi's OpenRouter credential is not an API key")
	}
	if entry.Key == nil {
		return "", errors.New("Pi's OpenRouter entry names no key")
	}
	value := strings.TrimSpace(*entry.Key)
	if value == "" {
		return "", errors.New("Pi's OpenRouter entry names no key")
	}
	if strings.HasPrefix(value, "!") {
		return "", errors.New("Pi's OpenRouter key is resolved by a command, which the board does not run")
	}
	key, err := resolveKeyValue(value, entry.Env, getenv)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", errors.New("Pi's OpenRouter entry resolves no key")
	}
	return key, nil
}

// resolveKeyValue replaces $NAME and ${NAME} in value with what the entry's
// own env map or the process environment holds, as Pi does, and turns its $$,
// $! escapes into the character they name. A variable neither source holds
// leaves the key unresolved, and the error names the variable and never a
// value.
func resolveKeyValue(value string, scope map[string]string, getenv func(string) string) (string, error) {
	lookup := func(name string) (string, bool) {
		if held, exists := scope[name]; exists {
			return held, true
		}
		if held := getenv(name); held != "" {
			return held, true
		}
		return "", false
	}
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '$' {
			out.WriteByte(value[i])
			i++
			continue
		}
		if i+1 >= len(value) {
			out.WriteByte('$')
			i++
			continue
		}
		switch next := value[i+1]; {
		case next == '$':
			out.WriteByte('$')
			i += 2
		case next == '!':
			out.WriteByte('!')
			i += 2
		case next == '{':
			end := strings.IndexByte(value[i+2:], '}')
			if end < 0 {
				return "", errors.New("Pi's OpenRouter key is not a form this board can resolve")
			}
			name := value[i+2 : i+2+end]
			held, exists := lookup(name)
			if !exists {
				return "", errors.New("Pi's OpenRouter key names an environment variable that is not set: " + name)
			}
			out.WriteString(held)
			i += 2 + end + 1
		default:
			end := i + 1
			for end < len(value) && isNameByte(value[end]) {
				end++
			}
			if end == i+1 {
				out.WriteByte('$')
				i++
				continue
			}
			name := value[i+1 : end]
			held, exists := lookup(name)
			if !exists {
				return "", errors.New("Pi's OpenRouter key names an environment variable that is not set: " + name)
			}
			out.WriteString(held)
			i = end
		}
	}
	return strings.TrimSpace(out.String()), nil
}

// isNameByte reports whether b may sit in an environment variable's name.
func isNameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
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
func readSpend(reading *Reading, answer answer) {
	data := answer.Data
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
	reading.ResetsAt = keyResetsAt(data, reading.Period)
}

// periodOf names the window a key's own limit resets over. A limit with no
// reset word never resets, which OpenRouter calls a lifetime budget; a word
// this build does not know is no window it can count, so it is "".
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

// answer is OpenRouter's current-key document: the key's figures under data.
type answer struct {
	Data keyData `json:"data"`
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
