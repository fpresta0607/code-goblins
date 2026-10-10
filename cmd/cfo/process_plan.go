package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/janitor"
)

const processPlanUsage = `usage: cfo process-plan

Say what the fleet's process sweeps would end on this machine now, and end
nothing. It reads the machine's processes and this home's terminals and task
records, and writes nothing into the home.

It lists what the janitor's hourly sweep would end, what it would name for the
CFO and what it would leave, each with the rule that decides it, then what a
cleanup or a relaunch of each task would end beside the task's terminal. A
would-end process that looks like the Overlord's, the CFO terminal's or a
working goblin's is listed first, under Alarms.
`

// processPlan is what the fleet's process sweeps would do now, from readings
// of the machine that ended nothing.
type processPlan struct {
	At time.Time
	// Sweep is the janitor's sweep as it would run now, and Later what a
	// sweep an hour on would end beside it if every goblin that rests stayed
	// at rest and nothing it watches did anything meanwhile.
	Sweep janitor.ProcessPlan
	Later []janitor.ProcessItem
	// Flags names, by process ID, each would-end process of the sweep that
	// looks like somebody else's, with why.
	Flags map[int]string
	Tasks []taskProcessPlan
}

// taskProcessPlan is what a cleanup or a relaunch of one task would end
// beside the task's terminal.
type taskProcessPlan struct {
	ID string
	// HostPID is the host that runs the task's terminal, 0 when none does.
	HostPID int
	Ending  []plannedProcess
	// Err is why the task could not be planned.
	Err string
}

// plannedProcess is one process a task's cleanup or relaunch would end.
type plannedProcess struct {
	PID    int
	Name   string
	Memory uint64
	// By is the evidence that makes it the task's: mark, place or parent.
	By string
	// Flag is why it looks like somebody else's, empty when it does not.
	Flag string
}

