package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/telemetry"
	"github.com/fpresta0607/code-goblins/internal/voice"
)

// runDoctor prints the tool checks, then a per-harness spawn sanity verdict
// (ok/broken from real --version probes), then the measured speed table from
// the no-mistakes telemetry database when one is available. A broken harness
// is unhealthy: every pipeline attempt on it is wasted time.
func runDoctor(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fix := flags.Bool("fix", false, "install each harness's newest version once it proves it starts, while nothing runs from its install")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	// Installing a harness changes it for the whole machine, which is the
	// CFO's or the Overlord's to do, never a goblin's or a gate agent's.
	if *fix && (os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "") {
		fmt.Fprintln(stderr, "cfo doctor: --fix installs harnesses for the whole machine, which the CFO or the Overlord runs, never a goblin or a gate agent")
		return 2
	}
	checks := doctor.Run()
	for _, c := range checks {
		switch {
		case c.Err != "" && c.Installer:
			fmt.Fprintf(stdout, "INSTALLER_UNAVAILABLE %s %s (install: %s) - only install.ps1 needs it, to add git and gh\n", c.Name, c.Err, c.Hint)
		case c.Err != "" && c.Presentation:
			fmt.Fprintf(stdout, "PRESENTATION_UNAVAILABLE %s %s (requires >=%s; install: %s) - nonvisual work proceeds in plain text\n", c.Name, c.Err, c.Floor, c.Hint)
		case c.Err != "":
			fmt.Fprintf(stdout, "MISSING  %-10s %s (install: %s)\n", c.Name, c.Err, c.Hint)
		case c.Floor != "":
			fmt.Fprintf(stdout, "ok       %-10s %s (floor %s)\n", c.Name, c.Version, c.Floor)
		default:
			fmt.Fprintf(stdout, "ok       %-10s %s\n", c.Name, c.Version)
		}
	}
	healthy := doctor.Healthy(checks)

	probes := doctor.ProbeHarnesses(context.Background())
	for _, p := range probes {
		if p.OK {
			fmt.Fprintf(stdout, "ok       %-10s harness spawn probe: %s\n", p.Name, p.Detail)
		} else {
			fmt.Fprintf(stdout, "broken   %-10s harness spawn probe: %s\n", p.Name, p.Detail)
			healthy = false
		}
	}
	releases, versions := reportHarnessVersions(stdout, probes)

	querier := telemetry.Querier{Commands: execx.OSRunner{}, DBPath: telemetry.DefaultDBPath()}
	rows, note := querier.SpeedTable(context.Background())
	if note != "" {
		fmt.Fprintf(stdout, "telemetry: skipped (%s)\n", note)
	} else {
		fmt.Fprintln(stdout, "telemetry: validation invocation minutes; error/cancelled rows are failure latency, not successful speed")
		fmt.Fprintln(stdout, "  agent     model                    role           step            outcome    count   avg min   max min")
		for _, r := range rows {
			fmt.Fprintf(stdout, "  %-9s %-24s %-14s %-15s %-10s %5d %9.1f %9.1f\n", r.Agent, r.Model, r.Role, r.Step, r.Outcome, r.Count, r.AvgMin, r.MaxMin)
		}
	}
	fmt.Fprintln(stdout, "telemetry: implementation unmeasured (the gate database records validation agents only)")

	reportRouting(stdout)
	reportProjectsRoot(stdout, runtime)
	reportDictation(stdout)
	reportCFOHarness(stdout)
	reportStaleWakes(stdout)
	reportHarnessMap(stdout)
	reportUserHooks(stdout)
	reportPermissions(stdout)
	reportLongPaths(stdout)
	reportDevDrive(stdout, runtime)

	if *fix && !fixHarnesses(stdout, releases, versions) {
		healthy = false
	}

	if !healthy {
		return 1
	}
	return 0
}

