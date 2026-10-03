package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// CFOConversation is the conversation the home's CFO last registered with,
// which a CFO that was closed comes back on. It is kept apart from
// primary.json, whose bytes a registration of the same process keeps, since
// questions and reviews are bound to them, while its conversation changes on
// every compact, clear and resume.
type CFOConversation struct {
	Harness string `json:"harness"`
	Session string `json:"session"`
	// Host is the native terminal the CFO ran in, empty for one in Herdr.
	Host    string    `json:"host,omitempty"`
	PID     int       `json:"pid"`
	Updated time.Time `json:"updated"`
}

// sessionID is a conversation identity a harness accepts as an argument and
// cmd /c reads as text alone.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func cfoConversationPath(stateDir string) string {
	return filepath.Join(stateDir, "cfo-conversation.json")
}

// recordCFOConversation records the conversation of the CFO that just
// registered. A registration that names no session, as `cfo register` by
// hand does, leaves the last one as it was.
func recordCFOConversation(stateDir string, primary primaryRegistration, session string) error {
	if !sessionID.MatchString(session) {
		return nil
	}
	data, err := json.Marshal(CFOConversation{Harness: primary.Agent, Session: session, Host: primary.Host, PID: primary.Process.PID, Updated: time.Now().UTC()})
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(cfoConversationPath(stateDir), data)
}

// ReadCFOConversation reads the conversation the home's CFO last registered
// with.
func ReadCFOConversation(stateDir string) (CFOConversation, error) {
	var conversation CFOConversation
	data, err := fsx.ReadFile(cfoConversationPath(stateDir))
	if err != nil {
		return conversation, err
	}
	if err := json.Unmarshal(data, &conversation); err != nil {
		return conversation, err
	}
	if !sessionID.MatchString(conversation.Session) {
		return CFOConversation{}, fmt.Errorf("%s names no conversation a harness can resume", cfoConversationPath(stateDir))
	}
	return conversation, nil
}

// CFOConversationLeft is a conversation a CFO coming back could not resume,
// so it started on a new one. The board names it, with the harness's
// arguments that resume it by hand, until the CFO next comes back on its
// conversation.
type CFOConversationLeft struct {
	Harness string    `json:"harness"`
	Session string    `json:"session"`
	Resume  []string  `json:"resume"`
	At      time.Time `json:"at"`
}

func cfoConversationLeftPath(stateDir string) string {
	return filepath.Join(stateDir, "cfo-conversation-left.json")
}

// RecordCFOConversationLeft records left for the board, at the time it is
// recorded.
func RecordCFOConversationLeft(stateDir string, left CFOConversationLeft) error {
	left.At = time.Now().UTC()
	data, err := json.Marshal(left)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(cfoConversationLeftPath(stateDir), data)
}

// ClearCFOConversationLeft forgets the conversation left once the CFO came
// back on its conversation.
func ClearCFOConversationLeft(stateDir string) {
	_ = os.Remove(cfoConversationLeftPath(stateDir))
}

// CFOConversationLeftNotice is what the board says of the conversation the
// CFO could not resume when it last came back, and empty when there is none.
func CFOConversationLeftNotice(stateDir string) string {
	data, err := fsx.ReadFile(cfoConversationLeftPath(stateDir))
	if err != nil {
		return ""
	}
	var left CFOConversationLeft
	if err := json.Unmarshal(data, &left); err != nil || !sessionID.MatchString(left.Session) {
		return ""
	}
	return fmt.Sprintf("The CFO's conversation %s could not be resumed when it came back at %s, so it started on a new one. That conversation is kept: %s in the CFO's home opens it by hand.", left.Session, left.At.UTC().Format("2006-01-02 15:04 UTC"), strings.Join(append([]string{left.Harness}, left.Resume...), " "))
}
