package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// shellCommandsDir holds the PowerShell script each of his ! messages runs.
const shellCommandsDir = "shell-commands"

// shellWord is a path every harness's shell takes unquoted as one word, Git
// Bash, PowerShell and cmd alike: a drive, forward slashes and no space,
// quote or character a shell splits on or expands.
var shellWord = regexp.MustCompile(`^[A-Za-z]:/[A-Za-z0-9_./-]+$`)

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
