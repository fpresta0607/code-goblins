// Package connections reads what the Connectors page shows, on three levels:
// this PC and its accounts, each project, and each goblin. The machine is
// checked once and each project once however many goblins it has, in the
// background; a goblin is read from its own session record, never by starting
// a harness. What a check finds is kept as a snapshot, and reading the
// snapshot starts nothing. Names and states only: no credential's value is
// read into, kept in or returned by any of it.
package connections

import (
	"strings"
	"time"
	"unicode"
)

// The four levels an item belongs to.
const (
	LevelPC      = "pc"
	LevelAccount = "account"
	LevelProject = "project"
	LevelGoblin  = "goblin"
)

// The statuses an item reads, a closed set.
const (
	// StatusVerified is a fact a probe, a sign-in check or the session itself
	// confirmed.
	StatusVerified = "verified"
	// StatusSet is present or resolved with nothing confirmed beyond that.
	StatusSet = "set"
	// StatusMissing is not there.
	StatusMissing = "missing"
	// StatusUnauthorized is rejected, or not signed in.
	StatusUnauthorized = "unauthorized"
	// StatusExpired is a sign-in the service said expired.
	StatusExpired = "expired"
	// StatusWrongTarget works and points at another project.
	StatusWrongTarget = "wrong_target"
	// StatusUnreachable did not answer.
	StatusUnreachable = "unreachable"
	// StatusFailed failed and did not say why.
	StatusFailed = "failed"
	// StatusNotGiven exists, and goblins are not given it.
	StatusNotGiven = "not_given"
	// StatusNotChecked is a check that did not run or did not finish; the
	// item's Detail says why.
	StatusNotChecked = "not_checked"
	// StatusEverywhere is a credential-like variable of this PC's user
	// environment, which every terminal and goblin carries with no grant.
	StatusEverywhere = "everywhere"
)

// The groups an item is listed under, by level.
const (
	GroupCLI              = "cli"
	GroupEnvironment      = "environment"
	GroupBuiltIn          = "built_in"
	GroupHarness          = "harness"
	GroupAccountConnector = "account_connector"
	GroupUserConfig       = "user_config"
	GroupService          = "service"
	GroupMCP              = "mcp"
	GroupLoaded           = "loaded"
	GroupFailed           = "failed"
	GroupCarried          = "carried"
	GroupNotGiven         = "not_given"
)

// Item is one row of a level.
type Item struct {
	// ID is unique within its level and scope: "<kind>:<name>", such as
	// service:supabase or cli:gh.
	ID    string `json:"id"`
	Level string `json:"level"`
	Group string `json:"group"`
	// Kind is cli, harness, credential, connector, mcp or service.
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// Detail says why, in plain words.
	Detail string `json:"detail,omitempty"`
	// Target is what the item points at, such as "Supabase project ...k3tv"
	// or the identity a manifest expects.
	Target string `json:"target,omitempty"`
	// Source says where the fact was read, in words.
	Source string `json:"source,omitempty"`
	// Credentials are a service's declared variable names.
	Credentials []Credential `json:"credentials,omitempty"`
	// Tools are a loaded MCP server's tool names, from the session record.
	Tools []string `json:"tools,omitempty"`
	// Actions are the repairs offered: store:NAME, login or cli.
	Actions []string `json:"actions,omitempty"`
	// Grant is what a goblin's row offers: grant, revoke, or nothing.
	Grant     string    `json:"grant,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Credential is a variable name a service declares and whether it resolves.
type Credential struct {
	Name       string `json:"name"`
	IsResolved bool   `json:"is_resolved"`
}

// Level is what one check of a level left.
type Level struct {
	Items []Item `json:"items"`
	// CheckedAt is zero until the first check ends.
	CheckedAt time.Time `json:"checked_at"`
	// Checking is whether a check is queued or running.
	Checking bool   `json:"checking"`
	Error    string `json:"error,omitempty"`
	// Basis is the fingerprint of what the level was checked against.
	Basis string `json:"basis,omitempty"`
}

// Project is the check of one project, named as auth.ProjectName names it.
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Level
}

// Goblin is what one goblin's own record says, showing only what differs from
// its project and this PC.
type Goblin struct {
	Task       string `json:"task"`
	Generation string `json:"generation"`
	Project    string `json:"project"`
	Harness    string `json:"harness"`
	// IsConfirmed is whether its session record said which servers loaded.
	IsConfirmed bool   `json:"is_confirmed"`
	Note        string `json:"note"`
	Level
}

// View is every level as the snapshots hold it.
type View struct {
	Machine  Level     `json:"machine"`
	Projects []Project `json:"projects"`
	Goblins  []Goblin  `json:"goblins"`
}

// validName is whether a name read from a configuration or a record is one
// the board can show.
func validName(name string) bool {
	return name != "" && len(name) <= 128 && strings.IndexFunc(name, unicode.IsControl) < 0 && !strings.Contains(name, "://")
}

// statusDetail is the plain reason behind a state a service's check named.
var statusDetail = map[string]string{
	StatusMissing:      "A credential it needs is not stored for this project.",
	StatusUnauthorized: "Its sign-in was rejected.",
	StatusExpired:      "Its sign-in expired.",
	StatusWrongTarget:  "Its credentials point at another project.",
	StatusUnreachable:  "It did not answer.",
	StatusFailed:       "Its check failed.",
}
