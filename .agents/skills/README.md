# Bundled skills

These skills ship with the repo so a clone carries the full fleet workflow.
They are synced from the operator's user scope (`%USERPROFILE%\.agents\skills`); edit them there, then refresh the copies here.
The exception is `showcase`, which is owned by this repo (its CLI is `cmd/showcase-axi`); edit it here, never sync it down over a user-scope copy.

## Which harness discovers what

- **kimi** and **pi** read `.agents/skills/` in the project root directly - these directories work as-is.
- **claude** reads `.claude/skills/` and **codex** reads `.codex/skills/`.
  `install.ps1 -Bootstrap` creates those as directory junctions pointing at `.agents/skills/` (one source of truth, no duplicate copies in git).

If you skip `install.ps1`, create the junctions by hand from the repo root:

```powershell
cmd /c mklink /J .claude\skills .agents\skills
cmd /c mklink /J .codex\skills .agents\skills
```

Junctions are the robust Windows choice here.
Git can track symlinks, but checking them out on Windows needs developer mode.
A junction is a reparse point created at clone time instead, so the repo stays a plain, tracked directory tree.

## Refresh command

After updating a skill in user scope, copy it back into the repo:

```powershell
$skills = "no-mistakes","gh-axi","chrome-devtools-axi","maintaining-project-memory","gnhf","supabase","supabase-postgres-best-practices"
foreach ($s in $skills) { Copy-Item -Recurse -Force "$env:USERPROFILE\.agents\skills\$s" ".agents\skills\$s" }
```
