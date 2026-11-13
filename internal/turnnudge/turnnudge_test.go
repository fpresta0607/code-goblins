package turnnudge

import (
	"encoding/json"
	"testing"
)

// A turn's last words that promise an action it never took are nudged, in
// each form a goblin ends a turn on one.
func TestPromisesReadsAnActionALastReplyPromises(t *testing.T) {
	for _, reply := range []string{
		"I will now run the tests.",
		"I'll now run the tests",
		"Now let me run the full suite to verify.",
		"The fix is in. Next, I'll push the branch and start the gate run.",
		"Let me look at the supervisor's evaluate action:",
		"I'm going to open the pull request now.",
		"I am about to rerun the failing test by name.",
		"OK so I'll go ahead and commit this.",
		"Tests pass, so I’ll open the PR now.",
		"Running the gate now.",
		"Now pushing the branch.",
		"Let's check the logs.",
		"Let me check if the build passed.",
		"Let me run it once more.",
		"I'll roll back the change.",
		"Time to run the gate.",
		"**Next:** I'll write the regression test.",
		"Summary of the change:\n- the hook reads the reply\n- the nudge comes once\n\nNow let me run the tests.",
		"I'll run:\n```\ngo test ./internal/turnnudge\n```",
		"Let me push, then wait for CI.",
	} {
		t.Run(reply, func(t *testing.T) {
			// Act
			isPromise := Promises(reply)

			// Assert
			if !isPromise {
				t.Fatalf("Promises(%q) = false, want true", reply)
			}
		})
	}
}

// A turn that is meant to end is not nudged: a question to the CEO, a done
// report, a wait, an action held until something else happens, an action
// already taken, or words that promise nothing.
func TestPromisesLeavesATurnMeantToEndAlone(t *testing.T) {
	for _, reply := range []string{
		"",
		"   \n\n ",
		"Should I open the pull request now, or wait for the CEO?",
		"I can push the branch now. Do you want me to?",
		"Reported done: PR https://github.com/example/repo/pull/1 is open with Fixes #1.",
		"Done. The gate and CI are green.",
		"I'll wait for CI to finish.",
		"I will wait for the CEO's answer.",
		"Waiting on the gate run now.",
		"Standing by.",
		"I'll stand by for the CEO's word.",
		"I'll report back.",
		"Let me know if you need anything else.",
		"I'll keep an eye on CI.",
		"I'll merge it once CI is green.",
		"Once CI is green, I'll merge.",
		"I'll start #1873 after the CEO lands this one.",
		"I'll continue when you answer.",
		"I'll fix it if the review asks for it.",
		"I'll run the full suite as soon as the machine is free.",
		"I'll look at it later.",
		"I'll wait for CI. Then I'll merge.",
		"I'll wait for CI, then I'll merge.",
		"I will not push until the CEO says so.",
		"I'll leave it there.",
		"I ran the tests and they pass.",
		"The hook I will add nudges once.",
		"Nothing to do now.",
		"Let me run the tests.\n\nSummary:\n- the hook reads the reply",
		"Here is the diff:\n```\n-old\n+new\n```",
	} {
		t.Run(reply, func(t *testing.T) {
			// Act
			isPromise := Promises(reply)

			// Assert
			if isPromise {
				t.Fatalf("Promises(%q) = true, want false", reply)
			}
		})
	}
}

// A Stop is nudged only at the turn's own end, never when a Stop hook already
// continued the turn, so a turn is nudged at most once, nor while background
// work or a session timer will wake the session, nor at a subagent's stop.
func TestShouldNudgeComesOnceATurnAndNotWhileWorkWillWakeTheSession(t *testing.T) {
	promise := "I will now run the tests."
	for _, test := range []struct {
		name string
		stop Stop
		want bool
	}{
		{"a turn's own end on a promise", Stop{Event: "Stop", LastAssistantMessage: promise}, true},
		{"a turn a Stop hook already continued", Stop{Event: "Stop", LastAssistantMessage: promise, IsStopHookActive: true}, false},
		{"a turn with background work in flight", Stop{Event: "Stop", LastAssistantMessage: promise, BackgroundTasks: []json.RawMessage{json.RawMessage(`{"id":"b1"}`)}}, false},
		{"a turn a session timer will wake", Stop{Event: "Stop", LastAssistantMessage: promise, SessionCrons: []json.RawMessage{json.RawMessage(`{"id":"c1"}`)}}, false},
		{"a subagent's stop", Stop{Event: "SubagentStop", LastAssistantMessage: promise}, false},
		{"a turn that ends on a question", Stop{Event: "Stop", LastAssistantMessage: "Should I run the tests?"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			isNudged := test.stop.ShouldNudge()

			// Assert
			if isNudged != test.want {
				t.Fatalf("ShouldNudge() = %v, want %v", isNudged, test.want)
			}
		})
	}
}

// Reply is the Stop hook's answer as Claude Code and Codex read it: a block
// whose reason is Message for a nudged Stop, and an empty object, which lets
// the turn end, for any other payload, one that cannot be read included.
func TestReplyBlocksTheStopWithTheNudgeOnlyForANudgedStop(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		isBlocked     bool
	}{
		{"a Claude Code Stop on a promise", `{"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Now let me run the tests.","background_tasks":[],"session_crons":[]}`, true},
		{"a Codex Stop on a promise", `{"hook_event_name":"Stop","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"I'll push the branch now."}`, true},
		{"a Codex Stop with no last message", `{"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":null}`, false},
		{"a Stop already continued", `{"hook_event_name":"Stop","stop_hook_active":true,"last_assistant_message":"Now let me run the tests."}`, false},
		{"a payload that cannot be read", `not json`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			reply := Reply([]byte(test.payload))

			// Assert
			var answer struct{ Decision, Reason string }
			if err := json.Unmarshal([]byte(reply), &answer); err != nil {
				t.Fatalf("Reply = %q, not JSON: %v", reply, err)
			}
			if !test.isBlocked {
				if reply != "{}" {
					t.Fatalf("Reply = %q, want {}", reply)
				}
				return
			}
			if answer.Decision != "block" || answer.Reason != Message {
				t.Fatalf("Reply = %q, want a block with the nudge", reply)
			}
		})
	}
}