// reportHarnessVersions prints each working harness's installed version next
// to the newest its publisher offers, with the command that installs it, and
// the version the Codex desktop app bundles, which updates by itself while
// goblins start the codex on PATH. None of it counts against the health
// verdict: an older harness still runs.
func reportHarnessVersions(stdout io.Writer, probes []doctor.HarnessProbe) (doctor.Releases, []doctor.HarnessVersion) {
	releases, err := doctor.ReleasesFromEnvironment()
	if err != nil {
		fmt.Fprintf(stdout, "version: the harnesses' newest versions are not read: %v\n", err)
	}
	versions := releases.Versions(context.Background(), http.DefaultClient, probes)
	for _, version := range versions {
		switch {
		case version.Problem != "":
			fmt.Fprintf(stdout, "version  %-10s %s installed; the newest could not be read: %s\n", version.Name, version.Installed, version.Problem)
		case version.Update != "":
			fmt.Fprintf(stdout, "version  %-10s %s installed, %s on %s: %s\n", version.Name, version.Installed, version.Newest, version.Source, version.Update)
		default:
			fmt.Fprintf(stdout, "version  %-10s %s installed, the newest on %s\n", version.Name, version.Installed, version.Source)
		}
	}
	program, isInstalled, err := doctor.DesktopCodex()
	switch {
	case err != nil:
		fmt.Fprintf(stdout, "version  %-10s the Codex desktop app's bundled codex.exe could not be found: %v\n", "codex", err)
	case isInstalled:
		probe := doctor.ProbeHarness(context.Background(), "codex", program)
		fmt.Fprintf(stdout, "version  %-10s the Codex desktop app bundles %s at %s; goblins start the codex on PATH\n", "codex", probe.Detail, program)
	}
	return releases, versions
}

// fixHarnesses brings each harness the report found behind up to its newest
// version, one at a time, and says what each came to: staged apart, proved in
// a native terminal of its own and installed; waiting while something runs
// from its install; or kept, where the new version did not prove itself. It
// reports whether none failed.
func fixHarnesses(stdout io.Writer, releases doctor.Releases, versions []doctor.HarnessVersion) bool {
	behind := slices.DeleteFunc(slices.Clone(versions), func(version doctor.HarnessVersion) bool { return version.Update == "" })
	if len(behind) == 0 {
		fmt.Fprintln(stdout, "fix: no harness is behind the newest version its publisher offers, as far as that could be read")
		return true
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintf(stdout, "fix: nothing is installed, the home cannot be resolved: %v\n", err)
		return false
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stdout, "fix: nothing is installed, this program's path cannot be read: %v\n", err)
		return false
	}
	// The proofs' terminals keep their records apart from the fleet's and
	// start in one folder, so a harness asks to trust one folder once.
	proof := filepath.Join(h.State, "harness-proof")
	service := spawn.Service{Harness: harness.DefaultRegistry(), Commands: execx.OSRunner{}, HomeRoot: h.Root, StateDir: proof, HostCommand: []string{self, "host"}}
	fixer := doctor.Fixer{
		Commands:    execx.OSRunner{},
		RunningFrom: doctor.RunningFrom,
		Prove: func(ctx context.Context, name, executable string) error {
			return service.ProveLaunch(ctx, "harness-proof-"+name, harness.Kind(name), executable, filepath.Join(proof, "folder"))
		},
		Probe: func(ctx context.Context, name string) doctor.HarnessProbe {
			path, err := exec.LookPath(name)
			if err != nil {
				return doctor.HarnessProbe{Name: name, Detail: "not found on PATH"}
			}
			return doctor.ProbeHarness(ctx, name, path)
		},
		Stage: filepath.Join(h.State, "harness-stage"),
	}
	isFixed := true
	for _, version := range behind {
		fixed := fixer.Fix(context.Background(), releases, version)
		word := "fix"
		if fixed.IsFailed {
			word, isFixed = "FAILED", false
		}
		fmt.Fprintf(stdout, "%-8s %-10s %s\n", word, fixed.Name, fixed.Outcome)
	}
	return isFixed
}

