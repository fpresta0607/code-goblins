package connections

import (
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

type Entry struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	Status          string    `json:"status"`
	Detail          string    `json:"detail,omitempty"`
	Source          string    `json:"source,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
	Actions         []string  `json:"actions,omitempty"`
	Scope           string    `json:"scope"`
	LiveStatus      string    `json:"live_status"`
	AccountIdentity string    `json:"account_identity"`
	TargetIdentity  string    `json:"target_identity"`
	RestartRequired bool      `json:"restart_required"`
	RestartReason   string    `json:"restart_reason,omitempty"`
	// Account is the repository service a credential variable is declared by,
	// which the board lists it under, or empty for a variable no service
	// declares.
	Account string `json:"account,omitempty"`
	// Grant is what the board offers beside the entry: grant for a service or
	// a project MCP server the task does not carry, revoke for one its record
	// names, or empty for one no cfo auth grant changes.
	Grant string `json:"grant,omitempty"`
	// Tools are the tools an MCP server reported in the check, by name, which
	// its connector's page lists in two groups.
	Tools []Tool `json:"tools,omitempty"`
}

// Tool is one tool an MCP server reported: its name, and whether the server
// marks it as one that only reads.
type Tool struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
}

// toolLimit is the most tools one server's entry lists.
const toolLimit = 1024

// listedTools are the tools a server reported that its entry lists: those
// with a name the board can show, by name, and no more than toolLimit.
func listedTools(reported []Tool) []Tool {
	var listed []Tool
	for _, tool := range reported {
		if validName(tool.Name) {
			listed = append(listed, tool)
		}
	}
	slices.SortFunc(listed, func(a, b Tool) int { return strings.Compare(a.Name, b.Name) })
	if len(listed) > toolLimit {
		listed = listed[:toolLimit]
	}
	return listed
}

type Snapshot struct {
	Generation string           `json:"generation"`
	Runtime    *RuntimeIdentity `json:"runtime,omitempty"`
	Entries    []Entry          `json:"entries"`
	Checking   bool             `json:"checking"`
	Error      string           `json:"error,omitempty"`
	CheckedAt  time.Time        `json:"checked_at"`
}

type RuntimeIdentity struct {
	ProcessID int       `json:"process_id"`
	StartedAt time.Time `json:"started_at"`
	Workspace string    `json:"workspace"`
}

type RuntimeObservation struct {
	Identity  RuntimeIdentity
	Env, Args []string
}
type codexStatus struct {
	Name          string `json:"name"`
	RuntimeStatus string `json:"runtimeStatus"`
	AuthStatus    string `json:"authStatus"`
	// Tools are the server's tools by name, each with the protocol's own
	// annotations.
	Tools map[string]struct {
		Annotations struct {
			ReadOnly bool `json:"readOnlyHint"`
		} `json:"annotations"`
	} `json:"tools"`
}

// serviceEntry is an account's row: connected only when its probe passed, and
// provided when its credentials resolved and no check could say more, either
// because the account declares none or because the program its check runs
// is not on this machine. Unverified is left for a check that ran and did not
// confirm the access.
func serviceEntry(service auth.Service, status auth.Status) Entry {
	verdict, detail := string(status.State), ""
	switch {
	case status.State == auth.StateGreen && len(service.Probe) > 0:
		verdict = "connected"
	case status.State == auth.StateGreen && service.Identity != nil:
		verdict, detail = "provided", "Credentials resolved and name this project. This account declares no connection check."
	case status.State == auth.StateGreen:
		verdict, detail = "provided", "Credentials resolved. This account declares no connection check."
	case status.State == auth.StateUnverified && status.Unchecked != "":
		verdict, detail = "provided", "Credentials resolved. Not checked on this machine: "+status.Unchecked+"."
	}
	if detail == "" {
		detail = statusDetail(verdict)
	}
	entry := Entry{ID: "service:" + service.Name, Name: service.Name, Kind: "service", Status: verdict, Source: "Repository", Detail: detail}
	entry.Scope = "configuration"
	if len(service.Probe) > 0 {
		entry.Scope = "separate_probe"
	}
	if verdict != "connected" && verdict != "provided" {
		if safeLoginURL(service.URL) {
			entry.Actions = append(entry.Actions, "login")
		}
		if len(service.Login) > 0 && len(status.Missing) == 0 {
			entry.Actions = append(entry.Actions, "cli")
		}
		for _, name := range status.Missing {
			if auth.ValidEnvName(name) && !auth.IsHarnessBillingKey(name) {
				entry.Actions = append(entry.Actions, "store:"+name)
			}
		}
	}
	return entry
}

func statusDetail(status string) string {
	return map[string]string{
		"missing": "Credential missing.", "unauthorized": "Sign-in required.", "expired": "Sign-in expired.",
		"wrong_target": "Credentials point to another project.", "unreachable": "Service did not respond.",
		"failed": "Connection check failed.", "unverified": "No successful connection check.",
		"withheld": "Not currently granted to this goblin.", "disabled": "Disabled in the probe configuration. Live session load state unknown.",
		"checking": "Waiting for the harness.", "skipped": "Optional connection is not available.", "unchecked": "Not checked.",
	}[status]
}

func codexEntry(status codexStatus) Entry {
	verdict := map[string]string{"connected": "connected", "starting": "checking", "authenticationRequired": "unauthorized", "failed": "failed", "cancelled": "failed", "disabled": "disabled"}[status.RuntimeStatus]
	if verdict == "" {
		verdict = "unverified"
	}
	entry := Entry{ID: "mcp:" + status.Name, Name: status.Name, Kind: "mcp", Status: verdict, Source: "Codex", Detail: statusDetail(verdict)}
	reported := make([]Tool, 0, len(status.Tools))
	for name, tool := range status.Tools {
		reported = append(reported, Tool{Name: name, ReadOnly: tool.Annotations.ReadOnly})
	}
	entry.Tools = listedTools(reported)
	if status.AuthStatus == "notLoggedIn" && verdict != "connected" && verdict != "disabled" {
		entry.Actions = []string{"login"}
	}
	return entry
}
