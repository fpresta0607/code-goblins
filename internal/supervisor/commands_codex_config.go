package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// codexConfig is what the user's Codex config.toml says about the skills
// Codex offers: whether its bundled skills are on, which plugins it turns on
// or off by name@marketplace, and its [[skills.config]] rules in order.
type codexConfig struct {
	isBundledEnabled bool
	plugins          map[string]bool
	skillRules       []codexSkillRule
}

// codexSkillRule turns the skills of one name, or the one at one SKILL.md
// path, on or off.
type codexSkillRule struct {
	name, path string
	isEnabled  bool
}

// isSkillEnabled applies the rules in order, a later one winning, as Codex
// applies them.
func (config codexConfig) isSkillEnabled(skill codexSkill) bool {
	isEnabled := true
	canonical, err := filepath.EvalSymlinks(skill.path)
	if err != nil {
		canonical = skill.path
	}
	for _, rule := range config.skillRules {
		if rule.name == skill.name || (rule.path != "" && strings.EqualFold(rule.path, canonical)) {
			isEnabled = rule.isEnabled
		}
	}
	return isEnabled
}

// tomlValue is one value config.toml holds: a string's text or a bare word,
// an array's items, or the keys an inline table sets. A multi-line string is
// none of them.
type tomlValue struct {
	text     string
	isString bool
	items    []tomlValue
	keys     []tomlPair
}

// tomlPair is one key an inline table sets, as its dotted path below the
// table, and its value.
type tomlPair struct {
	path  []string
	value tomlValue
}

// tomlWord matches a bare word, such as true, 3 or a date, and reads a date
// and a time joined by one space as one word.
var tomlWord = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d)?[^\s,\]}#]*`)

