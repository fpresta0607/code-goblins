package fleettree

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// browsers are the programs a job runs a browser with.
var browsers = []string{"chrome", "msedge", "firefox", "chrome-headless-shell", "headless_shell", "chromium"}

// shells only carry a command; a job is named by what runs under them.
var shells = []string{"bash", "sh", "cmd", "powershell", "pwsh", "conhost", "timeout", "sleep"}

// testCommands and buildCommands are what a job's command lines run when it
// tests or builds. A Go test binary is also named by its image, pkg.test.exe.
var (
	testCommands  = regexp.MustCompile(`(?i)(\bgo(\.exe)?"?\s+test\b|\bgo(\.exe)?"?\s+vet\b|\bvitest\b|\bjest\b|\bplaywright(\.cmd)?"?\s+test\b|\bpytest\b|\b(npm|pnpm|yarn)(\.cmd)?"?\s+(run\s+)?test\b|\bcargo\s+test\b|\bdotnet\s+test\b|\bcfo(\.exe)?"?\s+gate\s+test\b)`)
	buildCommands = regexp.MustCompile(`(?i)(\bgo(\.exe)?"?\s+build\b|\b(npm|pnpm|yarn)(\.cmd)?"?\s+(run\s+)?build\b|\bvite(\.cmd)?"?\s+build\b|\btsc(\.cmd)?"?(\s|$)|\bcargo\s+build\b|\bdotnet\s+build\b|\bmsbuild\b|\bwebpack\b)`)
)

// jobFacts is what a job's processes say about it: their command lines, by
// process id, and the ports each listens on.
type jobFacts struct {
	commands map[int]string
	listens  map[int][]int
}

// classify says what a job is doing and names it. A test run or build
// wins over the ports it opens, since tests listen on throwaway ones; a
// job listening on a port is a dev server; one running a browser and nothing
// else, a browser.
func classify(j job, facts jobFacts) (Group, string, string) {
	var tests, builds, browsing bool
	var ports []int
	programs := map[string]bool{}
	for _, member := range j.members {
		name := executableName(member.Exe)
		command := facts.commands[member.PID]
		if strings.HasSuffix(name, ".test") || testCommands.MatchString(command) {
			tests = true
		}
		if buildCommands.MatchString(command) {
			builds = true
		}
		if slices.Contains(browsers, name) {
			browsing = true
		}
		ports = append(ports, facts.listens[member.PID]...)
		if !slices.Contains(shells, name) {
			programs[name] = true
		}
	}
	slices.Sort(ports)
	ports = slices.Compact(ports)
	detail := jobDetail(j, programs)
	switch {
	case tests:
		return GroupTest, "Test run", detail
	case builds:
		return GroupBuild, "Build", detail
	case len(ports) > 0:
		return GroupDevServer, fmt.Sprintf("Dev server :%d", ports[0]), detail
	case browsing:
		return GroupBrowser, "Browser", detail
	}
	label := commandLabel(facts.commands[j.root.PID])
	if label == "" {
		label = j.root.Exe
	}
	return GroupOther, label, detail
}

// jobDetail names a job's programs, shells aside, and how many processes it
// runs.
func jobDetail(j job, programs map[string]bool) string {
	names := make([]string, 0, len(programs))
	for name := range programs {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) > 3 {
		names = append(names[:3], "...")
	}
	count := "1 process"
	if len(j.members) != 1 {
		count = fmt.Sprintf("%d processes", len(j.members))
	}
	if len(names) == 0 {
		return count
	}
	return strings.Join(names, ", ") + ", " + count
}

// evaluated is the command a Claude Code shell runs: it wraps each command
// as eval '<command>' after sourcing its shell snapshot, with each single
// quote written as '\”.
var evaluated = regexp.MustCompile(`eval '((?:[^']|'\\'')*)'`)

// shellCommand is the command a shell's command line runs, as typed.
func shellCommand(commandLine string) string {
	match := evaluated.FindStringSubmatch(commandLine)
	if match == nil {
		return ""
	}
	return strings.ReplaceAll(match[1], `'\''`, `'`)
}

// commandLabel is a short label for what a job's first process runs: the
// command its shell evaluates, else its command line, on one line.
func commandLabel(commandLine string) string {
	if command := shellCommand(commandLine); command != "" {
		commandLine = command
	}
	return bounded(strings.Join(strings.Fields(commandLine), " "), 80)
}

// bounded cuts text to at most limit characters, marking the cut.
func bounded(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-3]) + "..."
}
