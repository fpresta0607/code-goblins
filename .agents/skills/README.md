# Skills this repository owns

This directory holds the skills Code Goblins itself provides, each tracked once, here and nowhere else.

- `lavish` - the CFO's review surface over the third-party `lavish-axi` CLI.
  It is owned here because it carries the CFO's presentation-only rules; edit it here.
- `stow` - curates the CFO's startup memory (`data/overlord.md`, `data/learnings.md`, the harness memory index, the backlog) with tiered, decaying entries and a cold archive, adapted from First Mate's stow pass.

## Which harness reads this directory

- **Codex, Pi and Kimi** read `.agents/skills/` in the project directly.
- **Claude Code** reads `.claude/skills/`, which `install.ps1 -Bootstrap` makes a directory junction to this directory.
  Without `install.ps1`, create it from the repository root: `cmd /c mklink /J .claude\skills .agents\skills`.

A skill with the same name at user scope competes with the copy here: Claude Code uses the user copy, Codex lists both, and Pi and Kimi use this one.
Keep one copy of each name per machine.
[docs/load-map.md](../../docs/load-map.md) has the full load order for every harness.

## Third-party skills

The tools the fleet drives publish their own skills.
Install them once at user scope from their owners instead of copying them here; the commands are in the [README's Quick start](../../README.md#quick-start).
