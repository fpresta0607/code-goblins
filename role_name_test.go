package codegoblins

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The supervisor's role is the CEO, and everything named for it says so: the
// command, its settings, its skills, its hooks, the board and file names.
// oldRoleName is what the role was called before 2026-11-09, which only
// recorded history keeps.
const oldRoleName = "cfo"

// recordedHistory are the tracked paths that keep the old name, in their
// names and in their words, each with why.
var recordedHistory = []struct{ path, why string }{
	{"docs/evidence/", "what past runs recorded: their screenshots, logs and notes, as they were taken"},
	{"docs/plans/", "dated plans of past work, as they were written"},
	{"docs/superpowers/", "dated specs and plans of past work, as they were written"},
	{"go.sum", "module checksums, whose base64 spells the old name by chance"},
	{"connectors/crawler/package-lock.json", "a package's integrity hash, whose base64 spells the old name by chance"},
	{"role_name_test.go", "this file, which names what keeps the old name"},
	{"internal/cutover/", "the cutover, which finds each thing it moves from the old name by that name"},
}

// overlordsWords are the Supreme Overlord's own words that name the old role,
// kept as he wrote them wherever they are quoted, each with the day he wrote
// them. A quote a comment wraps onto further lines is the same words.
var overlordsWords = []struct{ day, words string }{
	{"2026-09-29", "and the board offers the same as a Restart CFO action"},
	{"2026-10-01", "Delivery unconfirmed. Inspect the CFO queue before sending again: I get these command center blips and errors, fix"},
	{"2026-10-01", "A home that has had a CFO never shows the first-run page again. While its CFO is closed the board says so in plain words with one Reopen action, which does what goblins does. No error naming a pid or telling him to run cfo register appears for a CFO he closed."},
	{"2026-10-02", "make sure that when you open the Code Goblins board for the first time, or any type of the Code Goblins app or UI for the first time, the default window is what you're seeing now on my screen, which would be the CFO terminal open and then the board on the left hand side."},
	{"2026-10-02", "The Command Center should only give me questions that the CFO has for the Overlord, for me"},
	{"2026-10-02", "when I close a Command Center question or the CFO answers it, the alert hangs around for a stale second or two in the goblin board even after answered ... or reappears a second later as the same command, it's not idempotent."},
	{"2026-10-07", "i dont like this restart the cfo button here"},
	{"2026-10-07", "you should put calude usage at cfo header only.."},
	{"2026-10-07", "it works in cfo but not other goblins so make sure that you fix that"},
	{"2026-10-07", "there should be an easy update button right on cfo header for claude code when needed ie by afk switch"},
	{"2026-10-07", "any alerts that are critical go through cfo to me as needed"},
	{"2026-10-08", "everything error wise goes to cfo and cfo decides what to tell me in command center"},
	{"2026-10-08", "everything error wise goes to cfo"},
	{"2026-10-08", "terminals in command center should have sticky input line just like cfo with scroll and jump to bottom"},
	{"2026-10-08", "what does restart the cfo do.. i want it in header of task panel please right below goblin"},
	{"2026-10-09", "CFO got overloaded with a bunch of these overlord messages"},
	{"2026-10-12", "Anything the CFO has already answered should not appear as a [line] for you in the AFK report. It should just be something that was already ... settled in history, the command center"},
	{"2026-10-17", "keep the same terminal header structure that we have for the CFO, even for goblins: the goblin image, then the name and title, and then the status of what it's doing. I do like the way the working status looks for in-progress goblins."},
	{"2026-10-18", "the header shows the restart button in terminal cfo for safe pc restart"},
	{"2026-10-20", "There's some buttons that aren't necessary on mobile, like the open terminal on the CFO header."},
	{"2026-10-26", "chat and task doesn't need to be full width, it can fit inline with cfo and afk and just be the icons"},
	{"2026-10-26", "you dont need to ahve cfo and You above each of our messages either"},
	{"2026-10-28", "remove the redundant claude or codex or pi icons in cfo banner in top left of board, only need usage"},
	{"2026-10-28", "place send button in line with message cfo height in chat input"},
	{"2026-10-28", "Why don't the queue controls match? What happens in the goblin with the icons for like send edit cancel? In CFO."},
	{"2026-10-29", "I don't like how it's stacked to have back and then the chat button. Especially in classic mode. The back button should definitely be slotted in somewhere into the classic and modern view to get back to the home screen. You could even just click on the CFO in the top, the CFO avatar, and that should take us back to the home screen."},
	{"2026-11-03", "when you click on cfo avatar the dropdown with both options"},
	{"2026-11-04", "Modern shat goblins that are not the CFO. Never pick up their cube message when I send them."},
	{"2026-11-04", "Scroll up on mobile in cfo chat jitters worse!"},
	{"2026-11-04", "afk commands dont have proper multiple choice answers like normal command cfo prompts please fix so its options arent always done / later"},
}

var oldRoleNamePattern = regexp.MustCompile(`(?i)` + oldRoleName)

// recordedHistoryLink is a path into a recorded-history folder, which names
// what it links to as that is named.
var recordedHistoryLink = func() *regexp.Regexp {
	var folders []string
	for _, entry := range recordedHistory {
		if strings.HasSuffix(entry.path, "/") {
			folders = append(folders, regexp.QuoteMeta(entry.path))
		}
	}
	return regexp.MustCompile(`(?:` + strings.Join(folders, "|") + `)[^\s)"'\]` + "`" + `]*`)
}()

