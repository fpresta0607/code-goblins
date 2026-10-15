package fleettree

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/proc"
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

// heavyLineRun is a run in the machine's heavy line, which waits there for
// its turn, holds it while its command runs, and tells its goblin how it
// ended.
var heavyLineRun = regexp.MustCompile(`(?i)\b(cfo|goblins)(\.exe)?"?\s+gate\s+turn\b`)

// isHeavyRun says a job is or runs a run in the machine's heavy line, by its
// processes' command lines.
func (j job) isHeavyRun(facts jobFacts) bool {
	return slices.ContainsFunc(j.members, func(member Process) bool {
		return heavyLineRun.MatchString(facts.commands[member.PID])
	})
}

// isService says a job is or runs a machine service (proc.ServiceOf), by
// its programs and their command lines.
func (j job) isService(facts jobFacts) bool {
	return slices.ContainsFunc(j.members, func(member Process) bool {
		var arguments []string
		if command := facts.commands[member.PID]; command != "" {
			arguments, _ = proc.SplitCommandLine(command)
		}
		return proc.ServiceOf(member.Exe, arguments) != proc.NoService
	})
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

// shellCommand is the command a shell's command line runs, as typed. Claude
// Code runs each command as bash -c "<setup> && eval <command> < /dev/null",
// the command quoted as one bash word, each single quote in it written as
// '"'"' and each double quote escaped once more for Windows.
func shellCommand(commandLine string) string {
	arguments, err := proc.SplitCommandLine(commandLine)
	if err != nil {
		return ""
	}
	for _, argument := range arguments {
		if _, script, found := strings.Cut(" "+argument, " eval "); found {
			return bashWord(script)
		}
	}
	return ""
}

// bashWord is the first word of script as bash reads it: its single-quoted,
// double-quoted and escaped parts joined, up to the first blank outside
// quotes. An unterminated quote reads as no word.
func bashWord(script string) string {
	var word strings.Builder
	for i := 0; i < len(script); i++ {
		switch character := script[i]; {
		case character == '\'':
			end := strings.IndexByte(script[i+1:], '\'')
			if end < 0 {
				return ""
			}
			word.WriteString(script[i+1 : i+1+end])
			i += end + 1
		case character == '"':
			for i++; i < len(script) && script[i] != '"'; i++ {
				if script[i] == '\\' && i+1 < len(script) && strings.IndexByte("\"\\$`", script[i+1]) >= 0 {
					i++
				}
				word.WriteByte(script[i])
			}
			if i == len(script) {
				return ""
			}
		case character == '\\' && i+1 < len(script):
			i++
			word.WriteByte(script[i])
		case character == ' ' || character == '\t' || character == '\n':
			return word.String()
		default:
			word.WriteByte(character)
		}
	}
	return word.String()
}

// cmdWrapper is cmd.exe running one command, as Windows starts npm's and
// node's children: cmd.exe /d /s /c "<command>".
var cmdWrapper = regexp.MustCompile(`(?i)^(?:"(?:[^"]*\\)?cmd(?:\.exe)?"|(?:\S*\\)?cmd(?:\.exe)?)(?:\s+/[a-z])*\s+(.*)$`)

// programPath is the folder and .exe of a command's program, which say
// nothing of what it does.
var programPath = regexp.MustCompile(`(?i)^(?:"(?:[^"]*\\)?([^"\\]+?)(?:\.exe)?"|(?:\S*\\)?([^\s\\]+?)(?:\.exe)?)(\s|$)`)

// commandTask is what a job's first process runs, as it was typed: the
// command its shell evaluates or cmd.exe runs, its program named without
// its folder, on one line.
func commandTask(commandLine string) string {
	command := shellCommand(commandLine)
	if command == "" {
		command = strings.TrimSpace(commandLine)
		if match := cmdWrapper.FindStringSubmatch(command); match != nil {
			command = strings.TrimSpace(match[1])
			if len(command) > 1 && strings.HasPrefix(command, `"`) && strings.HasSuffix(command, `"`) {
				command = command[1 : len(command)-1]
			}
		}
		command = programPath.ReplaceAllString(command, "$1$2$3")
	}
	return task(command)
}

// typed is a command as a terminal shows it was typed, after >, and nothing
// for no command.
func typed(command string) []string {
	if command = firstLine(command); command == "" {
		return nil
	}
	return []string{bounded("> "+command, agentLineLimit)}
}

// commandLabel is a short label for what a job's first process runs.
func commandLabel(commandLine string) string {
	return bounded(commandTask(commandLine), 80)
}

// bounded cuts text to at most limit characters, marking the cut.
func bounded(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-3]) + "..."
}
