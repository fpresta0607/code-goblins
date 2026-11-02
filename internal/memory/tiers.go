package memory

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

var tierMarker = regexp.MustCompile(`^(?:<!--(a|p):([0-9]{4}-[0-9]{2}-[0-9]{2})-->|<!--(P|g)-->|<!--t:([^<>\s]+)-->)$`)

func trailingAnnotations(text string) []string {
	var annotations []string
	text = strings.TrimSpace(text)
	for strings.HasSuffix(text, "-->") {
		start := strings.LastIndex(text, "<!--")
		if start < 0 {
			break
		}
		annotation := text[start:]
		if !slices.ContainsFunc([]string{"<!--a:", "<!--p:", "<!--P", "<!--g", "<!--t:"}, func(prefix string) bool { return strings.HasPrefix(annotation, prefix) }) {
			break
		}
		annotations = append(annotations, annotation)
		text = strings.TrimSpace(text[:start])
	}
	return annotations
}

func pinnedReferences(dataDir string) (map[string]bool, error) {
	content, err := ReadData(dataDir, "memory/MEMORY.md")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pinned := make(map[string]bool)
	names, err := GovernedFiles(dataDir)
	if err != nil {
		return nil, err
	}
	isLiteral := make([]bool, len(content))
	markLiteral := func(segment text.Segment) {
		for position := segment.Start; position < segment.Stop; position++ {
			isLiteral[position] = true
		}
	}
	document := goldmark.New().Parser().Parse(text.NewReader(content))
	ast.Walk(document, func(node ast.Node, isEntering bool) (ast.WalkStatus, error) {
		if !isEntering {
			return ast.WalkContinue, nil
		}
		if node.Kind() != ast.KindCodeBlock && node.Kind() != ast.KindFencedCodeBlock {
			return ast.WalkContinue, nil
		}
		for index := 0; index < node.Lines().Len(); index++ {
			markLiteral(node.Lines().At(index))
		}
		if fence, isFence := node.(*ast.FencedCodeBlock); isFence && fence.Info != nil {
			markLiteral(fence.Info.Segment)
		}
		return ast.WalkSkipChildren, nil
	})
	offset := 0
	for lineNumber, line := range strings.Split(string(content), "\n") {
		lineStart := offset
		offset += len(line) + 1
		if !slices.Contains(trailingAnnotations(line), "<!--P-->") || isLiteral[lineStart+strings.LastIndex(line, "<!--P-->")] {
			continue
		}
		destinations, err := indexDestinations(line)
		if err != nil {
			return nil, fmt.Errorf("memory/MEMORY.md line %d: %w", lineNumber+1, err)
		}
		for _, destination := range destinations {
			reference, err := url.Parse(destination)
			if err != nil {
				return nil, fmt.Errorf("pinned index destination %q: %w", destination, err)
			}
			if strings.EqualFold(reference.Scheme, "file") || len(reference.Scheme) == 1 {
				return nil, fmt.Errorf("pinned index destination %q must use a local path", destination)
			}
			if reference.IsAbs() || reference.Host != "" {
				continue
			}
			target := filepath.FromSlash(reference.Path)
			if target == "" || filepath.IsAbs(target) {
				return nil, fmt.Errorf("pinned index destination %q needs a nonempty memory-relative path", destination)
			}
			target = filepath.Join(dataDir, "memory", target)
			isResolved := false
			for _, name := range names {
				if fsx.SamePath(target, filepath.Join(dataDir, filepath.FromSlash(name))) {
					pinned[name] = true
					isResolved = true
				}
			}
			if !isResolved {
				return nil, fmt.Errorf("pinned index destination %q does not resolve to a governed memory source", destination)
			}
		}
	}
	return pinned, nil
}

