package supervisor

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// A session remembers when its generation last took a prompt: tool activity
// after it keeps the time, and the next prompt moves it.
func TestASessionRemembersWhenItTookAPrompt(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	for i, e := range []nativehook.Event{
		event(t, h, "SessionStart", "s1", "", now),
		event(t, h, "UserPromptSubmit", "s1", "t1", now.Add(time.Second)),
		event(t, h, "PostToolUse", "s1", "t1", now.Add(2*time.Second)),
	} {
		if err := s.Accept(e); err != nil {
			t.Fatalf("accept event %d: %v", i, err)
		}
	}
	if got := s.Snapshot().Sessions["codex/s1"].PromptAt; !got.Equal(now.Add(time.Second)) {
		t.Errorf("PromptAt after tool activity = %v, want the prompt's time %v", got, now.Add(time.Second))
	}

	if err := s.Accept(event(t, h, "UserPromptSubmit", "s1", "t2", now.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}

	if got := s.Snapshot().Sessions["codex/s1"].PromptAt; !got.Equal(now.Add(3 * time.Second)) {
		t.Errorf("PromptAt after the next prompt = %v, want %v", got, now.Add(3*time.Second))
	}
}

// A goblin's prompt is proven from its hook's event while the event waits in
// the spool, and from its session once the supervisor took it: for its own
// task and generation only, and only at or after the time asked about.
func TestNativePromptSinceReadsTheSpoolAndTheStore(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	prompt := event(t, h, "UserPromptSubmit", "s1", "t1", now)
	if err := nativehook.Spool(h.State, prompt); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name, task, generation string
		since                  time.Time
		want                   bool
	}{
		{"its own prompt in the spool", "task-1", "g1", now, true},
		{"another generation", "task-1", "g2", now, false},
		{"another task", "task-2", "g1", now, false},
		{"a prompt before the time asked about", "task-1", "g1", now.Add(time.Second), false},
	} {
		got, err := NativePromptSince(h.State, check.task, check.generation, check.since)
		if err != nil || got != check.want {
			t.Errorf("%s: NativePromptSince = %v, %v; want %v", check.name, got, err, check.want)
		}
	}

	if err := s.Ingest(); err != nil {
		t.Fatal(err)
	}

	if got, err := NativePromptSince(h.State, "task-1", "g1", now); err != nil || !got {
		t.Errorf("after ingest: NativePromptSince = %v, %v; want the store to prove the prompt", got, err)
	}
	if got, err := NativePromptSince(h.State, "task-1", "g1", now.Add(time.Second)); err != nil || got {
		t.Errorf("after ingest, a later time: NativePromptSince = %v, %v; want false", got, err)
	}
}

// A home with no hook events and no supervisor store proves nothing, without
// error.
func TestNativePromptSinceWithoutHooksProvesNothing(t *testing.T) {
	got, err := NativePromptSince(t.TempDir(), "task-1", "g1", time.Now())

	if err != nil || got {
		t.Errorf("NativePromptSince = %v, %v; want false with no error", got, err)
	}
}
