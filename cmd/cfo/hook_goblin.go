package main

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// goblinSealRefusal is what a goblin is told of a browser command a key
// fetch's seal refused.
const goblinSealRefusal = "A key fetch is reading in your login's browser now, so chrome-devtools-axi commands wait until it ends, within two minutes. Run this command again after that."

// hookGoblinSeal refuses a goblin's chrome-devtools-axi command while a key
// fetch reads in the browser its task holds, so nothing the page shows
// reaches its transcript. Every other command passes, and so does every
// command when the goblin's home or task cannot be read, since no fetch can
// seal a browser no task holds. A seal that cannot be read refuses.
func hookGoblinSeal(stdin io.Reader, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok || !strings.Contains(strings.ToLower(payload.Command), "chrome-devtools-axi") {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if isSealed, _ := browserlogin.Sealed(h.State, os.Getenv("CFO_TASK_ID"), time.Now()); !isSealed {
		return 0
	}
	return claudehook.DenyPreTool(stderr, goblinSealRefusal)
}

// hookGoblinKeyAlarm wakes the CFO when a goblin's tool call gave back text
// holding a secret key, naming the goblin, the tool and the kind of key and
// never the key: the call's output is in the goblin's transcript already, so
// the key is exposed and a real one has to be rolled. One tool call wakes
// the CFO once.
func hookGoblinKeyAlarm(stdin io.Reader) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	kinds := auth.ExposedKeys(payload.ToolResponse)
	task := os.Getenv("CFO_TASK_ID")
	if len(kinds) == 0 || state.ValidTaskID(task) != nil {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	held := kinds[len(kinds)-1]
	if len(kinds) > 1 {
		held = strings.Join(kinds[:len(kinds)-1], ", ") + " and " + held
	}
	detail := task + "'s " + payload.ToolName + " output held " + held + ", so that key is in its transcript now. Treat it as exposed and roll it if it is real. The value is not repeated here."
	if _, isNew, err := wake.AppendFirst(h.State, "key-alarm "+payload.SessionID+" "+payload.ToolUseID, "review", task, detail); err == nil && isNew {
		_, _ = wake.PublishEpisode(h.State)
	}
	return 0
}
