package memory

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Revalidate the snapshot rather than letting a retained Catalog become a cache
// of authority. Concurrent writes fail closed; callers retry after curation ends.
func (catalog Catalog) checkFresh() error {
	data, err := ReadData(catalog.dataDir, CatalogPath)
	if err != nil {
		return err
	}
	if Hash(data) != catalog.checksum {
		return errors.New("memory catalog changed; reload current sources")
	}
	names, err := GovernedFiles(catalog.dataDir)
	if err != nil {
		return err
	}
	if len(names) != len(catalog.Sources) {
		return errors.New("memory source inventory changed; reload current sources")
	}
	for _, source := range catalog.Sources {
		content, err := ReadData(catalog.dataDir, source.Path)
		if err != nil {
			return err
		}
		if !slices.Contains(names, source.Path) || Hash(content) != source.SHA256 {
			return fmt.Errorf("memory source %s changed; reload current sources", source.Path)
		}
	}
	return nil
}

func (catalog Catalog) deliveryKey(id string) string {
	return Hash([]byte(catalog.checksum + "\n" + id))
}

func searchWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(character rune) bool { return !unicode.IsLetter(character) && !unicode.IsNumber(character) })
}

func (catalog Catalog) selectEntries(query Query) (map[string]bool, error) {
	if query.ID != "" && (query.Search != "" || query.Project != "" || query.Task != "" || query.Topic != "") {
		return nil, errors.New("exact id cannot be combined with scope or search")
	}
	for _, fingerprint := range query.Delivered {
		if decoded, err := hex.DecodeString(fingerprint); err != nil || len(decoded) != 32 {
			return nil, errors.New("delivered fingerprints must be SHA256 hex strings")
		}
	}
	words := searchWords(query.Search)
	if query.Search != "" && (len(words) == 0 || query.Limit < 0 || query.Limit > 50) {
		return nil, errors.New("search needs words and a limit between 1 and 50")
	}
	limit := query.Limit
	if limit == 0 {
		limit = 5
	}
	selected := map[string]bool{}
	type candidate struct {
		id    string
		score int
	}
	var candidates []candidate
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if query.ID != "" {
				selected[entry.ID] = entry.ID == query.ID
				continue
			}
			if entry.Location == "warm" {
				selected[entry.ID] = !query.IsColdOnly
				continue
			}
			if query.Search == "" {
				selected[entry.ID] = query.Project != "" && slices.Contains(entry.Projects, query.Project) || query.Task != "" && slices.Contains(entry.Tasks, query.Task) || query.Topic != "" && slices.Contains(entry.Topics, query.Topic)
				continue
			}
			if query.Project != "" && !slices.Contains(entry.Projects, query.Project) || query.Task != "" && !slices.Contains(entry.Tasks, query.Task) || query.Topic != "" && !slices.Contains(entry.Topics, query.Topic) {
				continue
			}
			if entry.TaskState == "completed" && !query.IncludeCompleted && query.Task == "" {
				continue
			}
			text := entry.ID + " " + entry.Trigger + " " + strings.Join(entry.Topics, " ") + " " + string(catalog.content[source.Path][entry.Start:entry.End])
			counts := map[string]int{}
			for _, word := range searchWords(text) {
				counts[word]++
			}
			score := 0
			for _, word := range words {
				if counts[word] == 0 {
					score = 0
					break
				}
				score += counts[word]
			}
			if score > 0 {
				candidates = append(candidates, candidate{entry.ID, score})
			}
		}
	}
	slices.SortFunc(candidates, func(left, right candidate) int {
		if left.score != right.score {
			return right.score - left.score
		}
		return strings.Compare(left.id, right.id)
	})
	for _, candidate := range candidates[:min(limit, len(candidates))] {
		selected[candidate.id] = true
	}
	return selected, nil
}
