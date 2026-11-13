package main

import (
	"context"
	"maps"
	"slices"
	"testing"
)

// Only the scheduled task whose action starts the daemon of the home that
// moved goes: whatever the case of its path, with the quotes no-mistakes
// puts around a home that has a space in it, and never the task of a home
// whose path only begins the same way.
func TestForgetGateDaemonTaskRemovesOnlyTheTaskOfThatHome(t *testing.T) {
	for _, test := range []struct {
		name, registered, forgotten string
		wantGone                    bool
	}{
		{name: "the same home", registered: `C:\Users\someone\.no-mistakes`, forgotten: `C:\Users\someone\.no-mistakes`, wantGone: true},
		{name: "another spelling of its case", registered: `c:\users\Someone\.no-mistakes`, forgotten: `C:\Users\someone\.no-mistakes`, wantGone: true},
		{name: "a home with a space", registered: `C:\Users\some one\.no-mistakes`, forgotten: `C:\Users\some one\.no-mistakes`, wantGone: true},
		{name: "a home with an ampersand", registered: `C:\Users\R&D\.no-mistakes`, forgotten: `C:\Users\R&D\.no-mistakes`, wantGone: true},
		{name: "another home", registered: `D:\CodeGoblins\no-mistakes`, forgotten: `C:\Users\someone\.no-mistakes`},
		{name: "a home that begins the same", registered: `C:\Users\someone\.no-mistakes-backup`, forgotten: `C:\Users\someone\.no-mistakes`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			tasks := &gateTaskRunner{tasks: map[string]string{
				`\no-mistakes-daemon-0a0a0a0a`: gateDaemonTask(test.registered),
				`\Another Program\Updater`:     gateDaemonTask(test.forgotten),
			}}

			// Act
			err := forgetGateDaemonTask(context.Background(), tasks, test.forgotten)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			want := []string{`\Another Program\Updater`, `\no-mistakes-daemon-0a0a0a0a`}
			if test.wantGone {
				want = want[:1]
			}
			if left := slices.Sorted(maps.Keys(tasks.tasks)); !slices.Equal(left, want) {
				t.Errorf("tasks left = %q, want %q", left, want)
			}
		})
	}
}
