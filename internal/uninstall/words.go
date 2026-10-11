package uninstall

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// The uninstall's words. Each says what will happen or what happened in the
// fewest words that do, with no semicolon and no dash between clauses.

// Size is bytes as a person reads them, such as 14.2 GB.
func Size(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", bytes)
}

// itemsWhere are the plan's things that action and test take.
func (p Plan) itemsWhere(action Action, test func(Item) bool) []Item {
	var items []Item
	for _, item := range p.Items {
		if item.Action == action && test(item) {
			items = append(items, item)
		}
	}
	return items
}

func inGroup(groups ...Group) func(Item) bool {
	return func(item Item) bool { return slices.Contains(groups, item.Group) }
}

// isRecord is an agent's record of a conversation.
func isRecord(item Item) bool { return strings.Contains(item.What, "record of") }

// WriteSummary writes the plan as the person reads it before the yes: what it
// removes, what it keeps and where, and what it leaves.
func (p Plan) WriteSummary(out io.Writer) {
	line := func(label, detail string) { fmt.Fprintf(out, "  %-24s %s\n", label, detail) }
	fmt.Fprintln(out, "Uninstall Code Goblins")
	fmt.Fprintln(out)
	removes := p.itemsWhere(Remove, func(Item) bool { return true })
	if len(removes) > 0 || p.Count(GroupRunning, Stop) > 0 {
		fmt.Fprintln(out, "It removes")
		for _, item := range p.itemsWhere(Remove, inGroup(GroupHome)) {
			if item.Where == p.Home {
				line("The app and its folder", item.Where+", "+Size(item.Bytes))
			}
		}
		for _, item := range p.itemsWhere(Remove, inGroup(GroupDevDrive)) {
			if strings.HasPrefix(item.What, "the Dev Drive ") {
				line(capital(item.What), item.Where)
			} else if !item.Admin {
				line(capital(item.What), item.Where)
			}
		}
		if n := len(p.itemsWhere(Remove, inGroup(GroupCredentials))); n > 0 {
			line(capital(plural(n, "saved credential")), "in Windows Credential Manager")
		}
		if len(p.itemsWhere(Remove, inGroup(GroupShortcuts))) > 0 || len(p.itemsWhere(Remove, func(item Item) bool { return item.Group == GroupHome && item.Where != p.Home })) > 0 {
			line("Shortcuts and startup", "Start menu, desktop, start at login, Installed apps")
		}
		if len(p.itemsWhere(Remove, func(item Item) bool { return item.Group == GroupHarnesses && !isRecord(item) })) > 0 {
			line("What it added", "hooks, skills and trust in Claude Code, Codex and pi")
		}
		if records := p.itemsWhere(Remove, func(item Item) bool { return item.Group == GroupHarnesses && isRecord(item) }); len(records) > 0 {
			for i, record := range records {
				label := ""
				if i == 0 {
					label = "Conversation records"
				}
				line(label, capital(record.What))
			}
		}
		if worktrees := p.itemsWhere(Remove, inGroup(GroupCheckouts)); len(worktrees) > 0 {
			line("In your checkouts", plural(len(worktrees), "thing")+" it made: worktrees, goblin branches and ignore lines")
		}
		if folders := p.itemsWhere(Remove, inGroup(GroupFolders)); len(folders) > 0 {
			line("Its other folders", plural(len(folders), "folder and file")+", such as "+folders[0].What)
		}
		if running := p.itemsWhere(Stop, inGroup(GroupRunning)); len(running) > 0 {
			line("What runs now", runningWords(running))
		}
		if tools := p.itemsWhere(Remove, inGroup(GroupTools)); len(tools) > 0 {
			line("Tools it installed", toolWords(tools))
		}
		fmt.Fprintln(out)
	}
	if keeps := p.itemsWhere(Keep, func(Item) bool { return true }); len(keeps) > 0 {
		fmt.Fprintln(out, "It keeps, in "+p.Kept)
		for i, item := range p.itemsWhere(Keep, inGroup(GroupWork)) {
			label := ""
			if i == 0 {
				label = "Work not landed"
			}
			line(label, item.What)
		}
		if len(p.itemsWhere(Keep, inGroup(GroupFleetData))) > 0 {
			line("Your fleet data", "briefs, backlog, memory, reports and settings")
		}
		fmt.Fprintln(out)
	}
	if leaves := p.itemsWhere(Leave, func(Item) bool { return true }); len(leaves) > 0 {
		for _, item := range p.itemsWhere(Leave, inGroup(GroupDevDrive)) {
			fmt.Fprintln(out, "It leaves "+item.What+".")
		}
		if tools := p.itemsWhere(Leave, inGroup(GroupTools)); len(tools) > 0 {
			fmt.Fprintln(out, "It leaves the tools it installed, which other programs may use")
			fmt.Fprintln(out, "  "+toolWords(tools))
		}
		fmt.Fprintln(out)
	}
	if len(p.itemsWhere(Remove, func(item Item) bool { return item.Admin })) > 0 {
		fmt.Fprintln(out, "Windows asks once for an administrator to remove the Dev Drive.")
	}
}