// reportHarnessMap prints where each harness keeps its configuration and
// skills on this machine, the map cfo install records in the home's
// state\harnesses.json, and flags a harness folder that is missing, a skill
// kept as a real folder in more than one skills folder, and a junction whose
// target is gone. None of it counts against the health verdict: a harness
// this machine does not use has no folder, and the rest is the operator's to
// tidy.
func reportHarnessMap(stdout io.Writer) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stdout, "harnesses: the user's profile folder could not be read: %v\n", err)
		return
	}
	m := harnessmap.Find(os.Getenv, userHome)
	for _, root := range m.Roots {
		state := "present"
		if !root.Present {
			state = "missing"
		}
		fmt.Fprintf(stdout, "harness: %-6s %s (%s, %s); skills in %s\n", root.Harness, root.Path, root.Source, state, root.Skills)
	}
	fmt.Fprintf(stdout, "harness: shared skills in %s, which Codex and Pi read themselves and Claude Code reads through a junction per skill\n", m.SharedSkills)
	for _, problem := range harnessmap.Check(m) {
		fmt.Fprintf(stdout, "harness: %s: %s\n", strings.ToUpper(problem.Kind), problem.Detail)
	}
}

// reportLongPaths says when git's core.longpaths is off: a worktree under the
// home is a few folders deeper than one in a checkout, and a node_modules
// inside it can pass Windows' 260-character limit. cfo's own worktree commands
// turn it on for themselves; a goblin's git commands read the setting. It never
// counts against the health verdict.
func reportLongPaths(stdout io.Writer) {
	result, err := execx.OSRunner{}.Run(context.Background(), execx.Request{Name: "git", Args: []string{"config", "--global", "--get", "core.longpaths"}})
	if err == nil && strings.TrimSpace(string(result.Stdout)) == "true" {
		fmt.Fprintln(stdout, "git: core.longpaths is on")
		return
	}
	fmt.Fprintln(stdout, "git: core.longpaths is off, so a goblin's git can fail on a file deeper than 260 characters in its worktree; turn it on with: git config --global core.longpaths true")
}

// reportDevDrive says whether the home's worktrees, scratch and caches are on
// a Dev Drive, and on one that is absent, untrusted or not attached what
// fixes it. It never counts against the health verdict: a Dev Drive is
// optional, and a machine without one works as it always has.
func reportDevDrive(stdout io.Writer, runtime commandRuntime) {
	h, err := runtime.resolveHome()
	if err != nil {
		return
	}
	m, err := runtime.readDevDrive(context.Background())
	if err != nil {
		fmt.Fprintf(stdout, "dev drive: unreadable (%v)\n", err)
		return
	}
	fmt.Fprintln(stdout, "dev drive: "+devdrive.Describe(m, h).Line)
}

// reportDictation says dictation is ready, naming the speech model and the
// engine it runs on, or names what is missing and the fix. It never counts
// against the health verdict: the first dictation fetches what is missing.
func reportDictation(stdout io.Writer) {
	h, err := home.Resolve()
	if err != nil {
		return
	}
	speech, err := voice.For(h.Root, codegoblins.Voice)
	if err != nil {
		fmt.Fprintf(stdout, "dictation: settings unreadable (%v)\n", err)
		return
	}
	if absent := speech.Absent(); len(absent) > 0 {
		fmt.Fprintf(stdout, "dictation: not set up: %s missing, %d MB, from %s; run `cfo dictation setup`, or the first dictation fetches it\n", partNames(absent), speech.Missing()>>20, speech.Dir)
		return
	}
	fmt.Fprintf(stdout, "dictation: ready: %s %s on %s %s, in %s\n", speech.Settings.Model.Name, speech.Settings.Model.Version, speech.Settings.Engine.Name, speech.Settings.Engine.Version, speech.Dir)
}

// reportProjectsRoot prints where a bare `--project <name>` is looked up, or
// how to record it when nothing is. It never counts against the health
// verdict: every command still takes a path without it.
func reportProjectsRoot(stdout io.Writer, runtime commandRuntime) {
	root := ""
	if runtime.projectsRoot != nil {
		var err error
		if root, err = runtime.projectsRoot(); err != nil {
			fmt.Fprintf(stdout, "projects root: unreadable (%v)\n", err)
			return
		}
	}
	if root == "" {
		fmt.Fprintln(stdout, "projects root: not set, so --project takes a path only; run `cfo install --projects-root <dir>` with the folder that holds your checkouts to use bare names")
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(stdout, "projects root: %s is not a directory; record the right folder with `cfo install --projects-root <dir>`\n", root)
		return
	}
	fmt.Fprintf(stdout, "projects root: %s (--project <name> resolves to a checkout under it)\n", root)
}

