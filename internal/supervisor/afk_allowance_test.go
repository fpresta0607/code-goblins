package supervisor

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/quota"
)

// The allowance a switch keeps is the supervisor's last reading of each
// provider from its own quota-axi reads: every window's use and any credit
// balance. A provider whose numbers quota-axi itself calls stale, and a read
// that failed, keep the reading before, and a reading older than an hour is
// none.
func TestASwitchKeepsTheSupervisorsLastReadingOfEachProvider(t *testing.T) {
	// Arrange
	reset := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	answers := []struct {
		report  quota.Report
		skipped string
	}{
		{report: quota.Report{Providers: map[string]quota.Provider{
			"claude": {Name: "claude", Windows: []quota.Window{{ID: "five_hour", Label: "session", PercentUsed: 88, ResetsAt: reset}, {ID: "seven_day", Label: "week", PercentUsed: 1}}},
			"codex":  {Name: "codex", Windows: []quota.Window{{ID: "weekly", PercentUsed: 40}}, Credits: &quota.Credits{Remaining: 12, Unit: "credits"}},
		}}},
		{report: quota.Report{Providers: map[string]quota.Provider{
			"claude": {Name: "claude", Stale: true, Windows: []quota.Window{{ID: "seven_day", Label: "week", PercentUsed: 99}}},
			"codex":  {Name: "codex", Windows: []quota.Window{{ID: "weekly", PercentUsed: 41}}, Credits: &quota.Credits{Remaining: 11, Unit: "credits"}},
		}}},
		{skipped: "quota-axi: snapshot is stale"},
	}
	s := &Service{Options: Options{Quota: func(context.Context) (quota.Report, string) {
		answer := answers[0]
		answers = answers[1:]
		return answer.report, answer.skipped
	}}}

	// Act
	for range 3 {
		s.readQuota(t.Context(), time.Second)
	}
	held, old := s.heldAllowance(time.Now()), s.heldAllowance(time.Now().Add(quota.MaxAge+time.Minute))

	// Assert
	want := []afk.Allowance{
		{Provider: "claude", Window: "session", PercentUsed: 88, ResetsAt: reset},
		{Provider: "claude", Window: "week", PercentUsed: 1},
		{Provider: "codex", Window: "weekly", PercentUsed: 41},
		{Provider: "codex", Window: "credits", Credits: true, Remaining: 11, Unit: "credits"},
	}
	if !slices.Equal(held, want) {
		t.Errorf("heldAllowance = %+v, want %+v", held, want)
	}
	if len(old) != 0 {
		t.Errorf("heldAllowance an hour on = %+v, want none", old)
	}
}