// WriteDryRun writes every thing of the plan, each on a line of its own, and
// says nothing was changed.
func (p Plan) WriteDryRun(out io.Writer) {
	fmt.Fprintln(out, "Uninstall Code Goblins: the plan, with nothing changed")
	fmt.Fprintln(out)
	for _, item := range p.Items {
		where := item.Where
		if item.Admin {
			where += ", as administrator"
		}
		if item.Action == Keep {
			where += ", into " + p.Kept
		}
		fmt.Fprintf(out, "%-7s %s: %s\n", capital(string(item.Action)), item.What, where)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Nothing was changed. goblins uninstall makes this plan.")
}

// GroupDone is the line that says what one group did.
func (p Plan) GroupDone(group Group, done []Item) string {
	if len(done) == 0 {
		return ""
	}
	switch group {
	case GroupRunning:
		return "Stopped " + runningWords(done) + "."
	case GroupWork, GroupFleetData:
		return "Kept " + map[Group]string{GroupWork: "the work not landed", GroupFleetData: "your fleet data"}[group] + " in " + p.Kept + "."
	case GroupCheckouts:
		return "Removed its worktrees and goblin records from your checkouts."
	case GroupDevDrive:
		for _, item := range done {
			if strings.HasPrefix(item.What, "the Dev Drive ") {
				return "Removed " + item.What + " and its file " + item.Where + "."
			}
		}
		return "Removed " + done[0].What + "."
	case GroupCredentials:
		return "Removed " + plural(len(done), "saved credential") + "."
	case GroupHarnesses:
		return "Removed what it added to Claude Code, Codex and pi, and their records of the conversations."
	case GroupShortcuts:
		return "Removed the shortcuts, start at login and what the window registered with Windows."
	case GroupFolders:
		return "Removed its other folders, such as " + done[0].What + "."
	case GroupTools:
		return "Removed the tools it installed: " + toolWords(done) + "."
	case GroupHome:
		for _, item := range done {
			if item.Where == p.Home {
				return "Removed " + p.Home + " and its entry in Installed apps."
			}
		}
		return "Removed its entry in Installed apps and its place in your environment."
	}
	return ""
}

// runningWords names the programs to stop, such as the board, the window, the
// CFO and 2 goblins.
func runningWords(items []Item) string {
	var named []string
	goblins, others := 0, 0
	for _, item := range items {
		switch {
		case item.What == "the board" || item.What == "the window":
			if !slices.Contains(named, item.What) {
				named = append(named, item.What)
			}
		case item.What == "the CFO's terminal":
			named = append(named, "the CFO")
		case strings.HasSuffix(item.What, "'s terminal"):
			goblins++
		default:
			others++
		}
	}
	if goblins > 0 {
		named = append(named, plural(goblins, "goblin"))
	}
	if others > 0 {
		named = append(named, plural(others, "program")+" they started")
	}
	return list(named)
}

// toolWords names the tools, the first four by name.
func toolWords(items []Item) string {
	var names []string
	for _, item := range items {
		if !strings.HasPrefix(item.What, "the skill ") && !strings.HasPrefix(item.What, "its PATH entry") && !strings.HasPrefix(item.What, "a folder of the tools") && !slices.Contains(names, item.What) {
			names = append(names, item.What)
		}
	}
	if len(names) > 4 {
		return strings.Join(names[:4], ", ") + " and " + fmt.Sprint(len(names)-4) + " more"
	}
	return list(names)
}

// list joins names as a sentence does: a, b and c.
func list(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
