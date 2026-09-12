package supersede

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Event struct {
	Task            string    `json:"task"`
	Reason          string    `json:"reason"`
	At              time.Time `json:"at"`
	CleanupRequired bool      `json:"cleanup_required"`
}

func Record(taskTmp, task, reason string) (string, string, error) {
	if reason == "" {
		return "", "", fmt.Errorf("supersede: reason is required")
	}
	e := Event{Task: task, Reason: reason, At: time.Now().UTC(), CleanupRequired: true}
	p := filepath.Join(taskTmp, "supersede.json")
	b, _ := json.MarshalIndent(e, "", "  ")
	if err := os.WriteFile(p, append(b, '\n'), 0600); err != nil {
		return "", "", err
	}
	instruction := fmt.Sprintf("Direction change: %s. The prior implementation direction is rejected. Identify and delete artifacts introduced exclusively for it: stale files, unused exports/routes, feature flags, obsolete tests, and dead branches. Preserve only independently required behavior. Do not carry the rejected approach into handoff except as a rejected approach. Produce cleanup evidence before continuing.", reason)
	ip := filepath.Join(taskTmp, "supersede-cleanup.md")
	if err := os.WriteFile(ip, []byte(instruction+"\n"), 0600); err != nil {
		return "", "", err
	}
	return p, instruction, nil
}
