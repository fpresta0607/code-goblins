package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Outcome struct {
	TaskClass        string  `json:"task_class"`
	Harness          string  `json:"harness"`
	Model            string  `json:"model,omitempty"`
	Effort           string  `json:"effort,omitempty"`
	InputTokens      *int    `json:"input_tokens,omitempty"`
	OutputTokens     *int    `json:"output_tokens,omitempty"`
	TokenMeasurement string  `json:"token_measurement,omitempty"`
	DurationSeconds  float64 `json:"duration_seconds"`
	FirstPassTests   bool    `json:"first_pass_tests"`
	ReviewFindings   int     `json:"review_findings"`
	RepairRounds     int     `json:"repair_rounds"`
	Accepted         bool    `json:"accepted"`
	DeploymentPassed bool    `json:"deployment_passed"`
}

func AppendOutcome(path string, o Outcome) error {
	if o.TokenMeasurement == "" {
		o.TokenMeasurement = "unavailable"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, e := json.Marshal(o)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write(append(b, '\n'))
	return e
}
func ReadOutcomes(path string) ([]Outcome, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var out []Outcome
	s := bufio.NewScanner(f)
	for s.Scan() {
		var o Outcome
		if e := json.Unmarshal(s.Bytes(), &o); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, s.Err()
}
func Score(xs []Outcome) float64 {
	if len(xs) == 0 {
		return 0
	}
	var success, repairs, latency float64
	for _, x := range xs {
		if x.Accepted {
			success++
		}
		repairs += float64(x.RepairRounds)
		latency += x.DurationSeconds
	}
	n := float64(len(xs))
	return success/n - 0.08*(repairs/n) - 0.0001*(latency/n)
}
func BestByClass(xs []Outcome, class string) (string, error) {
	groups := map[string][]Outcome{}
	for _, x := range xs {
		if x.TaskClass == class {
			groups[x.Harness+"|"+x.Model] = append(groups[x.Harness+"|"+x.Model], x)
		}
	}
	best := ""
	score := -1e9
	for k, g := range groups {
		if s := Score(g); s > score {
			best, score = k, s
		}
	}
	if best == "" {
		return "", fmt.Errorf("no telemetry for task class %q", class)
	}
	return best, nil
}
