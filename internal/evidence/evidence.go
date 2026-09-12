package evidence

import (
	"encoding/json"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"os"
	"path/filepath"
	"time"
)

type Deployment struct {
	Required bool     `json:"required"`
	State    string   `json:"state"`
	Targets  []Target `json:"targets,omitempty"`
}
type Target struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Deployed bool   `json:"deployed"`
	Healthy  bool   `json:"healthy"`
	Error    string `json:"error,omitempty"`
}
type Security struct {
	Mode   string `json:"mode"`
	Passed bool   `json:"passed"`
}
type Hygiene struct {
	SupersededCodeRemaining bool `json:"superseded_code_remaining"`
}
type CI struct {
	Passed  bool   `json:"passed"`
	HeadSHA string `json:"head_sha,omitempty"`
}
type Artifact struct {
	Task        string    `json:"task"`
	Commit      string    `json:"commit,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
	Acceptance  struct {
		AllMet bool `json:"all_met"`
	} `json:"acceptance"`
	Tests      []verify.Result `json:"tests,omitempty"`
	Security   Security        `json:"security"`
	Hygiene    Hygiene         `json:"hygiene"`
	CI         CI              `json:"ci"`
	Deployment Deployment      `json:"deployment"`
	HeadSHA    string          `json:"head_sha,omitempty"`
}

func Save(path string, a Artifact) error {
	a.GeneratedAt = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, e := json.MarshalIndent(a, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
