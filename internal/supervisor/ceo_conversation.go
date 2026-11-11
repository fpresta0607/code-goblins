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
	"github.com/fpresta0607/code-goblins/internal/host"
)

// CEOConversation is the conversation the home's CEO last registered with,
// which a CEO that was closed comes back on. It is kept apart from
// primary.json, whose bytes a registration of the same process keeps, since
// questions and reviews are bound to them, while its conversation changes on
// every compact, clear and resume.
type CEOConversation struct {
	Harness string `json:"harness"`
	Session string `json:"session"`
	// Host is the native terminal the CEO ran in, empty for one in Herdr.
	Host    string    `json:"host,omitempty"`
	PID     int       `json:"pid"`
	Updated time.Time `json:"updated"`
}

// sessionID is a conversation identity a harness accepts as an argument and
// cmd /c reads as text alone.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ceoConversationPath(stateDir string) string {
	return filepath.Join(stateDir, "ceo-conversation.json")
}

// recordCEOConversation records the conversation of the CEO that just
// registered. A registration that names no session, as `goblins register` by
// hand does, leaves the last one as it was.
func recordCEOConversation(stateDir string, primary primaryRegistration, session string) error {
	if !sessionID.MatchString(session) {
		return nil
	}
	data, err := json.Marshal(CEOConversation{Harness: primary.Agent, Session: session, Host: primary.Host, PID: primary.Process.PID, Updated: time.Now().UTC()})
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(ceoConversationPath(stateDir), data)
}

// ReadCEOConversation reads the conversation the home's CEO last registered
// with.
func ReadCEOConversation(stateDir string) (CEOConversation, error) {
	var conversation CEOConversation
	data, err := fsx.ReadFile(ceoConversationPath(stateDir))
	if err != nil {
		return conversation, err
	}
	if err := json.Unmarshal(data, &conversation); err != nil {
		return conversation, err
	}
	if !sessionID.MatchString(conversation.Session) {
		return CEOConversation{}, fmt.Errorf("%s names no conversation a harness can resume", ceoConversationPath(stateDir))
	}
	return conversation, nil
}

// MatchesRunningCEO reports whether the conversation belongs to the live
// registered harness and the exact program the native terminal currently runs.
func (conversation CEOConversation) MatchesRunningCEO(stateDir string, record host.Record) bool {
	primary, isLive := livePrimary(stateDir)
	return isLive && primary.Host == record.ID &&
		primary.Process.PID == record.ChildPID && primary.Process.Start.Equal(record.ChildStart) &&
		conversation.Host == primary.Host && conversation.PID == primary.Process.PID &&
		conversation.Harness == primary.Agent && !conversation.Updated.Before(record.ChildStart)
}

// CEOConversationLeft is a conversation a CEO coming back could not resume,
// so it started on a new one. The board names it, with the harness's
// arguments that resume it by hand, until the CEO next comes back on its
// conversation.
type CEOConversationLeft struct {
	Harness string    `json:"harness"`
	Session string    `json:"session"`
	Resume  []string  `json:"resume"`
	At      time.Time `json:"at"`
}

func ceoConversationLeftPath(stateDir string) string {
	return filepath.Join(stateDir, "ceo-conversation-left.json")
}

// RecordCEOConversationLeft records left for the board, at the time it is
// recorded.
func RecordCEOConversationLeft(stateDir string, left CEOConversationLeft) error {
	left.At = time.Now().UTC()
	data, err := json.Marshal(left)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(ceoConversationLeftPath(stateDir), data)
}

// ClearCEOConversationLeft forgets the conversation left once the CEO came
// back on its conversation.
func ClearCEOConversationLeft(stateDir string) {
	_ = os.Remove(ceoConversationLeftPath(stateDir))
}

// CEOConversationLeftNotice is what the board says of the conversation the
// CEO could not resume when it last came back, and empty when there is none.
func CEOConversationLeftNotice(stateDir string) string {
	data, err := fsx.ReadFile(ceoConversationLeftPath(stateDir))
	if err != nil {
		return ""
	}
	return ceoConversationLeftNotice(data)
}

func ceoConversationLeftNotice(data []byte) string {
	var left CEOConversationLeft
	if err := json.Unmarshal(data, &left); err != nil || !sessionID.MatchString(left.Session) {
		return ""
	}
	return fmt.Sprintf("The CEO's conversation %s could not be resumed when it came back at %s, so it started on a new one. That conversation is kept: %s in the CEO's home opens it by hand.", left.Session, left.At.UTC().Format("2006-01-02 15:04 UTC"), strings.Join(append([]string{left.Harness}, left.Resume...), " "))
}
