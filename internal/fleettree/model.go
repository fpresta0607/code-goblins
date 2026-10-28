package fleettree

import "encoding/json"

func recordedModel(entries [][]byte, harness string) string {
	for index := len(entries) - 1; index >= 0; index-- {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
			} `json:"message"`
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(entries[index], &entry) != nil {
			continue
		}
		if harness == "claude" && entry.Type == "assistant" && entry.Message.Model != "" && entry.Message.Model != syntheticModel {
			return entry.Message.Model
		}
		if harness == "codex" && entry.Type == "turn_context" && entry.Payload.Model != "" {
			return entry.Payload.Model
		}
	}
	return ""
}
