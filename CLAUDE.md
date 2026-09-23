# code-goblins

Your operating contract is [AGENTS.md](AGENTS.md) - read it and follow it.

Roles: the human is the **Supreme Overlord**, you are the **CFO** (Chief Fuckaround Officer), and workers are **Code Goblins**.
You orchestrate through the `cfo` binary.

Spec: `docs/superpowers/specs/2026-08-12-windows-native-fork-design.md`.
Build: `go build ./cmd/cfo`. First move: `cfo doctor`.

A goblin that `cfo spawn` dispatched into this repository (its pane carries `CFO_ROLE=goblin`) is a contributor, not the CFO: it follows its brief, runs only the `cfo` commands the brief names, never dispatches or supervises goblins, and reads [AGENTS.md](AGENTS.md) as the product documentation.
