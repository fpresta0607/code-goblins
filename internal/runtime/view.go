package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// RenderJSON writes the complete typed report.
func RenderJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}

// RenderMarkdown writes the human report from an already-built value. Like
// fleet-view's renderer it has no dependency other than the typed value it is
// given: it reads no files, runs no commands and consults no clock.
//
// The five sections are in the order a dispatch decision needs them: what is
// running and whose it is, what is listening and whether it is safe to stop,
// what the machine has left, where the work lands, and how to run it here.
func RenderMarkdown(w io.Writer, report Report) error {
	out := &writer{w: w}
	out.line("# Local Runtime")
	out.line("")
	out.line("Schema: " + dash(report.Schema))
	out.line("Home: " + dash(report.Home))
	renderNotes(out, report.Notes)

	renderContainers(out, report)
	renderServers(out, report)
	renderHeadroom(out, report)
	renderDeployments(out, report)
	renderLocal(out, report)
	return out.err
}

// renderNotes prints every source that could not be read, before anything
// else. A reader who does not know a section is blind will read it as empty,
// and empty is the answer that says there is room to dispatch.
func renderNotes(out *writer, notes []string) {
	if len(notes) == 0 {
		return
	}
	out.line("")
	out.line("## Degraded")
	out.line("")
	for _, note := range notes {
		out.line("- " + text(note))
	}
}

func renderContainers(out *writer, report Report) {
	out.line("")
	out.line("## Containers")
	if len(report.Stacks) == 0 {
		out.line("")
		out.line("No containers found.")
	}
	for _, stack := range report.Stacks {
		out.line("")
		out.line("### " + text(stack.Owner.Label()) + " - " + text(stack.Name))
		out.line("")
		out.line("Evidence: " + text(stack.Owner.Evidence))
		if stack.Owner.Reap {
			out.line("Retire with: `cfo reap` (it already refuses to kill what is still working)")
		}
		out.line("")
		out.line("| Container | Image | State | Since | Restarts | Ports |")
		out.line("| --- | --- | --- | --- | --- | --- |")
		for _, container := range stack.Containers {
			state := container.State
			if container.RestartLooping() {
				state += " **RESTART LOOP**"
			}
			out.row(
				container.Name,
				Truncate(container.Image, 44),
				state,
				stamp(container.Since),
				strconv.Itoa(container.RestartCount),
				strings.Join(container.Ports, ", "),
			)
		}
		for _, container := range stack.Looping() {
			out.line("")
			out.line(fmt.Sprintf("- RESTART LOOP: %s has restarted %d times and is not staying up; nothing on this machine is consuming what it produces.",
				text(container.Name), container.RestartCount))
		}
	}
	renderLooseVolumes(out, report.LooseVolumes)
}

func renderLooseVolumes(out *writer, volumes []LooseVolume) {
	out.line("")
	out.line("### Volumes nothing references")
	out.line("")
	if len(volumes) == 0 {
		out.line("None: every named volume is mounted by a container.")
		return
	}
	var total int64
	out.line("| Volume | Size | Owner | Evidence |")
	out.line("| --- | --- | --- | --- |")
	for _, volume := range volumes {
		total += volume.Bytes
		out.row(volume.Name, Bytes(volume.Bytes), volume.Owner.Label(), volume.Owner.Evidence)
	}
	out.line("")
	out.line(fmt.Sprintf("%d volumes, %s, mounted by nothing. This command does not remove them.", len(volumes), Bytes(total)))
}

func renderServers(out *writer, report Report) {
	out.line("")
	out.line("## Local servers")
	out.line("")
	if len(report.Servers) == 0 {
		out.line("No listening servers found.")
		renderFolded(out, report.Folded)
		return
	}
	out.line("| Port | PID | Process | Directory | Kind | Task | Safe to stop |")
	out.line("| --- | --- | --- | --- | --- | --- | --- |")
	for _, server := range report.Servers {
		out.row(
			strconv.Itoa(server.Port),
			strconv.Itoa(server.PID),
			server.Process,
			Truncate(server.WorkDir, 60),
			string(server.Directory),
			server.Owner.TaskID,
			string(server.Stop),
		)
	}
	out.line("")
	for _, server := range report.Servers {
		out.line(fmt.Sprintf("- %d (pid %d): %s - %s",
			server.Port, server.PID, text(string(server.Stop)), text(server.StopReason())))
	}
	if safe := countSafe(report.Servers); safe > 0 {
		out.line("")
		out.line(fmt.Sprintf("%d server(s) are safe to stop. `cfo reap` owns retiring them; this command only reports.", safe))
	}
	renderFolded(out, report.Folded)
}

// renderFolded accounts for every listener the table left out. Naming the
// ports matters: a reader who cannot find the port they came for needs to see
// that it was folded and why, not conclude that nothing is listening on it.
func renderFolded(out *writer, folded []Folded) {
	if len(folded) == 0 {
		return
	}
	out.line("")
	out.line("Not listed above:")
	for _, group := range folded {
		ports := make([]string, 0, len(group.Ports))
		for _, port := range group.Ports {
			ports = append(ports, strconv.Itoa(port))
		}
		out.line(fmt.Sprintf("- %d port(s) %s: %s", len(group.Ports), text(group.Reason), strings.Join(ports, ", ")))
	}
}