// reportCFOHarness prints the harness this home starts the CFO as and what a
// CFO in it gets, from the table the quick start and the board's first-run
// page read, with each thing it goes without on a line of its own. It never
// counts against the health verdict.
func reportCFOHarness(stdout io.Writer) {
	h, err := home.Resolve()
	if err != nil {
		return
	}
	agent, err := cfoHarness(h.State)
	if err != nil {
		fmt.Fprintf(stdout, "cfo harness: unreadable (%v)\n", err)
		return
	}
	capability, _ := supervisor.CFOCapabilityFor(agent)
	fmt.Fprintf(stdout, "cfo harness: %s (%s); %s\n", capability.Name, capability.Note, capability.Registers)
	for _, lack := range capability.Lacks {
		fmt.Fprintf(stdout, "  goes without: %s\n", lack)
	}
}

// reportRouting prints the standing switch policy and the execution lane
// table, because a rule that silently restarts a goblin's harness and the
// model each kind of work is dispatched on should both be visible in the
// same place the operator checks everything else.
func reportRouting(stdout io.Writer) {
	h, err := home.Resolve()
	if err != nil {
		return
	}
	policy, err := routing.Load(h.Data)
	if err != nil {
		fmt.Fprintf(stdout, "routing: unreadable (%v)\n", err)
		return
	}
	if len(policy.Rules) == 0 {
		fmt.Fprintf(stdout, "routing: no standing switch rules (add %s to answer a harness fault automatically)\n", policy.Path)
		fmt.Fprintln(stdout, "  a goblin whose harness starts erroring wakes the CFO undecided; fix it with `cfo switch <id> --harness <h>`")
	} else {
		fmt.Fprintf(stdout, "routing: %d standing switch rule(s) from %s\n", len(policy.Rules), policy.Path)
		for _, rule := range policy.Rules {
			from := rule.Harness
			if from == "" {
				from = "any"
			}
			mode := "recommend"
			if rule.Auto {
				mode = "automatic"
			}
			fmt.Fprintf(stdout, "  %-9s %-11s %-9s %s\n", from, rule.Fault, mode, rule.Command("<id>"))
		}
	}
	table, err := policy.LaneTable()
	if err != nil {
		fmt.Fprintf(stdout, "routing: lane table invalid (%v)\n", err)
		return
	}
	if len(table.Lanes) == 0 {
		fmt.Fprintf(stdout, "routing: no execution lanes (add lanes to %s so `cfo spawn` without --harness can pick a model)\n", policy.Path)
		return
	}
	fmt.Fprintf(stdout, "routing: %d execution lane(s) from %s (default %s, escalate to %s)\n", len(table.Lanes), policy.Path, table.DefaultLane, valueOr(table.EscalateTo, "none"))
	names := make([]string, 0, len(table.Lanes))
	for name := range table.Lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lane := table.Lanes[name]
		fmt.Fprintf(stdout, "  %-11s %-7s %-8s %-7s %s\n", name, lane.Harness, valueOr(lane.Model, "default"), valueOr(lane.Effort, "default"), lane.Note)
	}
}

