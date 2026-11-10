package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// shellCommandsDir holds the PowerShell script each of his ! messages runs.
const shellCommandsDir = "shell-commands"

// shellWord is a path every harness's shell takes unquoted as one word, Git
// Bash, PowerShell and cmd alike: a drive, forward slashes and no space,
// quote or character a shell splits on or expands.
var shellWord = regexp.MustCompile(`^[A-Za-z]:/[A-Za-z0-9_./-]+$`)

// shellLine matches the line shellMessageLine delivers, with or without the
// harness's !, as a session records it, its group the script it runs.
var shellLine = regexp.MustCompile(`^(?:!\s*)?[A-Za-z]:/[A-Za-z0-9_./-]+ -NoProfile -ExecutionPolicy Bypass -File ([A-Za-z]:/[A-Za-z0-9_./-]+/[0-9a-f]{32}\.ps1)$`)

// shellScriptName is the script a message's command is kept in, named after
// the message, so a delivery tried again keeps the same one.
func shellScriptName(actionID string) string {
	sum := sha256.Sum256([]byte(actionID))
	return hex.EncodeToString(sum[:16]) + ".ps1"
}

// shellMessageLine is the line a message in shell mode is delivered as
// (#2051): one that starts with ! and a command runs the command in Windows
// PowerShell 5.1, never the harness's own shell. The command is kept as a
// script, encoded as a run card's is, and the line hands the harness's own !
// powershell.exe and that script, so the harness records the run and what it
// printed in the session's conversation as it records any ! command, and no
// shell parses the command itself. Each script is written to the run audit
// once, for the session it went to. isShell is false for any other message,
// which is delivered as it was written.
func shellMessageLine(stateDir, actionID, task, text string) (line string, isShell bool, err error) {
	trimmed := strings.TrimSpace(text)
	command := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
	if !strings.HasPrefix(trimmed, "!") || command == "" {
		return "", false, nil
	}
	powershell, err := runShellPath("powershell", exec.LookPath, exists, os.Getenv("SystemRoot"))
	if err != nil {
		return "", true, fmt.Errorf("%w: nothing ran: %v", ErrRejected, err)
	}
	script := filepath.Join(stateDir, shellCommandsDir, shellScriptName(actionID))
	program, file := filepath.ToSlash(powershell), filepath.ToSlash(script)
	if !shellWord.MatchString(program) || !shellWord.MatchString(file) {
		return "", true, fmt.Errorf("%w: nothing ran: the path of Windows PowerShell or of the board's state folder holds a space or a character a shell splits on, so no ! line can name it", ErrRejected)
	}
	data := append([]byte("\xef\xbb\xbf"), command...)
	isKept := exists(script)
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		return "", true, err
	}
	if err := fsx.AtomicWriteFile(script, data); err != nil {
		return "", true, err
	}
	if !isKept {
		who := task
		if who == "" {
			who = "the CFO"
		}
		if err := appendRunAudit(stateDir, Run{ID: actionID, ScriptSum: runDigest(data)}, "shell mode for "+who, time.Now().UTC()); err != nil {
			return "", true, err
		}
	}
	return "! " + program + " -NoProfile -ExecutionPolicy Bypass -File " + file, true, nil
}

// shellCommandOf is the command a shell-mode line runs, read back from the
// script it names in the board's own folder of them, and false for any other
// text or a script that folder no longer holds.
func shellCommandOf(stateDir, text string) (string, bool) {
	match := shellLine.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil || !strings.EqualFold(path.Dir(match[1]), filepath.ToSlash(filepath.Join(stateDir, shellCommandsDir))) {
		return "", false
	}
	data, err := os.ReadFile(filepath.FromSlash(match[1]))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf")), true
}

// shownShellCommands is messages and waiting with each of his shell-mode
// messages shown as the command he typed rather than the line that ran it,
// whose powershell.exe and script path say nothing he wrote (#2051): a shell
// row the session recorded, and a prompt its harness holds, which is his
// shell command still waiting. What the reader returned is left as it was.
func shownShellCommands(stateDir string, messages []fleettree.Message, waiting []fleettree.WaitingPrompt) ([]fleettree.Message, []fleettree.WaitingPrompt) {
	messages, waiting = slices.Clone(messages), slices.Clone(waiting)
	for i := range messages {
		if messages[i].Kind != fleettree.MessagePrompt || messages[i].Source != fleettree.FromShell {
			continue
		}
		if command, isShell := shellCommandOf(stateDir, messages[i].Text); isShell {
			messages[i].Text = command
		}
	}
	for i := range waiting {
		if command, isShell := shellCommandOf(stateDir, waiting[i].Text); isShell {
			waiting[i].Source, waiting[i].Text = fleettree.FromShell, command
		}
	}
	return messages, waiting
}
