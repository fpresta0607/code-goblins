package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/harness"
)

const allowanceFloorUsage = "usage: cfo allowance-floor [<claude|codex> <percent>]"

// runAllowanceFloor says each provider's weekly allowance floor, the percent
// of its week the fleet keeps back, or sets one provider's in the home's
// config/fleet.json. The supervisor's pause, the board's dials, cfo spawn
// and its lane choice all read that setting.
func runAllowanceFloor(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 0 && len(args) != 2 {
		fmt.Fprintln(stderr, allowanceFloorUsage)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path := filepath.Join(h.Root, "config", "fleet.json")
	if len(args) == 2 {
		provider := args[0]
		percent, err := strconv.ParseFloat(args[1], 64)
		switch {
		case !slices.Contains(fleetconfig.WeeklyFloorProviders, provider):
			fmt.Fprintf(stderr, "cfo allowance-floor: %q is not a provider whose week the fleet measures, which are %s\n", provider, strings.Join(fleetconfig.WeeklyFloorProviders, " and "))
			return 2
		case err != nil || !(percent >= 0 && percent < 100):
			fmt.Fprintf(stderr, "cfo allowance-floor: %q is not a percent of at least 0 and under 100\n", args[1])
			return 2
		// The floor is the Overlord's setting, which the CFO changes on his
		// word, never a goblin or a gate agent.
		case os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "":
			fmt.Fprintln(stderr, "cfo allowance-floor: the weekly floor is the Overlord's setting, which the CFO changes on his word, never a goblin or a gate agent")
			return 2
		}
		if err := fleetconfig.SetWeeklyFloor(h.Root, provider, percent); err != nil {
			fmt.Fprintf(stderr, "cfo allowance-floor: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %v percent, set in %s. cfo spawn reads it at once, and the supervisor at its next fleet reading, within a minute\n", provider, percent, path)
		return 0
	}
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		fmt.Fprintf(stderr, "cfo allowance-floor: %v\n", err)
		return 1
	}
	for _, provider := range fleetconfig.WeeklyFloorProviders {
		floor := settings.WeeklyFloor(provider)
		source := "the default"
		if _, isSet := settings.WeeklyFloorPercent[provider]; isSet {
			source = "set in " + path
		}
		meaning := fmt.Sprintf("goblins pause with %v percent of its week left", floor)
		if floor == 0 {
			meaning = "goblins run on its week until " + provider + " refuses"
		}
		fmt.Fprintf(stdout, "%s: %v percent, %s: %s\n", provider, floor, source, meaning)
	}
	return 0
}