// reportStaleWakes prints what the monitor counted: the task wakes it raised
// and the stale wakes it held back because the goblin's pane showed work
// running or a failed screen read was read again, by reason. A detector that
// stops seeing then reads as nothing counted rather than as a quiet fleet. It
// never counts against the health verdict.
func reportStaleWakes(stdout io.Writer) {
	h, err := home.Resolve()
	if err != nil {
		return
	}
	tally, err := monitor.ReadTally(h.State)
	if err != nil {
		fmt.Fprintf(stdout, "stale wakes: tally unreadable (%v)\n", err)
		return
	}
	if tally.Since.IsZero() {
		fmt.Fprintln(stdout, "stale wakes: nothing counted yet (the monitor counts once cfo serve or the watcher has scanned)")
		return
	}
	fmt.Fprintf(stdout, "stale wakes since %s: raised and held back because the goblin showed it was working\n", tally.Since.UTC().Format("2006-01-02 15:04 UTC"))
	reasons := make([]string, 0, len(tally.Reasons))
	for reason := range tally.Reasons {
		reasons = append(reasons, string(reason))
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		counted := tally.Reasons[monitor.Reason(reason)]
		line := fmt.Sprintf("  %-20s raised %4d  held back %4d", reason, counted.Raised, counted.Suppressed)
		if counted.Suppressed > 0 {
			line += fmt.Sprintf("  last %s at %s: %s", counted.LastTask, counted.Last.UTC().Format("01-02 15:04"), counted.LastWhy)
		}
		fmt.Fprintln(stdout, line)
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// reportUserHooks says whether the user's Claude Code settings still hold the
// CFO's hooks, which a build before 2026-10 installed there and every Claude
// Code session of the user runs, and names the fix. The CFO's terminal starts
// with its hooks, so they are needed nowhere else. It never counts against
// the health verdict: they do nothing in a session that is not the CFO's own,
// and what they cost is 0.1 s for each Bash call, turn end and start.
func reportUserHooks(stdout io.Writer) {
	settings, err := install.UserSettingsPath()
	if err != nil {
		fmt.Fprintf(stdout, "hooks: Claude Code's settings could not be found (%v)\n", err)
		return
	}
	names, err := install.UserHooks(settings)
	if err != nil {
		fmt.Fprintf(stdout, "hooks: %s unreadable (%v)\n", settings, err)
		return
	}
	if len(names) > 0 {
		fmt.Fprintf(stdout, "hooks: %s still holds %d of the CFO's hooks (%s), so every Claude Code session on this machine starts cfo.exe for them on each Bash call, turn end and start; the CFO's terminal starts with its own, and `cfo install` takes these out, keeping every other hook and backing the file up first\n", settings, len(names), strings.Join(names, ", "))
		return
	}
	fmt.Fprintf(stdout, "hooks: the CFO's terminal starts with its hooks, and %s holds none of them, so no other Claude Code session runs one\n", settings)
}

// reportPermissions says whether the user's Claude Code settings hold the
// allow rules cfo install adds for the commands that file Command Center
// items, names any missing with the fix, and says when auto mode sets them
// all aside. Without them auto mode's classifier can refuse a card, and the
// Overlord sees nothing. It never counts against the health verdict: a Codex
// or pi CFO does not read them, and only the Overlord adds them, with cfo
// install or by hand, because no agent widens its own permissions.
func reportPermissions(stdout io.Writer) {
	settings, err := install.UserSettingsPath()
	if err != nil {
		fmt.Fprintf(stdout, "permissions: Claude Code's settings could not be found (%v)\n", err)
		return
	}
	permissions, err := install.ReadPermissions(settings)
	if err != nil {
		fmt.Fprintf(stdout, "permissions: %s unreadable (%v)\n", settings, err)
		return
	}
	total := len(install.PermissionRules())
	if len(permissions.Missing) > 0 {
		fmt.Fprintf(stdout, "permissions: %d of the %d Command Center allow rules are missing from %s: %s; without them Claude Code's auto mode can refuse the CFO's questions, run items, reviews, presentations, documents and credential requests, and nothing reaches the Command Center; they are the Supreme Overlord's to add, with `cfo install` (it keeps his own rules and backs the file up first) or by hand, and a CFO tells him rather than adding them itself\n", len(permissions.Missing), total, settings, strings.Join(permissions.Missing, ", "))
	} else {
		fmt.Fprintf(stdout, "permissions: the %d Command Center allow rules are in %s\n", total, settings)
	}
	if permissions.ClassifyAllShell {
		fmt.Fprintf(stdout, "permissions: autoMode.classifyAllShell is on in %s, so auto mode sets these rules aside and its classifier judges every item the CFO files\n", settings)
	}
}