func countSafe(servers []Server) int {
	count := 0
	for _, server := range servers {
		if server.Stop == StopSafe {
			count++
		}
	}
	return count
}

func renderHeadroom(out *writer, report Report) {
	machine := report.Headroom
	out.line("")
	out.line("## Machine headroom")
	out.line("")
	out.line("| Resource | Total | Available | In use by Docker |")
	out.line("| --- | --- | --- | --- |")
	out.row("memory", Bytes(machine.MemoryTotal), Bytes(machine.MemoryAvailable)+" ("+Percent(machine.MemoryAvailable, machine.MemoryTotal)+")", "see WSL below")
	out.row(dash(machine.DiskName)+" disk", Bytes(machine.DiskTotal), Bytes(machine.DiskFree)+" ("+Percent(machine.DiskFree, machine.DiskTotal)+")", Bytes(machine.DockerTotal()))
	out.line("")
	out.line("Docker on disk: images " + Bytes(machine.DockerImages) +
		", containers " + Bytes(machine.DockerContainers) +
		", volumes " + Bytes(machine.DockerVolumes) +
		", build cache " + Bytes(machine.DockerBuildCache) +
		"; " + Bytes(machine.DockerReclaimable) + " of that is reclaimable.")
	out.line("")
	out.line("WSL virtual machine: " + Bytes(machine.WSL) + " of memory (" + Percent(machine.WSL, machine.MemoryTotal) +
		" of the machine). Every container runs inside it, so this is the figure that starves the fleet without any Windows-side process appearing to grow.")

	out.line("")
	out.line("### Declared memory limits of running stacks")
	out.line("")
	if len(report.Limits) == 0 {
		out.line("No stack is running.")
		return
	}
	out.line("| Stack | Owner | Declared limit | Running | Undeclared |")
	out.line("| --- | --- | --- | --- | --- |")
	var declared int
	for _, limit := range report.Limits {
		value := Bytes(limit.Bytes)
		if limit.Bytes == 0 {
			value = "none"
		} else {
			declared++
		}
		out.row(limit.Stack, limit.Owner, value, strconv.Itoa(limit.Containers), strconv.Itoa(limit.Undeclared))
	}
	if declared == 0 {
		out.line("")
		out.line("No running stack declares a memory limit, so none of them has a ceiling: what they will take is bounded only by the machine. Treat available memory above, not these rows, as the dispatch budget.")
	}
}

func renderDeployments(out *writer, report Report) {
	out.line("")
	out.line("## Deployed where")
	out.line("")
	printed := false
	for _, project := range report.Projects {
		if len(project.Targets) == 0 {
			continue
		}
		printed = true
		out.line("### " + text(project.Name))
		out.line("")
		out.line("| Provider | Target | Read from |")
		out.line("| --- | --- | --- |")
		for _, target := range project.Targets {
			detail := target.Detail
			if detail == "" {
				detail = "declared, no address in any manifest"
			}
			out.row(target.Provider, detail, target.Source)
		}
		for _, target := range project.Targets {
			if target.Note != "" {
				out.line("")
				out.line("- " + text(target.Provider) + ": " + text(target.Note))
			}
		}
		out.line("")
	}
	if !printed {
		out.line("No project declares a deploy target.")
	}
}

func renderLocal(out *writer, report Report) {
	out.line("")
	out.line("## Local testing")
	out.line("")
	out.line("| Project | Up | Down | Source | Declared |")
	out.line("| --- | --- | --- | --- | --- |")
	printed := false
	for _, project := range report.Projects {
		if len(project.Local.Up) == 0 {
			continue
		}
		printed = true
		out.row(
			project.Name,
			strings.Join(project.Local.Up, " && "),
			strings.Join(project.Local.Down, " && "),
			project.Local.Source,
			boolText(project.Local.Declared),
		)
	}
	if !printed {
		out.line("| - | - | - | - | - |")
		out.line("")
		out.line("No project declares or implies a local stack.")
		return
	}
	out.line("")
	out.line("Declared means the project's own worktree manifest named the commands. The rest are derived from the file in the source column, and a project whose real command is neither should declare a `local` block there.")
}

// writer accumulates the first write error rather than checking each line, so
// the renderer above reads as the document it produces.
type writer struct {
	w   io.Writer
	err error
}

func (t *writer) line(value string) {
	if t.err != nil {
		return
	}
	_, t.err = fmt.Fprintln(t.w, value)
}

// row renders one Markdown table row, escaping each cell.
func (t *writer) row(values ...string) {
	cells := make([]string, len(values))
	for index, value := range values {
		cells[index] = strings.ReplaceAll(text(dash(value)), "|", `\|`)
	}
	t.line("| " + strings.Join(cells, " | ") + " |")
}

// text keeps persisted values visible without letting terminal control
// sequences into the Markdown projection, exactly as fleet-view's renderer
// does: a container label or a command line is somebody else's string.
func text(value string) string {
	var out strings.Builder
	for _, char := range value {
		if unicode.IsControl(char) {
			fmt.Fprintf(&out, "\\u%04X", char)
			continue
		}
		out.WriteRune(char)
	}
	return out.String()
}

func stamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func dash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
