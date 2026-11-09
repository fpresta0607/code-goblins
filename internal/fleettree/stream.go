package fleettree

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/fpresta0607/code-goblins/internal/sessionstream"
)

// A session's stream is what the fleet agrees on, and each harness's own
// record stays the evidence: everything below maps the messages a harness
// reader already normalized into one shape of events, and back again, so a
// harness is one adapter and no reader of the stream changes with it.

// StreamEvents is the events of a session whose messages are messages, in the
// order the session produced them. It is a pure function of that list, so a
// reader that derives it again from the same record derives the same events,
// and an append writes only what is new.
//
// A turn is named by the prompt that began it, and every event of the turn
// names it. A notice is the fleet's own word to the session rather than a
// turn, and a command the harness ran itself never reached the model, so each
// belongs to the turn it landed in without beginning one.
func StreamEvents(id sessionstream.Identity, messages []Message) []sessionstream.Event {
	events := make([]sessionstream.Event, 0, len(messages))
	turn := ""
	for _, message := range messages {
		switch message.Kind {
		case MessagePrompt:
			kind := sessionstream.KindPrompt
			if message.Source == FromNotice {
				kind = sessionstream.KindNotice
			}
			if beginsTurn(message) {
				turn = message.ID
			}
			event := sessionstream.Event{
				ID: sessionstream.EventID(id, kind, message.ID, "", 0, message.Text, message.Source, message.Output, message.Errors, strconv.FormatBool(message.IsQueued)),
				At: message.At, Kind: kind, Turn: turn, Place: message.Seq, Message: message.ID,
				Text: message.Text, Source: message.Source, Queued: message.IsQueued, Output: message.Output, Errors: message.Errors,
				Pictures: streamPictures(message.Pictures),
			}
			events = append(events, event)
		case MessageReply:
			for at, block := range message.Blocks {
				events = append(events, streamBlock(id, turn, message, at, block)...)
			}
		case MessageCompaction:
			events = append(events, sessionstream.Event{
				ID: sessionstream.EventID(id, sessionstream.KindCompaction, message.ID, "", 0, message.Text, message.Trigger),
				At: message.At, Kind: sessionstream.KindCompaction, Turn: turn, Place: message.Seq, Message: message.ID,
				Text: message.Text, Trigger: message.Trigger,
			})
		case MessageInterrupt:
			events = append(events, sessionstream.Event{
				ID: sessionstream.EventID(id, sessionstream.KindInterrupt, message.ID, "", 0, message.Text),
				At: message.At, Kind: sessionstream.KindInterrupt, Turn: turn, Place: message.Seq, Message: message.ID,
				Text: message.Text,
			})
		}
	}
	return events
}

// streamBlock is the events of one block of a reply: its text or thinking, or
// a call and the result that answers it.
func streamBlock(id sessionstream.Identity, turn string, message Message, at int, block Block) []sessionstream.Event {
	base := sessionstream.Event{
		At: message.At, Turn: turn, Place: message.Seq, Message: message.ID, Block: at, Model: message.Model,
	}
	switch block.Kind {
	case BlockThinking:
		base.ID = sessionstream.EventID(id, sessionstream.KindThinking, message.ID, "", at, block.Text)
		base.Kind = sessionstream.KindThinking
		base.Text = block.Text
		return []sessionstream.Event{base}
	case BlockCall:
		call := base
		call.ID = sessionstream.EventID(id, sessionstream.KindToolCall, message.ID, block.CallID, at, block.Tool, block.Subject, planWords(block.Plan), agentWords(block.Agent))
		call.Kind = sessionstream.KindToolCall
		call.CallID, call.Tool, call.Subject, call.State = block.CallID, block.Tool, block.Subject, block.State
		call.Plan, call.Agent = streamPlan(block.Plan), streamAgent(block.Agent)
		if block.State == CallRunning || block.State == "" {
			return []sessionstream.Event{call}
		}
		result := base
		result.ID = sessionstream.EventID(id, sessionstream.KindToolResult, message.ID, block.CallID, at, block.State, block.Result,
			strconv.FormatInt(block.Spilled, 10), editWords(block.Edit), agentWords(block.Agent))
		result.Kind = sessionstream.KindToolResult
		result.CallID, result.Tool, result.State, result.Result, result.Spilled = block.CallID, block.Tool, block.State, block.Result, block.Spilled
		result.Pictures, result.Agent = streamPictures(block.Pictures), streamAgent(block.Agent)
		if block.Edit.Added != 0 || block.Edit.Removed != 0 || len(block.Edit.Diff) > 0 {
			result.Edit = &sessionstream.Edit{Added: block.Edit.Added, Removed: block.Edit.Removed, Diff: block.Edit.Diff}
		}
		return []sessionstream.Event{call, result}
	}
	base.ID = sessionstream.EventID(id, sessionstream.KindAssistant, message.ID, "", at, block.Text, strconv.FormatBool(block.Spilled > 0))
	base.Kind = sessionstream.KindAssistant
	base.Text = block.Text
	base.Pictures = streamPictures(block.Pictures)
	return []sessionstream.Event{base}
}

