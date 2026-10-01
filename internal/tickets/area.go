package tickets

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// Area is what a piece of work touches: the repository paths and file names a
// brief names, or the paths given by hand, and the brief's significant words.
type Area struct {
	Paths []string
	words map[string]bool
}

// BriefArea reads the area from a brief's Task and Acceptance criteria
// sections, or from the whole text when it has neither. Other sections are
// left out on purpose: a brief's Constraints name the files other goblins own,
// which are exactly the files this work must not be matched against.
func BriefArea(text string) Area {
	scope := briefScope(text)
	area := Area{words: map[string]bool{}}
	for _, word := range significantWords(scope) {
		area.words[word.stem] = true
	}
	return area.WithPaths(pathMentions(scope)...)
}

// WithPaths adds repository paths to the area, each written with forward
// slashes, and keeps them sorted and unique.
func (a Area) WithPaths(paths ...string) Area {
	merged := slices.Clone(a.Paths)
	for _, p := range paths {
		if normalized := normalizePath(p); normalized != "" {
			merged = append(merged, normalized)
		}
	}
	slices.Sort(merged)
	a.Paths = slices.Compact(merged)
	return a
}

// briefScope is the text of a brief's Task and Acceptance criteria sections.
func briefScope(text string) string {
	var scope []string
	inScope, sawSection := false, false
	for _, line := range strings.Split(text, "\n") {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok {
			heading = strings.ToLower(strings.TrimSpace(heading))
			inScope = heading == "task" || strings.HasPrefix(heading, "acceptance")
			sawSection = sawSection || inScope
			continue
		}
		if inScope {
			scope = append(scope, line)
		}
	}
	if !sawSection {
		return text
	}
	return strings.Join(scope, "\n")
}

var (
	pathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	driveLetter = regexp.MustCompile(`^[A-Za-z]:`)
	// fileNameExtensions are the extensions that make a bare word with a dot
	// a file name: "store.go" is a file, "e.g." and "cfo.exe" are not ones a
	// pull request changes.
	fileNameExtensions = []string{"c", "cfg", "cjs", "cs", "css", "go", "graphql", "h", "html", "ini", "java", "js", "json", "jsx", "kt", "md", "mdx", "mjs", "php", "proto", "ps1", "py", "rb", "rs", "scss", "sh", "sql", "svelte", "swift", "tf", "toml", "ts", "tsx", "vue", "yaml", "yml"}
)

// pathMentions finds the repository paths and file names written in prose:
// a word with a slash that is not a URL or an absolute path, or a bare file
// name with a source or document extension. Backslashes become slashes.
func pathMentions(text string) []string {
	var mentions []string
	for _, token := range strings.FieldsFunc(text, isMentionSeparator) {
		if mention := normalizePath(token); mention != "" {
			mentions = append(mentions, mention)
		}
	}
	slices.Sort(mentions)
	return slices.Compact(mentions)
}

func isMentionSeparator(r rune) bool {
	return strings.ContainsRune(" \t\r\n`'\"()[]{}<>,;|*", r)
}

// normalizePath turns one token into a repository path or file name, or ""
// when it is neither.
func normalizePath(token string) string {
	token = strings.TrimRight(strings.TrimSpace(token), ".:!?")
	if token == "" || strings.Contains(token, "://") || strings.ContainsAny(token, "@$%~") || driveLetter.MatchString(token) {
		return ""
	}
	if strings.HasPrefix(token, "/") || strings.HasPrefix(token, `\`) {
		return ""
	}
	token = strings.TrimPrefix(strings.ReplaceAll(token, `\`, "/"), "./")
	token = strings.TrimSuffix(token, "/")
	if !strings.Contains(token, "/") {
		extension := strings.ToLower(strings.TrimPrefix(path.Ext(token), "."))
		if !pathSegment.MatchString(token) || strings.TrimSuffix(token, path.Ext(token)) == "" || !slices.Contains(fileNameExtensions, extension) {
			return ""
		}
		return token
	}
	for _, segment := range strings.Split(token, "/") {
		if segment == "" || segment == "." || segment == ".." || !pathSegment.MatchString(segment) {
			return ""
		}
	}
	return token
}

// coversFile reports whether an area path covers a file a pull request or
// branch changes: the same path, a file under it, or, for a bare file name in
// the area, a file of that name anywhere. Case is ignored, as Windows does.
func coversFile(areaPath, file string) bool {
	areaPath, file = strings.ToLower(areaPath), strings.ToLower(file)
	if areaPath == file || strings.HasPrefix(file, areaPath+"/") {
		return true
	}
	return !strings.Contains(areaPath, "/") && path.Base(file) == areaPath
}

// coversMention reports whether an area path covers a path written in an
// issue. Prose often names a file without its folder, so a bare name there
// also matches an area path ending in it.
func coversMention(areaPath, mention string) bool {
	if coversFile(areaPath, mention) {
		return true
	}
	return !strings.Contains(mention, "/") && strings.EqualFold(path.Base(areaPath), mention)
}

// word is one significant word: its stem, which comparisons use, and the
// form it was written in, which the report shows.
type word struct {
	stem    string
	written string
}

var wordPattern = regexp.MustCompile(`[a-z0-9]+`)

// stopWords are the common words of four letters or more that say nothing
// about what a piece of work is about.
var stopWords = strings.Fields(`about above after again against already also always among another anything around because been before being below between both cannot could does doing done down during each either else even ever every first from further gets give given goes going have having here instead into itself just keep keeps kept like made make makes many more most much must need needs never next none once only other others over read reads said same says second should show shows since some still such than that their them then there these they thing things third this those though through today under until upon used uses using very want wants were what when where which while will with within without work working works would your yours`)

// significantWords are the words of four letters or more in text that are
// not stop words or numbers, each once, in the order first written.
func significantWords(text string) []word {
	var words []word
	seen := map[string]bool{}
	for _, written := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		if len(written) < 4 || slices.Contains(stopWords, written) || strings.Trim(written, "0123456789") == "" {
			continue
		}
		stem := written
		if len(stem) > 4 && strings.HasSuffix(stem, "s") && !strings.HasSuffix(stem, "ss") {
			stem = strings.TrimSuffix(stem, "s")
		}
		if seen[stem] {
			continue
		}
		seen[stem] = true
		words = append(words, word{stem: stem, written: written})
	}
	return words
}
