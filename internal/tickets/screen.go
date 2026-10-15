package tickets

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// Screen is what an issue may not say. Secrets are refused on every
// repository; private names, teammates and local paths only on a public one,
// where anyone can read the issue.
type Screen struct {
	IsPublic bool
	// Private are the names of the fleet's projects whose repositories are
	// not public, or are not on GitHub at all.
	Private []string
	// People are the logins and names of teammates, the people other than
	// the Overlord who work in the fleet's repositories.
	People []string
	// Account is a login an issue may mention, such as the repository's
	// owner.
	Account string
	// Secrets are credential values the filer holds, refused wherever they
	// appear.
	Secrets []string
}

var (
	// localPath is a path on a machine rather than in a repository: a
	// drive (C:\ or C:/), a network share, Git Bash's /c/, a home folder or
	// a Windows profile folder by its variable.
	localPath = regexp.MustCompile(`(?i)\b[a-z]:[\\/]|\\\\[\w.$-]+\\|(?:^|[\s"'(=\x60])(?:/[a-z]/|~[\\/])|%userprofile%|%appdata%|%localappdata%`)
	// mention is an @login; one inside an email address or a path is not.
	mention = regexp.MustCompile(`(?:^|[^\w/.@-])@([A-Za-z0-9][A-Za-z0-9-]{0,38})\b`)
)

// Refusal says why text may not be filed, naming what it found but never
// repeating a secret, or "" when it may.
func (s Screen) Refusal(text string) string {
	for _, value := range s.Secrets {
		if len(value) >= 8 && strings.Contains(text, value) {
			return "it holds a credential value this terminal carries"
		}
	}
	if strings.Contains(text, "-----BEGIN") && strings.Contains(text, "PRIVATE KEY") {
		return "it holds a private key"
	}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("=\"'`,()", r) }) {
		if auth.IsKeyValue(word) {
			return "it holds what has the shape of a key"
		}
	}
	if !s.IsPublic {
		return ""
	}
	if private := Mentioned(text, s.Private); len(private) > 0 {
		return "it names " + private[0] + ", whose repository is not public"
	}
	for _, person := range s.People {
		if pattern := namePattern(person); pattern != nil && !strings.EqualFold(person, s.Account) && pattern.MatchString(text) {
			return "it names " + person + ", a teammate"
		}
	}
	for _, match := range mention.FindAllStringSubmatch(text, -1) {
		if !strings.EqualFold(match[1], s.Account) {
			return "it mentions @" + match[1]
		}
	}
	if path := localPath.FindString(text); path != "" {
		return "it names a path on this machine (" + strings.TrimSpace(path) + "...)"
	}
	return ""
}

// Mentioned are those of names that text names, as Refusal matches them.
func Mentioned(text string, names []string) []string {
	var mentioned []string
	for _, name := range names {
		if pattern := namePattern(name); pattern != nil && pattern.MatchString(text) {
			mentioned = append(mentioned, name)
		}
	}
	return mentioned
}

// namePattern matches name as a whole word whatever its case, with its words,
// as separators and case changes split them, joined by a hyphen, an
// underscore, a dot, a space or nothing: PocketPiggies, pocket-piggies and
// Pocket Piggies are one name. A name with no letter or digit is nil.
func namePattern(name string) *regexp.Regexp {
	var words []string
	var word []rune
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			if len(word) > 0 {
				words, word = append(words, string(word)), nil
			}
			continue
		case i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) && len(word) > 0:
			words, word = append(words, string(word)), nil
		}
		word = append(word, r)
	}
	if len(word) > 0 {
		words = append(words, string(word))
	}
	if len(words) == 0 {
		return nil
	}
	for i, word := range words {
		words[i] = regexp.QuoteMeta(word)
	}
	return regexp.MustCompile(`(?i)(?:^|[^\pL\pN])` + strings.Join(words, `[-_. ]?`) + `(?:$|[^\pL\pN])`)
}
