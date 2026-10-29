package supervisor

import (
	"bytes"
	"encoding/json"
	"slices"
)

// boardChange is what turns a JSON value a board holds into the one it is
// sent next: the new value whole, or for an object the keys whose values
// changed and the keys it lost, or for a list whose items each carry an id of
// their own the items that changed by id, and every id in order when items
// came, went or moved, or only the ids that went and those that came at its
// end when that names fewer. Between two snapshots a few live goblins' clocks
// and the meters move, and each snapshot was the whole fleet, a megabyte
// (#1611).
type boardChange struct {
	Value    json.RawMessage        `json:"value,omitempty"`
	Keys     map[string]boardChange `json:"keys,omitempty"`
	Gone     []string               `json:"gone,omitempty"`
	Order    []string               `json:"order,omitempty"`
	Removed  []string               `json:"removed,omitempty"`
	Appended []string               `json:"appended,omitempty"`
	Items    map[string]boardChange `json:"items,omitempty"`
}

// changeOf is the change from held to next, and false when they are the
// same. Both are JSON as encoding/json writes it, so the same value is the
// same bytes.
func changeOf(held, next json.RawMessage) (boardChange, bool) {
	if bytes.Equal(held, next) {
		return boardChange{}, false
	}
	if held[0] == '{' && next[0] == '{' {
		return changeOfKeys(keysOf(held), keysOf(next))
	}
	if held[0] == '[' && next[0] == '[' {
		heldItems, nextItems := itemsOf(held), itemsOf(next)
		if heldIDs, nextIDs := identified(heldItems), identified(nextItems); len(heldIDs) > 0 && len(nextIDs) > 0 {
			return changeOfItems(heldItems, heldIDs, nextItems, nextIDs), true
		}
	}
	return boardChange{Value: next}, true
}

// keysOf is each key of an object as encoding/json writes it, with its value.
// The JSON is split where encoding/json put its marks rather than decoded:
// decoding the megabyte a board holds and the one it is sent next took the
// stream several times as long as writing the snapshot.
func keysOf(object json.RawMessage) map[string]json.RawMessage {
	keys := map[string]json.RawMessage{}
	for i := 1; i < len(object)-1; i++ {
		keyEnd := valueEnd(object, i)
		end := valueEnd(object, keyEnd+1)
		keys[unquoted(object[i:keyEnd])] = object[keyEnd+1 : end]
		i = end
	}
	return keys
}

// itemsOf is each item of a list as encoding/json writes it.
func itemsOf(list json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	for i := 1; i < len(list)-1; i++ {
		end := valueEnd(list, i)
		items = append(items, list[i:end])
		i = end
	}
	return items
}

// valueEnd is the index just past the JSON value that starts at data[start],
// which encoding/json wrote, so it is valid and has no space outside its
// strings.
func valueEnd(data []byte, start int) int {
	switch data[start] {
	case '"':
		i := start + 1
		for data[i] != '"' {
			if data[i] == '\\' {
				i++
			}
			i++
		}
		return i + 1
	case '{', '[':
		depth := 0
		for i := start; ; i++ {
			switch data[i] {
			case '"':
				i = valueEnd(data, i) - 1
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
	default:
		i := start
		for i < len(data) && data[i] != ',' && data[i] != '}' && data[i] != ']' {
			i++
		}
		return i
	}
}

// unquoted is the text of a JSON string encoding/json wrote.
func unquoted(literal json.RawMessage) string {
	if bytes.IndexByte(literal, '\\') < 0 {
		return string(literal[1 : len(literal)-1])
	}
	var text string
	_ = json.Unmarshal(literal, &text)
	return text
}

// changeOfKeys is the change from one object to another, each as its keys'
// values, and false when no key changed.
func changeOfKeys(held, next map[string]json.RawMessage) (boardChange, bool) {
	var out boardChange
	keys := map[string]boardChange{}
	for key, value := range next {
		if before, isHeld := held[key]; !isHeld {
			keys[key] = boardChange{Value: value}
		} else if changed, isChanged := changeOf(before, value); isChanged {
			keys[key] = changed
		}
	}
	if len(keys) > 0 {
		out.Keys = keys
	}
	for key := range held {
		if _, isKept := next[key]; !isKept {
			out.Gone = append(out.Gone, key)
		}
	}
	slices.Sort(out.Gone)
	return out, out.Keys != nil || out.Gone != nil
}

// changeOfItems is the change from one list of items with ids to another.
func changeOfItems(held []json.RawMessage, heldIDs []string, next []json.RawMessage, nextIDs []string) boardChange {
	var out boardChange
	byID := make(map[string]json.RawMessage, len(held))
	for i, id := range heldIDs {
		byID[id] = held[i]
	}
	items := map[string]boardChange{}
	for i, id := range nextIDs {
		if before, isHeld := byID[id]; !isHeld {
			items[id] = boardChange{Value: next[i]}
		} else if changed, isChanged := changeOf(before, next[i]); isChanged {
			items[id] = changed
		}
	}
	if len(items) > 0 {
		out.Items = items
	}
	if !slices.Equal(heldIDs, nextIDs) {
		out.Order = nextIDs
		// A list that keeps its newest, as the actions do, drops its oldest
		// and adds at its end: those few ids say it, where the order names
		// every id (#1611).
		isListed := make(map[string]bool, len(nextIDs))
		for _, id := range nextIDs {
			isListed[id] = true
		}
		var removed []string
		kept := 0
		isKeptInOrder := true
		for _, id := range heldIDs {
			switch {
			case !isListed[id]:
				removed = append(removed, id)
			case nextIDs[kept] != id:
				isKeptInOrder = false
			default:
				kept++
			}
		}
		if appended := nextIDs[kept:]; isKeptInOrder && len(removed)+len(appended) < len(nextIDs) {
			out.Order, out.Removed, out.Appended = nil, removed, appended
		}
	}
	return out
}

// identified is the id of each item, in order, or nil unless every item is
// an object with an id no other item has.
func identified(items []json.RawMessage) []string {
	ids := make([]string, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		id := idOf(item)
		if id == "" || seen[id] {
			return nil
		}
		ids[i], seen[id] = id, true
	}
	return ids
}

// idOf is the "id" of an object, read only up to that key, which is most
// often its first, or "" when it has no id that is a string.
func idOf(item json.RawMessage) string {
	if item[0] != '{' {
		return ""
	}
	for i := 1; i < len(item)-1; i++ {
		keyEnd := valueEnd(item, i)
		end := valueEnd(item, keyEnd+1)
		if string(item[i:keyEnd]) == `"id"` {
			if item[keyEnd+1] != '"' {
				return ""
			}
			return unquoted(item[keyEnd+1 : end])
		}
		i = end
	}
	return ""
}