func streamPictures(pictures []Picture) []sessionstream.Picture {
	if len(pictures) == 0 {
		return nil
	}
	kept := make([]sessionstream.Picture, 0, len(pictures))
	for _, picture := range pictures {
		kept = append(kept, sessionstream.Picture{ID: picture.ID, MediaType: picture.MediaType, Bytes: picture.Bytes})
	}
	return kept
}

func streamPlan(steps []PlanStep) []sessionstream.PlanStep {
	if len(steps) == 0 {
		return nil
	}
	kept := make([]sessionstream.PlanStep, 0, len(steps))
	for _, step := range steps {
		kept = append(kept, sessionstream.PlanStep{Text: step.Text, Status: step.Status})
	}
	return kept
}

func streamAgent(agent AgentLink) *sessionstream.Agent {
	if agent == (AgentLink{}) {
		return nil
	}
	return &sessionstream.Agent{Node: agent.Node, Type: agent.Type}
}

// planWords, editWords and agentWords are the parts of a call's own event id,
// so a plan, a diff or a sub-agent that changed is another event the reader
// folds over the one before.
func planWords(steps []PlanStep) string {
	words := ""
	for _, step := range steps {
		words += step.Text + "\x01" + step.Status + "\x02"
	}
	return words
}

func editWords(edit Edit) string {
	words := strconv.Itoa(edit.Added) + "\x01" + strconv.Itoa(edit.Removed)
	for _, line := range edit.Diff {
		words += "\x01" + line
	}
	return words
}

func agentWords(agent AgentLink) string {
	return agent.Node + "\x01" + agent.Type
}

// RestatedEvent says a harness rewrote its own record behind the reader, as
// pi's branch reset does, and carries the conversation its record holds now.
// An append-only stream cannot take a message back any other way, so a reader
// folds the restated conversation in place of all it held before.
func RestatedEvent(id sessionstream.Identity, messages []Message) sessionstream.Event {
	data, err := json.Marshal(messages)
	if err != nil {
		data = []byte("[]")
	}
	return sessionstream.Event{
		ID: sessionstream.EventID(id, sessionstream.KindRestated, "", "", 0, string(data)),
		At: time.Now().UTC(), Kind: sessionstream.KindRestated, Restated: string(data),
	}
}

// TurnPrompt is the prompt that began a session's newest turn, which names
// the turn, and the zero Message while the session holds no turn yet.
func TurnPrompt(messages []Message) Message {
	for at := len(messages) - 1; at >= 0; at-- {
		if beginsTurn(messages[at]) {
			return messages[at]
		}
	}
	return Message{}
}

// beginsTurn reports whether message is a prompt that begins a turn. A notice
// is the fleet's own word to the session, and a ! command, or a slash command
// the harness ran itself and recorded what it printed, as /model does, never
// reaches the model, so no hook reports a turn for it.
func beginsTurn(message Message) bool {
	if message.Kind != MessagePrompt {
		return false
	}
	switch message.Source {
	case FromNotice, FromShell:
		return false
	case FromSlash:
		return message.Output == "" && message.Errors == ""
	}
	return true
}