func indexDestinations(line string) ([]string, error) {
	invalidLink := errors.New("pinned index entry needs a complete inline Markdown link")
	var destinations []string
	labelDepth := 0
	for position := 0; position < len(line); position++ {
		if line[position] == '\\' {
			position++
			continue
		}
		if line[position] == '[' {
			labelDepth++
			continue
		}
		if line[position] != ']' || labelDepth == 0 {
			continue
		}
		labelDepth--
		if labelDepth != 0 || position+1 == len(line) || line[position+1] != '(' {
			continue
		}
		position += 2
		for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
			position++
		}
		if position == len(line) {
			return nil, invalidLink
		}
		isAngle := line[position] == '<'
		if isAngle {
			position++
		}
		var destination strings.Builder
		depth := 0
		for ; position < len(line); position++ {
			character := line[position]
			if character == '\\' && position+1 < len(line) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(line[position+1])) {
				position++
				destination.WriteByte(line[position])
				continue
			}
			if isAngle && character == '>' {
				position++
				break
			}
			if !isAngle && depth == 0 && (character == ')' || character == ' ' || character == '\t') {
				break
			}
			if character == '<' || character == '\r' || character == '\n' || !isAngle && (character == '>' || character == ' ' || character == '\t') {
				return nil, invalidLink
			}
			if !isAngle {
				switch character {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			destination.WriteByte(character)
		}
		end := position
		for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
			position++
		}
		if position > end && position < len(line) && strings.ContainsRune("\"'(", rune(line[position])) {
			delimiter := line[position]
			if delimiter == '(' {
				delimiter = ')'
			}
			position++
			for ; position < len(line); position++ {
				if line[position] == '\\' {
					position++
				} else if line[position] == delimiter {
					break
				}
			}
			if position == len(line) {
				return nil, invalidLink
			}
			position++
			for position < len(line) && (line[position] == ' ' || line[position] == '\t') {
				position++
			}
		}
		if position >= len(line) || line[position] != ')' || depth != 0 {
			return nil, invalidLink
		}
		destinations = append(destinations, destination.String())
	}
	if len(destinations) == 0 {
		return nil, invalidLink
	}
	return destinations, nil
}

type Status struct {
	Tier           string `json:"tier"`
	Reinforced     string `json:"reinforced,omitempty"`
	IsStale        bool   `json:"is_stale"`
	IsGrace        bool   `json:"is_grace"`
	IsLegacy       bool   `json:"is_legacy"`
	PendingCapture string `json:"pending_capture,omitempty"`
}

// Inspect follows Stow's default date horizons. Reading never advances decay.
func Inspect(content []byte, authority string, now time.Time) (Status, error) {
	status := Status{Tier: "aging"}
	if authority == "ruling" || authority == "history" {
		status.Tier = "pinned"
	}
	hasTier := false
	for _, annotation := range trailingAnnotations(string(content)) {
		match := tierMarker.FindStringSubmatch(annotation)
		if match == nil {
			return Status{}, fmt.Errorf("malformed memory marker")
		}
		if match[4] != "" {
			if status.PendingCapture != "" {
				return Status{}, fmt.Errorf("conflicting pending-capture markers")
			}
			status.PendingCapture = match[4]
			continue
		}
		if hasTier {
			return Status{}, fmt.Errorf("conflicting tier markers; catalog each entry separately")
		}
		hasTier = true
		switch match[3] {
		case "P":
			status.Tier = "pinned"
		case "g":
			status.Tier = "aging"
			status.IsGrace = true
		default:
			date, err := time.Parse(time.DateOnly, match[2])
			if err != nil || date.After(now) {
				return Status{}, fmt.Errorf("invalid reinforcement date %q", match[2])
			}
			status.Reinforced = match[2]
			status.Tier = "aging"
			horizon := 30 * 24 * time.Hour
			if match[1] == "p" {
				status.Tier = "perishable"
				horizon = 7 * 24 * time.Hour
			}
			status.IsStale = !now.Before(date.Add(horizon))
		}
	}
	if !hasTier && status.Tier == "aging" {
		status.IsLegacy = true
	}
	return status, nil
}