// readCodexConfig reads the settings of codexConfig from config.toml in home.
// It reads the whole file as TOML: table and array table headers and keys,
// dotted or quoted, each ending its line but for a comment, set to a one-line
// or multi-line string, a bare word, an array or an inline table, the last two
// of which may span lines and hold comments. A file it cannot read is an
// error, as Codex refuses to start with one.
func readCodexConfig(home string) (codexConfig, error) {
	config := codexConfig{isBundledEnabled: true, plugins: map[string]bool{}}
	data, err := readConfigFile(filepath.Join(home, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	unreadable := errors.New("unreadable Codex config.toml")
	var table []string
	rule := -1
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for text = tomlGap(text); text != ""; text = tomlGap(text) {
		var ok bool
		if text[0] == '[' {
			closing := "]"
			if strings.HasPrefix(text, "[[") {
				closing = "]]"
			}
			if table, text, ok = tomlKeyPath(text[len(closing):]); !ok || !strings.HasPrefix(text, closing) {
				return config, unreadable
			}
			text, rule = text[len(closing):], -1
			if closing == "]]" && slices.Equal(table, []string{"skills", "config"}) {
				config.skillRules = append(config.skillRules, codexSkillRule{isEnabled: true})
				rule = len(config.skillRules) - 1
			}
			config.set(home, table, tomlValue{}, rule)
		} else {
			var key []string
			if key, text, ok = tomlKeyPath(text); !ok || !strings.HasPrefix(text, "=") {
				return config, unreadable
			}
			var value tomlValue
			if value, text, ok = readTOMLValue(tomlSpace(text[1:])); !ok {
				return config, unreadable
			}
			config.set(home, append(slices.Clone(table), key...), value, rule)
		}
		if text = tomlSpace(text); text != "" && text[0] != '#' && text[0] != '\n' {
			return config, unreadable
		}
	}
	return config, nil
}

// set records the value the configuration sets at path, and each key below it
// an inline table sets, inside the [[skills.config]] entry rule when there is
// one. An array at skills.config makes each of its inline tables a
// [[skills.config]] entry, in order.
func (config *codexConfig) set(home string, path []string, value tomlValue, rule int) {
	isTrue := !value.isString && value.text == "true"
	isBool := !value.isString && (value.text == "true" || value.text == "false")
	switch {
	case rule >= 0 && len(path) == 3:
		switch {
		case path[2] == "name" && value.isString:
			config.skillRules[rule].name = strings.TrimSpace(value.text)
		case path[2] == "path" && value.isString:
			skillPath := value.text
			if !filepath.IsAbs(skillPath) {
				skillPath = filepath.Join(home, skillPath)
			}
			if canonical, err := filepath.EvalSymlinks(skillPath); err == nil {
				skillPath = canonical
			}
			config.skillRules[rule].path = filepath.Clean(skillPath)
		case path[2] == "enabled" && isBool:
			config.skillRules[rule].isEnabled = isTrue
		}
	case slices.Equal(path, []string{"skills", "bundled", "enabled"}) && isBool:
		config.isBundledEnabled = isTrue
	case len(path) >= 2 && path[0] == "plugins":
		if _, isSet := config.plugins[path[1]]; !isSet {
			config.plugins[path[1]] = true
		}
		if len(path) == 3 && path[2] == "enabled" && isBool {
			config.plugins[path[1]] = isTrue
		}
	}
	for _, pair := range value.keys {
		config.set(home, append(slices.Clone(path), pair.path...), pair.value, rule)
	}
	if slices.Equal(path, []string{"skills", "config"}) {
		for _, item := range value.items {
			config.skillRules = append(config.skillRules, codexSkillRule{isEnabled: true})
			config.set(home, path, item, len(config.skillRules)-1)
		}
	}
}

// readTOMLValue reads the value that opens text, and the text after it.
func readTOMLValue(text string) (tomlValue, string, bool) {
	switch {
	case strings.HasPrefix(text, `"""`) || strings.HasPrefix(text, "'''"):
		rest, ok := tomlMultiLineString(text)
		return tomlValue{}, rest, ok
	case strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'"):
		value, rest, ok := tomlString(text)
		return tomlValue{text: value, isString: true}, rest, ok
	case strings.HasPrefix(text, "["):
		var array tomlValue
		rest, ok := tomlItems(text[1:], "]", func(item string) (string, bool) {
			value, rest, ok := readTOMLValue(item)
			array.items = append(array.items, value)
			return rest, ok
		})
		return array, rest, ok
	case strings.HasPrefix(text, "{"):
		var table tomlValue
		rest, ok := tomlItems(text[1:], "}", func(item string) (string, bool) {
			key, rest, ok := tomlKeyPath(item)
			if !ok || !strings.HasPrefix(rest, "=") {
				return "", false
			}
			value, rest, ok := readTOMLValue(tomlSpace(rest[1:]))
			table.keys = append(table.keys, tomlPair{path: key, value: value})
			return rest, ok
		})
		return table, rest, ok
	}
	word := tomlWord.FindString(text)
	return tomlValue{text: word}, text[len(word):], word != ""
}

// tomlItems reads the items of an array or an inline table, each with read,
// up to the closing bracket that ends them, and returns the text after it.
// Commas part the items, one may follow the last, and newlines and comments
// may come between them.
func tomlItems(text, closing string, read func(string) (string, bool)) (string, bool) {
	for text = tomlGap(text); !strings.HasPrefix(text, closing); {
		rest, ok := read(text)
		if !ok {
			return "", false
		}
		text = tomlGap(rest)
		if strings.HasPrefix(text, ",") {
			text = tomlGap(text[1:])
		} else if !strings.HasPrefix(text, closing) {
			return "", false
		}
	}
	return text[1:], true
}

// tomlKeyPath reads the dotted key that opens text, and the text after it.
func tomlKeyPath(text string) ([]string, string, bool) {
	var path []string
	for {
		text = tomlSpace(text)
		var segment string
		switch {
		case text == "":
			return nil, "", false
		case text[0] == '"' || text[0] == '\'':
			var ok bool
			segment, text, ok = tomlString(text)
			if !ok {
				return nil, "", false
			}
		default:
			end := strings.IndexFunc(text, func(r rune) bool {
				return !(r == '_' || r == '-' || ('0' <= r && r <= '9') || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z'))
			})
			if end < 0 {
				end = len(text)
			}
			if end == 0 {
				return nil, "", false
			}
			segment, text = text[:end], text[end:]
		}
		path = append(path, segment)
		text = tomlSpace(text)
		if !strings.HasPrefix(text, ".") {
			return path, text, true
		}
		text = text[1:]
	}
}

// tomlString reads the one-line basic or literal string that opens text, and
// the text after it.
func tomlString(text string) (string, string, bool) {
	if text[0] == '\'' {
		end := strings.IndexAny(text[1:], "'\n")
		if end < 0 || text[end+1] == '\n' {
			return "", "", false
		}
		return text[1 : end+1], text[end+2:], true
	}
	for index := 1; index < len(text); index++ {
		switch text[index] {
		case '\\':
			index++
		case '"':
			value, err := strconv.Unquote(text[:index+1])
			return value, text[index+1:], err == nil
		}
	}
	return "", "", false
}

// tomlMultiLineString passes over the multi-line basic or literal string that
// opens text, and returns the text after it. Up to two quotes just inside its
// closing delimiter are its own.
func tomlMultiLineString(text string) (string, bool) {
	delimiter := text[:3]
	for index := 3; index < len(text); index++ {
		if text[index] == '\\' && delimiter == `"""` {
			index++
			continue
		}
		if strings.HasPrefix(text[index:], delimiter) {
			quotes := len(text[index:]) - len(strings.TrimLeft(text[index:], delimiter[:1]))
			return text[index+min(quotes, 5):], true
		}
	}
	return "", false
}

// tomlSpace passes over the spaces and tabs that open text.
func tomlSpace(text string) string {
	return strings.TrimLeft(text, " \t")
}

// tomlGap passes over the spaces, tabs, newlines and comments that open text.
func tomlGap(text string) string {
	for {
		text = strings.TrimLeft(text, " \t\n")
		if !strings.HasPrefix(text, "#") {
			return text
		}
		_, text, _ = strings.Cut(text, "\n")
	}
}