// MessagesOf is the conversation a stream's events are, in the order the
// session produced it: the inverse of StreamEvents, so the board renders the
// stream and the harness's record alike.
//
// A later event of the same thing takes the place of the one before it, which
// is how a call that finished replaces the call that was running, and how a
// harness that rewrote words it had written replaces them.
func MessagesOf(events []sessionstream.Event) []Message {
	ordered := make([]sessionstream.Event, 0, len(events))
	for _, event := range events {
		switch event.Kind {
		case sessionstream.KindPrompt, sessionstream.KindNotice, sessionstream.KindAssistant, sessionstream.KindThinking,
			sessionstream.KindToolCall, sessionstream.KindToolResult, sessionstream.KindCompaction, sessionstream.KindInterrupt,
			sessionstream.KindRestated:
			ordered = append(ordered, event)
		}
	}
	sort.SliceStable(ordered, func(one, other int) bool { return ordered[one].Seq < ordered[other].Seq })
	index := map[string]int{}
	messages := make([]Message, 0, len(ordered))
	for _, event := range ordered {
		if event.Kind == sessionstream.KindRestated {
			messages, index = restated(event.Restated)
			continue
		}
		at, isHeld := index[event.Message]
		if !isHeld {
			at = len(messages)
			index[event.Message] = at
			messages = append(messages, Message{ID: event.Message, Seq: event.Place, At: event.At})
		}
		message := &messages[at]
		if event.Model != "" {
			message.Model = event.Model
		}
		switch event.Kind {
		case sessionstream.KindPrompt, sessionstream.KindNotice:
			message.Kind = MessagePrompt
			message.Source, message.IsQueued, message.Text = event.Source, event.Queued, event.Text
			message.Output, message.Errors = event.Output, event.Errors
			message.Pictures = messagesPictures(event.Pictures)
		case sessionstream.KindCompaction:
			message.Kind, message.Text, message.Trigger = MessageCompaction, event.Text, event.Trigger
		case sessionstream.KindInterrupt:
			message.Kind, message.Text = MessageInterrupt, event.Text
		case sessionstream.KindAssistant, sessionstream.KindThinking, sessionstream.KindToolCall:
			message.Kind = MessageReply
			block := message.blockAt(event.Block)
			block.Kind, block.Text = BlockText, event.Text
			if event.Kind == sessionstream.KindThinking {
				block.Kind = BlockThinking
			}
			if event.Kind == sessionstream.KindToolCall {
				block.Kind, block.CallID, block.Tool, block.Subject = BlockCall, event.CallID, event.Tool, event.Subject
				block.State, block.Plan, block.Agent = event.State, messagesPlan(event.Plan), messagesAgent(event.Agent)
			}
			block.Pictures = messagesPictures(event.Pictures)
		case sessionstream.KindToolResult:
			message.Kind = MessageReply
			block := message.callAt(event)
			block.State, block.Result, block.Spilled = event.State, event.Result, event.Spilled
			block.Pictures = messagesPictures(event.Pictures)
			if event.Edit != nil {
				block.Edit = Edit{Added: event.Edit.Added, Removed: event.Edit.Removed, Diff: event.Edit.Diff}
			}
			if event.Agent != nil {
				block.Agent = messagesAgent(event.Agent)
			}
		}
	}
	for at := range messages {
		if messages[at].At.IsZero() {
			for _, event := range ordered {
				if event.Message == messages[at].ID {
					messages[at].At = event.At
					break
				}
			}
		}
	}
	sort.SliceStable(messages, func(one, other int) bool {
		if messages[one].Seq != messages[other].Seq {
			return messages[one].Seq < messages[other].Seq
		}
		return messages[one].At.Before(messages[other].At)
	})
	return messages
}

// restated is the conversation a restated event carries, and where each of
// its messages stands.
func restated(blob string) ([]Message, map[string]int) {
	var messages []Message
	if json.Unmarshal([]byte(blob), &messages) != nil {
		messages = nil
	}
	index := make(map[string]int, len(messages))
	for at, message := range messages {
		index[message.ID] = at
	}
	return messages, index
}

// blockAt is the block of a reply at index at, made where the reply holds
// none yet.
func (message *Message) blockAt(at int) *Block {
	for len(message.Blocks) <= at {
		message.Blocks = append(message.Blocks, Block{})
	}
	return &message.Blocks[at]
}

// callAt is the block of a reply a result answers: the call it names, or the
// block the result stands at when the reply never showed the call.
func (message *Message) callAt(event sessionstream.Event) *Block {
	for at := range message.Blocks {
		if message.Blocks[at].Kind == BlockCall && message.Blocks[at].CallID == event.CallID {
			return &message.Blocks[at]
		}
	}
	block := message.blockAt(event.Block)
	block.Kind, block.CallID, block.Tool = BlockCall, event.CallID, event.Tool
	return block
}

func messagesPictures(pictures []sessionstream.Picture) []Picture {
	if len(pictures) == 0 {
		return nil
	}
	kept := make([]Picture, 0, len(pictures))
	for _, picture := range pictures {
		kept = append(kept, Picture{ID: picture.ID, MediaType: picture.MediaType, Bytes: picture.Bytes})
	}
	return kept
}

func messagesPlan(steps []sessionstream.PlanStep) []PlanStep {
	if len(steps) == 0 {
		return nil
	}
	kept := make([]PlanStep, 0, len(steps))
	for _, step := range steps {
		kept = append(kept, PlanStep{Text: step.Text, Status: step.Status})
	}
	return kept
}

func messagesAgent(agent *sessionstream.Agent) AgentLink {
	if agent == nil {
		return AgentLink{}
	}
	return AgentLink{Node: agent.Node, Type: agent.Type}
}
