package connections

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

type Entry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	Source    string    `json:"source,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Actions   []string  `json:"actions,omitempty"`
}
type Snapshot struct {
	Entries   []Entry   `json:"entries"`
	Checking  bool      `json:"checking"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}
type codexStatus struct {
	Name          string `json:"name"`
	RuntimeStatus string `json:"runtimeStatus"`
	AuthStatus    string `json:"authStatus"`
}

func serviceEntry(service auth.Service, status auth.Status) Entry {
	verdict := string(status.State)
	if status.State == auth.StateGreen {
		verdict = "connected"
		if len(service.Probe) == 0 {
			verdict = "unverified"
		}
	}
	entry := Entry{ID: "service:" + service.Name, Name: service.Name, Kind: "service", Status: verdict, Source: "Repository", Detail: statusDetail(verdict)}
	if verdict != "connected" {
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
		"withheld": "Not available to this goblin.", "disabled": "Disabled at launch. Ask the CFO to enable it.",
		"checking": "Waiting for the harness.", "skipped": "Optional connection is not available.",
	}[status]
}

func codexEntry(status codexStatus) Entry {
	verdict := map[string]string{"connected": "connected", "starting": "checking", "authenticationRequired": "unauthorized", "failed": "failed", "cancelled": "failed", "disabled": "disabled"}[status.RuntimeStatus]
	if verdict == "" {
		verdict = "unverified"
	}
	entry := Entry{ID: "mcp:" + status.Name, Name: status.Name, Kind: "mcp", Status: verdict, Source: "Codex", Detail: statusDetail(verdict)}
	if status.AuthStatus == "notLoggedIn" && verdict != "connected" && verdict != "disabled" {
		entry.Actions = []string{"login"}
	}
	return entry
}