func runProcessPlan(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	fs := flag.NewFlagSet("process-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, processPlanUsage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, processPlanUsage)
		return 2
	}
	if runtime.resolveHome == nil || runtime.processPlan == nil {
		fmt.Fprintln(stderr, "cfo process-plan: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan, err := runtime.processPlan(context.Background(), h)
	if err != nil {
		fmt.Fprintf(stderr, "cfo process-plan: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, renderProcessPlan(plan))
	return 0
}

// renderProcessPlan writes a plan as Markdown, alarms first.
func renderProcessPlan(plan processPlan) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# What the process sweeps would end\n\n")
	fmt.Fprintf(&out, "Read at %s from %d processes and %d terminals of this home. Nothing was ended and nothing was written.\n\n", plan.At.UTC().Format("2006-01-02 15:04:05Z"), plan.Sweep.Read, len(plan.Sweep.Owners))

	var alarms []string
	for _, items := range [][]janitor.ProcessItem{plan.Sweep.Ending, plan.Later} {
		for _, item := range items {
			if flag := plan.Flags[item.PID]; flag != "" {
				alarms = append(alarms, fmt.Sprintf("- The sweep would end %s pid %d, %s: %s.", item.Name, item.PID, memoryShown(item.Memory), flag))
			}
		}
	}
	for _, task := range plan.Tasks {
		for _, process := range task.Ending {
			if process.Flag != "" {
				alarms = append(alarms, fmt.Sprintf("- A cleanup or a relaunch of %s would end %s pid %d, %s: %s.", task.ID, process.Name, process.PID, memoryShown(process.Memory), process.Flag))
			}
		}
	}
	out.WriteString("## Alarms\n\n")
	if len(alarms) == 0 {
		out.WriteString("None. No would-end list holds a desktop program, a process of the CFO's terminal, a process of a goblin that works, or a process that is another terminal's.\n\n")
	} else {
		out.WriteString(strings.Join(alarms, "\n") + "\n\n")
	}

	out.WriteString("## The janitor's sweep\n\n")
	writeItems(&out, "Would end now", plan.Sweep.Ending, plan.Flags, true)
	writeItems(&out, "Would end at a sweep an hour on, if each goblin that rests stays at rest and nothing it watches does anything meanwhile", plan.Later, plan.Flags, true)
	writeItems(&out, "Would name for the CFO and leave running", plan.Sweep.Left, nil, true)
	writeItems(&out, "Would leave, by terminal", plan.Sweep.Kept, nil, false)
	if len(plan.Sweep.Notes) > 0 {
		out.WriteString("### Notes\n\n")
		for _, note := range plan.Sweep.Notes {
			fmt.Fprintf(&out, "- %s\n", note)
		}
		out.WriteString("\n")
	}

	out.WriteString("## A cleanup or a relaunch of each task\n\n")
	out.WriteString("Each first ends the task's terminal, which ends what its job holds. The lists below are what it would then end by the task's mark, its folders and its own processes' children. For a task whose terminal runs, the harness and what it started are in the list, since they carry the mark.\n\n")
	if len(plan.Tasks) == 0 {
		out.WriteString("No task is recorded in this home.\n")
	}
	for _, task := range plan.Tasks {
		terminal := "its terminal has no host"
		if task.HostPID != 0 {
			terminal = fmt.Sprintf("its terminal runs, host pid %d", task.HostPID)
		}
		var memory uint64
		for _, process := range task.Ending {
			memory += process.Memory
		}
		fmt.Fprintf(&out, "### %s\n\n%s. It would end %d processes holding %s.\n\n", task.ID, upperFirst(terminal), len(task.Ending), memoryShown(memory))
		if task.Err != "" {
			fmt.Fprintf(&out, "It could not be planned: %s\n\n", task.Err)
			continue
		}
		if len(task.Ending) == 0 {
			continue
		}
		out.WriteString("| pid | name | memory | the task's by | flag |\n| --- | --- | --- | --- | --- |\n")
		for _, process := range task.Ending {
			fmt.Fprintf(&out, "| %d | %s | %s | %s | %s |\n", process.PID, tableText(process.Name), memoryShown(process.Memory), ownedBy(process.By), tableText(process.Flag))
		}
		out.WriteString("\n")
	}
	return out.String()
}

// writeItems writes one list of the sweep's plan as a table, with each
// process's command when withCommand, and says so when the list is empty.
func writeItems(out *strings.Builder, title string, items []janitor.ProcessItem, flags map[int]string, withCommand bool) {
	var memory uint64
	for _, item := range items {
		memory += item.Memory
	}
	fmt.Fprintf(out, "### %s\n\n", title)
	if len(items) == 0 {
		out.WriteString("Nothing.\n\n")
		return
	}
	fmt.Fprintf(out, "%d processes holding %s.\n\n", len(items), memoryShown(memory))
	sorted := append([]janitor.ProcessItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Owner < sorted[j].Owner })
	if withCommand {
		out.WriteString("| pid | name | memory | terminal | rule | flag | command |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	} else {
		out.WriteString("| pid | name | memory | terminal | rule |\n| --- | --- | --- | --- | --- |\n")
	}
	for _, item := range sorted {
		if withCommand {
			fmt.Fprintf(out, "| %d | %s | %s | %s | %s | %s | %s |\n", item.PID, tableText(item.Name), memoryShown(item.Memory), tableText(item.Owner), tableText(item.Why), tableText(flags[item.PID]), tableText(item.Command))
		} else {
			fmt.Fprintf(out, "| %d | %s | %s | %s | %s |\n", item.PID, tableText(item.Name), memoryShown(item.Memory), tableText(item.Owner), tableText(item.Why))
		}
	}
	out.WriteString("\n")
}

// ownedBy is the evidence a task's process is its own by, in words.
func ownedBy(by string) string {
	switch by {
	case "mark":
		return "its terminal's mark"
	case "place":
		return "its place in the task's folders, with no parent of somebody else's running"
	default:
		return "its parent, which is the task's own"
	}
}

// memoryShown is a process's private memory in megabytes.
func memoryShown(bytes uint64) string {
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
}

// tableText is text that cannot break a Markdown table's row.
func tableText(text string) string {
	return strings.NewReplacer("|", `\|`, "\r", " ", "\n", " ").Replace(text)
}
