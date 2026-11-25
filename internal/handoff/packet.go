// Package handoff hands a goblin's task to a cloud session through GitHub
// and takes it back (docs/plans/2026-11-24-cloud-handoff-design.md): the
// cloud session reads a screened packet in the task's pull request, works on
// a branch of its own, and its commits come back onto the goblin's branch for
// the goblin's own delivery mode to validate.
package handoff

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// The markers the packet sits between in the pull request's body, so a
// handoff writes over its own packet and keeps the rest of the body.
const (
	PacketStart = "<!-- goblins-handoff v1 -->"
	PacketEnd   = "<!-- /goblins-handoff -->"
)

// cancelledLine is the line a cancelled handoff's packet starts with.
const cancelledLine = "**Cancelled.** This handoff was cancelled: the goblin works on without the cloud session's work, so open no further pull request from it."

// Packet is what a cloud session given only the pull request's link needs:
// what is done and left, the work so far, how the task is verified and the
// rules it follows. It never carries the task's brief, its decisions or the
// goblin's status lines.
type Packet struct {
	// Handoff is the goblin's own pause handoff, written for publication.
	Handoff       string
	Issues        []string
	Branch        string
	Head          string
	DefaultBranch string
	Commits       []string
	Mode          string
	Commands      []Command
}

// Command is one verification command frozen in the task's delivery
// receipt, with what keeps a cloud session from running it.
type Command struct {
	Text          string
	IsWindowsOnly bool
	NeedsGoblins  bool
}

// VerificationCommands are the delivery receipt's fast and full commands,
// each marked Windows-only when it names a package the Linux CI job leaves
// out (except) or every package, and marked when it runs goblins, which a
// cloud session does not have.
func VerificationCommands(verification project.Verification, except []string) []Command {
	var commands []Command
	for _, command := range append(append([]project.Command{}, verification.Fast...), verification.Full...) {
		if len(command) == 0 {
			continue
		}
		commands = append(commands, Command{Text: strings.Join(command, " "), IsWindowsOnly: namesExcepted(command, except), NeedsGoblins: command[0] == "goblins"})
	}
	return commands
}

func namesExcepted(command project.Command, except []string) bool {
	if len(except) == 0 {
		return false
	}
	for _, argument := range command[1:] {
		if argument == "./..." {
			return true
		}
		for _, excepted := range except {
			if argument == excepted || argument == excepted+"/..." {
				return true
			}
		}
	}
	return false
}

// workflowPath is where a repository's Go workflow names the packages its
// Linux CI job leaves out.
var workflowPath = filepath.Join(".github", "workflows", "go.yml")

// LinuxExcept is the package list of the EXCEPT block in workflow's linux
// job, the packages whose tests the Linux CI job does not run, or nothing
// when the workflow has no such job.
func LinuxExcept(workflow string) []string {
	lines := strings.Split(strings.ReplaceAll(workflow, "\r\n", "\n"), "\n")
	isInLinuxJob, isInExcept := false, false
	var except []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(trimmed, ":"):
			if isInLinuxJob && len(except) > 0 {
				return except
			}
			isInLinuxJob, isInExcept = trimmed == "linux:", false
		case isInLinuxJob && strings.HasPrefix(trimmed, "EXCEPT:"):
			isInExcept = true
		case isInExcept && strings.HasPrefix(trimmed, "./"):
			except = append(except, trimmed)
		case isInExcept && trimmed != "":
			return except
		}
	}
	return except
}

// Text is the packet as it is written between its markers.
func (packet Packet) Text() string {
	var text strings.Builder
	line := func(format string, values ...any) { fmt.Fprintf(&text, format+"\n", values...) }
	line("%s", PacketStart)
	line("## Handoff to a cloud session")
	line("")
	line("The goblin that owns this branch is paused while a cloud session works on it. Read this packet and follow its rules.")
	line("")
	line("### Done and left")
	line("")
	line("%s", strings.TrimSpace(packet.Handoff))
	line("")
	if len(packet.Issues) > 0 {
		line("### Issues")
		line("")
		for _, issue := range packet.Issues {
			line("- %s", issue)
		}
		line("")
	}
	line("### Branch")
	line("")
	line("`%s` at `%s`, with these commits since `%s`:", packet.Branch, packet.Head, packet.DefaultBranch)
	line("")
	for _, commit := range packet.Commits {
		line("- %s", commit)
	}
	if len(packet.Commits) == 0 {
		line("- none yet")
	}
	line("")
	line("### Verification")
	line("")
	line("Delivery mode: `%s`. The goblin validates the whole branch through it once its work comes back.", packet.Mode)
	line("")
	for _, command := range packet.Commands {
		var notes []string
		if command.IsWindowsOnly {
			notes = append(notes, "Windows only: its packages are on the Linux CI job's EXCEPT list")
		}
		if command.NeedsGoblins {
			notes = append(notes, "needs goblins, which a cloud session does not have")
		}
		if len(notes) == 0 {
			line("- `%s`", command.Text)
			continue
		}
		line("- `%s` (%s)", command.Text, strings.Join(notes, "; "))
	}
	if len(packet.Commands) == 0 {
		line("- none frozen")
	}
	line("")
	line("Run the tests of the packages you change that are not on the Linux CI job's EXCEPT list.")
	line("")
	line("### Rules for this session")
	line("")
	line("- Branch from `%s` at `%s`.", packet.Branch, packet.Head)
	line("- Push only to your own branch, never to `%s`.", packet.Branch)
	line("- Open your pull request into `%s`, and say in its body what you ran with what result and what you could not run.", packet.Branch)
	line("- Never mark any pull request ready, and never merge anything.")
	text.WriteString(PacketEnd)
	return text.String()
}

// Splice is body with packet in place of the packet it holds, or after it
// when it holds none, keeping the rest of the body.
func Splice(body, packet string) string {
	start := strings.Index(body, PacketStart)
	if start >= 0 {
		if end := strings.Index(body[start:], PacketEnd); end >= 0 {
			return body[:start] + packet + body[start+end+len(PacketEnd):]
		}
	}
	if strings.TrimSpace(body) == "" {
		return packet + "\n"
	}
	return strings.TrimRight(body, "\n") + "\n\n" + packet + "\n"
}

// MarkCancelled is body with its packet marked cancelled, or body as it is
// when it holds no packet or one already marked.
func MarkCancelled(body string) string {
	start := strings.Index(body, PacketStart)
	if start < 0 || strings.Contains(body, cancelledLine) {
		return body
	}
	after := start + len(PacketStart)
	return body[:after] + "\n" + cancelledLine + body[after:]
}

// Refusal says why the packet may not be published, naming the line that
// holds what screen refuses and never repeating a secret, or "" when it may.
// A packet is screened as a public issue is on every repository, private or
// public: it is written in a pull request body, never in the home.
func Refusal(screen tickets.Screen, text string) string {
	screen.IsPublic = true
	reason := screen.Refusal(text)
	if reason == "" {
		return ""
	}
	for number, line := range strings.Split(text, "\n") {
		if lineReason := screen.Refusal(line); lineReason != "" {
			return fmt.Sprintf("line %d of the packet is refused: %s", number+1, lineReason)
		}
	}
	return "the packet is refused: " + reason
}
