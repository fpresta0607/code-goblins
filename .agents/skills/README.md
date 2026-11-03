# Skills this repository owns

This directory holds the skills Code Goblins itself provides, each tracked once, here and nowhere else.

- `lavish` - the CFO's review surface over the third-party `lavish-axi` CLI.
  It is owned here because it carries the CFO's presentation-only rules; edit it here.
- `stow` - captures session knowledge and open work, curates the shared warm/cold memory map, preserves per-item pinned authority and reports budget and capture exceptions.
- `step-card` - what a goblin does when its browser or computer-use work reaches a step only the Supreme Overlord can take: one Command Center card with the exact page and only his inputs, waited on so it goes on the moment the card closes.
- `project-check` - assesses what the home knows about one project and brings it up to date: its project record, its verification gate, its configs and its connectors.
  The checks live in `cfo project check`, so the skill is the same text for every harness and names no tool of one.
- `smart-routing` - guides the CFO's worker model and reasoning effort choices through the existing lanes, current capabilities, quota and evidence of verified completion.

The cfo-* skills are precise trigger entry points for the extracted operating contract.
`cfo-delivery` owns selection at task intake and scout completion as well as validation, gate findings, publication and landing; its procedure remains in the delivery contract.
The [ownership inventory](../../docs/contract/ownership.md) maps every original section, skill trigger, command reference and staged First Mate follow-up, and owns the [source-checkout and installed-home loading rules](../../docs/contract/ownership.md#loading-and-installation).
Their policy bodies stay in `docs/contract/`, which installation carries into a home, rather than being duplicated per harness or relative to a shared skill directory.

## Which harness reads this directory

- **Codex and Pi** read `.agents/skills/` in the project directly.
- **Claude Code** discovers the installed skills through the user-scope junctions described in the [load map](../../docs/load-map.md#claude-code-the-cfo-and-claude-goblins).

A skill with the same name at user scope competes with the copy here: Claude Code uses the user copy, Codex lists both, and Pi uses this one.
Keep one copy of each name per machine.
[docs/load-map.md](../../docs/load-map.md) has the full load order for every harness.

## Third-party skills

The tools the fleet drives publish their own skills.
Install them once at user scope from their owners instead of copying them here; both installs in [docs/install.md](../../docs/install.md) do this for you.
