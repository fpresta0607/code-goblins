package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin's turn ends on an API error once its harness gives up retrying a
// request to the model's API that failed to connect or timed out, and the
// goblin then sits at its prompt: on 2026-11-24 about 17 did, after the
// network came back, until each was typed into or restarted by hand (#2863).
// The supervisor carries such a goblin on once its API answers again.
const (
	// apiErrorCarryOnEvery is how often the supervisor looks for them, and
	// apiErrorCarryOnGap how long a goblin it carried on waits before it is
	// carried on again, so one whose every turn fails on the API is carried
	// on at most once in that time.
	apiErrorCarryOnEvery = 30 * time.Second
	apiErrorCarryOnGap   = 15 * time.Minute
	// apiCheckTimeout bounds the check that an API answers.
	apiCheckTimeout = 10 * time.Second
)

// apiErrorNote is the note the supervisor types into a goblin it carries on.
const apiErrorNote = "The API answers again after your last turn ended on an API error; carry on from where you stopped."

// apiOrigins is where each harness's requests to its model's API go, by
// harness, and for pi by the provider its model names first, as
// "openrouter/deepseek/deepseek-v4.1-flash": Claude Code's Messages API,
// Codex's ChatGPT backend and OpenRouter. A goblin whose API is not here is
// not carried on, and its wake names its error for the CEO.
var apiOrigins = map[string]string{
	string(harness.Claude): "https://api.anthropic.com",
	string(harness.Codex):  "https://chatgpt.com",
	"pi/openrouter":        "https://openrouter.ai",
}

// apiOrigin is where meta's goblin's requests to its model's API go.
func apiOrigin(meta state.TaskMeta) (string, bool) {
	key := meta.Harness
	if harness.Kind(meta.Harness) == harness.Pi {
		provider, _, _ := strings.Cut(meta.Model, "/")
		key += "/" + provider
	}
	origin, isKnown := apiOrigins[key]
	return origin, isKnown
}

// keepCarryingOnAfterAPIErrors carries goblins on after API errors every
// interval until ctx ends.
func (s *Service) keepCarryingOnAfterAPIErrors(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.carryOnAfterAPIErrors(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// carryOnAfterAPIErrors carries on each live goblin, not paused or changing,
// whose screen shows its turn ended on an API error, once a check finds its
// API answering again: it types the goblin a note, held for a turn, and
// restarts it in place when its prompt takes no typing. An error routing
// names as a provider's refusal, such as a rate limit, is the watcher's to
// route, and a goblin carried on within apiErrorCarryOnGap waits.
func (s *Service) carryOnAfterAPIErrors(ctx context.Context, now time.Time) {
	stateDir := s.Store.Home.State
	answers := map[string]error{}
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" {
			continue
		}
		if record, err := state.ReadLifecycle(stateDir, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.Phase != "running" {
			continue
		}
		if carried, isCarried := s.apiErrorCarried[meta.ID]; isCarried && now.Sub(carried) < apiErrorCarryOnGap {
			continue
		}
		record, err := host.ReadRecord(stateDir, meta.ID)
		if err != nil || !host.Running(record) {
			continue
		}
		screen, err := s.readScreen(record)
		if err != nil {
			continue
		}
		screens, _ := harness.NativeScreens(harness.Kind(meta.Harness))
		failure, isEnded := screens.EndedOnAPIError(screen)
		if !isEnded {
			continue
		}
		if _, _, isRefusal := routing.Detect(failure); isRefusal {
			continue
		}
		origin, isKnown := apiOrigin(meta)
		if !isKnown {
			continue
		}
		answer, isChecked := answers[origin]
		if !isChecked {
			answer = s.apiAnswers(ctx, origin)
			answers[origin] = answer
		}
		if answer != nil {
			continue
		}
		if s.apiErrorCarried == nil {
			s.apiErrorCarried = map[string]time.Time{}
		}
		s.apiErrorCarried[meta.ID] = now
		err = typeIntoGoblin(ctx, stateDir, meta, goblinLine(apiErrorNote), true, false, false)
		switch {
		case err == nil, errors.Is(err, spawn.ErrInTurn):
		case errors.Is(err, spawn.ErrUnsentWords), errors.Is(err, fleet.ErrNotTyped):
			s.restartAfterAPIError(meta)
		default:
			s.publish(fmt.Errorf("carrying %s on after its turn ended on an API error (%s): %w", meta.ID, failure, err))
		}
	}
}

// apiAnswers checks that origin answers, through the test's check when it has
// one.
func (s *Service) apiAnswers(ctx context.Context, origin string) error {
	if s.Options.APIAnswers != nil {
		return s.Options.APIAnswers(ctx, origin)
	}
	ctx, cancel := context.WithTimeout(ctx, apiCheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, origin, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// restartAfterAPIError starts restarting meta's goblin in place on its own
// harness, model, effort and conversation, as a pause and resume does, unless
// another change of it is under way. The restart runs on by itself, as a
// switch the board started does, so the supervisor's stop never waits on it.
func (s *Service) restartAfterAPIError(meta state.TaskMeta) {
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.changing[meta.ID] != "" {
		return
	}
	s.markChanging(meta.ID, "switch")
	go func() {
		output, err := s.Options.Dispatch.Spawn(context.Background(), []string{"switch", meta.ID, "--generation", meta.SpawnGen, "--restart"})
		if err != nil {
			s.publish(fmt.Errorf("restarting %s in place after its turn ended on an API error: %s", meta.ID, spawnFailure(output, err)))
		}
		s.starts.Lock()
		delete(s.changing, meta.ID)
		s.starts.Unlock()
		s.notify()
	}()
	s.notify()
}
