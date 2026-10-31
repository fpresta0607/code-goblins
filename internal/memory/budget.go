package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

func Tokens(size int) int { return (size + 3) / 4 }

func ReadBudget(path string, defaultTokens int) (int, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultTokens, nil
	}
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must contain a positive estimated-token allowance", path)
	}
	return value, nil
}

type Report struct {
	IsLegacy        bool          `json:"is_legacy"`
	Bytes           int           `json:"bytes"`
	EstimatedTokens int           `json:"estimated_tokens"`
	Budget          int           `json:"budget"`
	IsOverBudget    bool          `json:"is_over_budget"`
	Inputs          []Measurement `json:"inputs"`
	Exceptions      []string      `json:"exceptions"`
	Estimate        string        `json:"estimate"`
}

func MemoryReport(dataDir string, now time.Time) (Report, error) {
	budget, err := ReadBudget(filepath.Join(filepath.Dir(dataDir), "config", "startup-memory-budget"), DefaultBudget)
	if err != nil {
		return Report{}, err
	}
	report := Report{Budget: budget, Inputs: []Measurement{}, Exceptions: []string{}, Estimate: "ceil(bytes/4); local text estimate, not provider billing or RAM"}
	_, err = os.Lstat(filepath.Join(dataDir, filepath.FromSlash(CatalogPath)))
	if errors.Is(err, os.ErrNotExist) {
		report.IsLegacy = true
		report.Exceptions = append(report.Exceptions, "uncurated legacy sources; no authority has been offloaded or retired")
		for _, name := range []string{"projects.md", "overlord.md", "memory/MEMORY.md"} {
			content, err := ReadData(dataDir, name)
			if errors.Is(err, os.ErrNotExist) {
				report.Exceptions = append(report.Exceptions, "ABSENT: "+name)
				continue
			}
			if err != nil {
				return Report{}, err
			}
			report.Inputs = append(report.Inputs, Measurement{Kind: "memory", Path: name, SHA256: Hash(content), Bytes: len(content), EstimatedTokens: Tokens(len(content))})
			report.Bytes += len(content)
		}
	} else {
		catalog, err := Load(dataDir, now)
		if err != nil {
			return Report{}, err
		}
		text, err := catalog.Retrieve(Query{}, now)
		if err != nil {
			return Report{}, err
		}
		text += catalog.Routes(2000)
		report.Bytes = len(text)
		report.Inputs = append(report.Inputs, Measurement{Kind: "memory", Path: "verified warm ranges and cold routing index", SHA256: Hash([]byte(text)), Bytes: len(text), EstimatedTokens: Tokens(len(text))})
	}
	report.EstimatedTokens = Tokens(report.Bytes)
	report.IsOverBudget = report.EstimatedTokens > budget
	if report.IsOverBudget {
		report.Exceptions = append(report.Exceptions, "over budget: retain authority and open a concrete budget or per-item offload decision; not reset-safe")
	}
	return report, nil
}

type Input struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`
}

// Package lists actual emitted inputs, including duplicates when delivered twice.
type Package struct {
	Harness string  `json:"harness"`
	Inputs  []Input `json:"inputs"`
}

type Measurement struct {
	Kind            string `json:"kind"`
	Path            string `json:"path"`
	SHA256          string `json:"sha256"`
	Bytes           int    `json:"bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Reason          string `json:"reason,omitempty"`
}

type PackageReport struct {
	Harness         string        `json:"harness"`
	Inputs          []Measurement `json:"inputs"`
	Bytes           int           `json:"bytes"`
	EstimatedTokens int           `json:"estimated_tokens"`
	Budget          int           `json:"budget"`
	IsOverBudget    bool          `json:"is_over_budget"`
	Estimate        string        `json:"estimate"`
}

// MeasurePackages requires each category for all three harnesses. A hook preview
// alone cannot masquerade as complete startup accounting.
func MeasurePackages(packages []Package, baseDir string, budget int) ([]PackageReport, error) {
	if budget <= 0 {
		return nil, errors.New("startup package budget must be positive")
	}
	seen := make(map[string]bool)
	var reports []PackageReport
	for _, startup := range packages {
		if !slices.Contains([]string{"codex", "claude", "pi"}, startup.Harness) || seen[startup.Harness] {
			return nil, fmt.Errorf("unsupported or duplicate startup harness %q", startup.Harness)
		}
		seen[startup.Harness] = true
		report := PackageReport{Harness: startup.Harness, Budget: budget, Estimate: "ceil(bytes/4); local text estimate, not provider billing or RAM; only supplied emitted inputs measured"}
		kinds := make(map[string]bool)
		for _, input := range startup.Inputs {
			if !slices.Contains([]string{"core", "global", "memory", "skills-index", "brief", "digest"}, input.Kind) || input.Path == "" {
				return nil, fmt.Errorf("invalid startup input %q", input.Kind)
			}
			path := input.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("%s %s input: %w", startup.Harness, input.Kind, err)
			}
			if len(content) == 0 && strings.TrimSpace(input.Reason) == "" {
				return nil, fmt.Errorf("%s %s input is empty without an explicit absence or containment reason", startup.Harness, input.Kind)
			}
			kinds[input.Kind] = true
			report.Inputs = append(report.Inputs, Measurement{Kind: input.Kind, Path: input.Path, SHA256: Hash(content), Bytes: len(content), EstimatedTokens: Tokens(len(content)), Reason: input.Reason})
			report.Bytes += len(content)
		}
		for _, kind := range []string{"core", "global", "memory", "skills-index", "brief", "digest"} {
			if !kinds[kind] {
				return nil, fmt.Errorf("%s startup is incomplete: missing %s", startup.Harness, kind)
			}
		}
		report.EstimatedTokens = Tokens(report.Bytes)
		report.IsOverBudget = report.EstimatedTokens > budget
		reports = append(reports, report)
	}
	if len(seen) != 3 {
		return nil, errors.New("startup accounting needs codex, claude and pi packages")
	}
	return reports, nil
}
