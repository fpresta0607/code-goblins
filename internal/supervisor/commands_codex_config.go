package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
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

// tomlPair is one key a TOML line sets, as its dotted path below the table it
// is in, and its value: the text of a string, or a bare word such as true.
type tomlPair struct {
	path     []string
	value    string
	isString bool
}

// readCodexConfig reads the settings of codexConfig from config.toml in home.
// It reads the TOML Codex and its users write for them: table and array
// table headers, dotted and quoted keys, string and boolean values and
// inline tables of them. Other values, multi-line strings and arrays among
// them, are passed over.
func readCodexConfig(home string) (codexConfig, error) {
	config := codexConfig{isBundledEnabled: true, plugins: map[string]bool{}}
	data, err := fsx.ReadFile(filepath.Join(home, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	var table []string
	rule := -1
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			isArray := strings.HasPrefix(line, "[[")
			path, rest, ok := tomlKeyPath(strings.TrimLeft(line, "["))
			if !ok || !strings.HasPrefix(rest, "]") {
				return config, errors.New("unreadable Codex table header: " + line)
			}
			table, rule = path, -1
			if isArray && slices.Equal(path, []string{"skills", "config"}) {
				config.skillRules = append(config.skillRules, codexSkillRule{isEnabled: true})
				rule = len(config.skillRules) - 1
			}
			config.set(home, tomlPair{path: path}, rule)
			continue
		}
		key, rest, ok := tomlKeyPath(line)
		if !ok || !strings.HasPrefix(rest, "=") {
			return config, errors.New("unreadable Codex setting: " + line)
		}
		value := strings.TrimSpace(rest[1:])
		if delimiter := value[:min(3, len(value))]; delimiter == `"""` || delimiter == "'''" {
			for !strings.Contains(value[3:], delimiter) && index+1 < len(lines) {
				index++
				value = "   " + lines[index]
			}
			continue
		}
		if strings.HasPrefix(value, "[") {
			for depth := tomlArrayDepth(value, 0); depth > 0 && index+1 < len(lines); {
				index++
				depth = tomlArrayDepth(lines[index], depth)
			}
			continue
		}
		pairs, _, ok := tomlValue(value)
		if !ok {
			continue
		}
		for _, pair := range pairs {
			pair.path = append(append(slices.Clone(table), key...), pair.path...)
			config.set(home, pair, rule)
		}
	}
	return config, nil
}

// set records one key the configuration sets, by its full path, inside the
// [[skills.config]] entry rule when there is one.
func (config *codexConfig) set(home string, pair tomlPair, rule int) {
	path, isTrue := pair.path, !pair.isString && pair.value == "true"
	isBool := !pair.isString && (pair.value == "true" || pair.value == "false")
	switch {
	case rule >= 0 && len(path) == 3:
		switch {
		case path[2] == "name" && pair.isString:
			config.skillRules[rule].name = strings.TrimSpace(pair.value)
		case path[2] == "path" && pair.isString:
			skillPath := pair.value
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
}

// tomlKeyPath reads the dotted key that opens text, and the text after it.
func tomlKeyPath(text string) ([]string, string, bool) {
	var path []string
	for {
		text = strings.TrimSpace(text)
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
		text = strings.TrimSpace(text)
		if !strings.HasPrefix(text, ".") {
			return path, text, true
		}
		text = text[1:]
	}
}

// tomlString reads the basic or literal string that opens text, and the text
// after it.
func tomlString(text string) (string, string, bool) {
	if text[0] == '\'' {
		end := strings.IndexByte(text[1:], '\'')
		if end < 0 {
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

// tomlValue reads the value that opens text: a string, a bare word, or an
// inline table of them, each key it sets by its path below the value.
func tomlValue(text string) ([]tomlPair, string, bool) {
	switch {
	case text == "":
		return nil, "", false
	case text[0] == '"' || text[0] == '\'':
		value, rest, ok := tomlString(text)
		return []tomlPair{{value: value, isString: true}}, rest, ok
	case text[0] == '{':
		var pairs []tomlPair
		rest := strings.TrimSpace(text[1:])
		for !strings.HasPrefix(rest, "}") {
			key, after, ok := tomlKeyPath(rest)
			if !ok || !strings.HasPrefix(after, "=") {
				return nil, "", false
			}
			values, after, ok := tomlValue(strings.TrimSpace(after[1:]))
			if !ok {
				return nil, "", false
			}
			for _, value := range values {
				value.path = append(slices.Clone(key), value.path...)
				pairs = append(pairs, value)
			}
			rest = strings.TrimSpace(after)
			if strings.HasPrefix(rest, ",") {
				rest = strings.TrimSpace(rest[1:])
			} else if !strings.HasPrefix(rest, "}") {
				return nil, "", false
			}
		}
		return pairs, rest[1:], true
	}
	end := strings.IndexAny(text, " \t,}#")
	if end < 0 {
		end = len(text)
	}
	return []tomlPair{{value: text[:end]}}, text[end:], end > 0
}

// tomlArrayDepth is how many arrays are still open after text, given depth
// open before it, its strings and comments passed over.
func tomlArrayDepth(text string, depth int) int {
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '[':
			depth++
		case ']':
			depth--
		case '#':
			return depth
		case '"', '\'':
			if _, rest, ok := tomlString(text[index:]); ok {
				index = len(text) - len(rest) - 1
			}
		}
	}
	return depth
}
