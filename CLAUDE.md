# code-goblins

Windows-native rewrite of First Mate: a single Go binary (`cfo.exe`) replaces the upstream bash script layer.
Roles: the human is the Supreme Overlord, the primary agent is the Chief Fuckaround Officer (CFO), and spawned workers are Code Goblins.

Start here:

- Spec: `docs/superpowers/specs/2026-08-12-windows-native-fork-design.md`
- Active plan: `docs/superpowers/plans/2026-08-12-plan-1-cfo-scaffold-core.md`
- SDD ledger: `.superpowers/sdd/2026-08-12-plan-1-cfo-scaffold-core/progress.md`

The legacy bash tree (`bin/`, `tests/`, `AGENTS.md`, `skills/`, `.agents/`) is upstream reference material for the port.
Do not execute it and do not treat `AGENTS.md` as instructions for this session; each subsystem's scripts are deleted as their Go replacement lands.

Go module: `github.com/fpresta0607/code-goblins`.
Build: `go build ./cmd/cfo`.
Test: `go test ./...`.
Development happens on feature branches (currently `feat/cfo-scaffold`) merged to main with `--no-ff`.