// quotePattern finds words wherever they are quoted: where a comment wraps
// them, a line break, its indent and the comment's own marker stand for a
// space.
func quotePattern(words string) *regexp.Regexp {
	parts := strings.Fields(words)
	for index, part := range parts {
		parts[index] = regexp.QuoteMeta(part)
	}
	return regexp.MustCompile(strings.Join(parts, `\s+(?:(?://+|#+|\*+)[ \t]*)?`))
}

// roleNameFindings are the lines of text that name the old role, each with
// its number, once the Overlord's quoted words and links into recorded
// history are set aside, and how many times each of his quotes was found.
func roleNameFindings(text []byte, quotes []*regexp.Regexp, quoted []int) []string {
	text = recordedHistoryLink.ReplaceAll(text, nil)
	for index, quote := range quotes {
		text = quote.ReplaceAllFunc(text, func(match []byte) []byte {
			quoted[index]++
			return bytes.Repeat([]byte("\n"), bytes.Count(match, []byte("\n")))
		})
	}
	var found []string
	for number, line := range bytes.Split(text, []byte("\n")) {
		if oldRoleNamePattern.Match(line) {
			found = append(found, fmt.Sprintf("%d: %s", number+1, strings.TrimSpace(string(line))))
		}
	}
	return found
}

func isRecordedHistory(path string) bool {
	return slices.ContainsFunc(recordedHistory, func(entry struct{ path, why string }) bool {
		return path == entry.path || strings.HasSuffix(entry.path, "/") && strings.HasPrefix(path, entry.path)
	})
}

// oldRoleNameFindings are the tracked files outside recorded history whose
// path or text names the old role, and how many times each of the
// Overlord's quotes was found. A file deleted but not yet staged names
// nothing, and a binary file is not read as text.
func oldRoleNameFindings(files []string) ([]string, []int, error) {
	quotes := make([]*regexp.Regexp, len(overlordsWords))
	for index, quote := range overlordsWords {
		quotes[index] = quotePattern(quote.words)
	}
	quoted := make([]int, len(quotes))
	var findings []string
	for _, file := range files {
		if isRecordedHistory(filepath.ToSlash(file)) {
			continue
		}
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", file, err)
		}
		if oldRoleNamePattern.MatchString(filepath.ToSlash(file)) {
			findings = append(findings, file+": its path")
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		for _, found := range roleNameFindings(data, quotes, quoted) {
			findings = append(findings, file+":"+found)
		}
	}
	return findings, quoted, nil
}

func TestTheOldRoleNameIsFoundOutsideHisWords(t *testing.T) {
	// Arrange
	quotes := []*regexp.Regexp{quotePattern("place send button in line with message " + oldRoleName + " height in chat input")}
	text := strings.Join([]string{
		`// The Overlord, 2026-10-28: "place send button in line with message ` + oldRoleName,
		`	// height in chat input". The ` + strings.ToUpper(oldRoleName) + `'s composer`,
		"run " + oldRoleName + ".exe doctor",
		"the CEO runs ceo doctor",
		"see [the evidence](https://example.com/blob/main/docs/evidence/" + oldRoleName + "-board.md), as recorded",
	}, "\n")
	quoted := make([]int, len(quotes))

	// Act
	found := roleNameFindings([]byte(text), quotes, quoted)

	// Assert
	want := []string{"2: \". The " + strings.ToUpper(oldRoleName) + "'s composer", "3: run " + oldRoleName + ".exe doctor"}
	if !slices.Equal(found, want) || quoted[0] != 1 {
		t.Fatalf("found %q with the quote found %d times, want %q with it found once", found, quoted[0], want)
	}
}

func TestATrackedPathThatNamesTheOldRoleIsFound(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	named := filepath.Join(dir, oldRoleName+"-notes.md")
	if err := os.WriteFile(named, []byte("the CEO's notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	findings, _, err := oldRoleNameFindings([]string{named, filepath.Join(dir, "deleted.md")})

	// Assert
	if err != nil {
		t.Fatalf("oldRoleNameFindings: %v", err)
	}
	if want := []string{named + ": its path"}; !slices.Equal(findings, want) {
		t.Fatalf("findings %q, want %q", findings, want)
	}
}

func TestOnlyRecordedHistoryAndHisWordsKeepTheOldRoleName(t *testing.T) {
	// Arrange
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(files) < 100 {
		t.Fatalf("git ls-files listed %d files, too few to be this repository", len(files))
	}

	// Act
	findings, quoted, err := oldRoleNameFindings(files)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(findings) > 0 {
		t.Errorf("these name the old role, which is the CEO now: rename them to ceo or CEO, or, for recorded history or the Overlord's own words, add them to the lists in role_name_test.go with why:\n%s", strings.Join(findings, "\n"))
	}
	for _, entry := range recordedHistory {
		if !slices.ContainsFunc(files, func(file string) bool { return file == entry.path || strings.HasPrefix(file, entry.path) }) {
			t.Errorf("recorded history lists %s, which no tracked file is: remove it from the list", entry.path)
		}
	}
	for index, quote := range overlordsWords {
		if quoted[index] == 0 {
			t.Errorf("the Overlord's words of %s are quoted nowhere: remove them from the list: %q", quote.day, quote.words)
		}
	}
}
