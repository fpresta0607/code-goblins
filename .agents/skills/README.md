# Skills this repository owns

This directory holds the skills Code Goblins itself provides, each tracked once, here and nowhere else.

- `lavish` - the CFO's review surface over the third-party `lavish-axi` CLI.
  It is owned here because it carries the CFO's presentation-only rules; edit it here.
- `stow` - curates the CFO's startup memory (`data/overlord.md`, `data/memory/`, the backlog) with tiered, decaying entries and a cold archive, adapted from First Mate's stow pass.
- `step-card` - what a goblin does when its browser or computer-use work reaches a step only the Supreme Overlord can take: one Command Center card with the exact page and only his inputs, waited on so it goes on the moment the card closes.
- `project-check` - assesses what the home knows about one project and brings it up to date: its project record, its verification gate, its configs and its connectors.
  The checks live in `cfo project check`, so the skill is the same text for every harness and names no tool of one.
- `smart-routing` - guides the CFO's worker model and reasoning effort choices through the existing lanes, current capabilities, quota and evidence of verified completion.

The cfo-* skills are precise trigger entry points for the extracted operating contract.
They retrieve a single procedure through `cfo help contract <topic>`, from the binary's embedded policy source.
The [ownership inventory](../../docs/contract/ownership.md) maps every original section, skill trigger, command reference and staged First Mate follow-up.
Their policy bodies stay in `docs/contract/`, which installation carries into a home, rather than being duplicated per harness or relative to a shared skill directory.

## Which harness reads this directory

- **Codex and Pi** read `.agents/skills/` in the project directly.
- **Claude Code** reads `.claude/skills/`, which `install.cmd -Dev` makes a directory junction to this directory.
  Without `install.cmd -Dev`, create it from the repository root: `cmd /c mklink /J .claude\skills .agents\skills`.

A skill with the same name at user scope competes with the copy here: Claude Code uses the user copy, Codex lists both, and Pi uses this one.
Keep one copy of each name per machine.
[docs/load-map.md](../../docs/load-map.md) has the full load order for every harness.

## Third-party skills

The tools the fleet drives publish their own skills.
Install them once at user scope from their owners instead of copying them here; both installs in [docs/install.md](../../docs/install.md) do this for you.
