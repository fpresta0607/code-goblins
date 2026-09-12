# Project runtime contracts

Code Goblins keeps secrets in the existing auth store and keeps infrastructure shape in `data/projects/<project>/project.json`. The runtime manifest is strict JSON: unknown fields fail closed, provider strings are intentionally open-ended, and credential values are never valid manifest data.

```json
{
  "project": "precisiondocs",
  "services": [
    {"name":"postgres","kind":"database","provider":"supabase","environment":"production","location":"remote","env":["DATABASE_URL"],"health":["psql","$DATABASE_URL","-c","select 1"]},
    {"name":"redis","kind":"redis","provider":"upstash","location":"remote","env":["REDIS_URL"]},
    {"name":"vectors","kind":"vector-store","provider":"qdrant","location":"remote","env":["QDRANT_URL","QDRANT_API_KEY"]},
    {"name":"frontend","kind":"frontend","provider":"vercel","environment":"production"},
    {"name":"backend","kind":"backend","provider":"fly","environment":"production","depends_on":["postgres","redis","vectors"]}
  ],
  "deployment": {
    "required": true,
    "targets": [
      {"name":"web","provider":"vercel","command":["vercel","--prod"],"verify":["curl","-f","https://app.example.com/api/health"]},
      {"name":"api","provider":"fly","command":["flyctl","deploy"],"verify":["curl","-f","https://api.example.com/health"]}
    ]
  },
  "verification": {
    "fast":[["go","test","./internal/foo/..."]],
    "full":[["go","test","./..."]],
    "changed_only":true,
    "max_fast_seconds":90,
    "max_full_seconds":900
  },
  "security": {
    "mode":"risk",
    "fast":[["gitleaks","detect","--no-git"]],
    "deep":[["govulncheck","./..."]],
    "triggers":["auth","payments","migration","infra"]
  },
  "hygiene":{"supersede_clean":true},
  "routing": {
    "default_lane":"open-builder",
    "escalate_to":"subscription-rescue",
    "lanes": {
      "open-scout":{"harness":"pi","model":"openrouter/your-preset","effort":"low"},
      "open-builder":{"harness":"pi","model":"your-open-model","effort":"medium"},
      "subscription-rescue":{"harness":"codex","effort":"high"}
    }
  },
  "budgets": {
    "scout":{"warn_context_tokens":30000,"compact_context_tokens":50000,"restart_context_tokens":80000,"max_repair_rounds":1},
    "builder":{"warn_context_tokens":50000,"compact_context_tokens":75000,"restart_context_tokens":110000,"max_repair_rounds":2}
  }
}
```

`cfo spawn ... --auto` deterministically classifies the task and selects a configured lane. Explicit `--harness`, `--model`, and `--effort` remain authoritative overrides. Spawn writes `runtime-capsule.md` and `task-capsule.json` under the task temporary directory; both contain names and policy, never secret values.

Verification is tiered. `cfo verify <task> --tier fast` is the default changed-scope gate; `full` is the project regression gate; `deep` is for expensive validation. Every command produces structured timing, exit, scope, task, and commit evidence. `cfo security` follows the project security mode. `cfo deploy` executes every declared target and its verification command, so GitHub CI cannot substitute for a required Vercel/Fly/Railway/custom deployment.

When a direction is rejected, `cfo supersede <task> --reason ...` records a durable supersede event, writes cleanup instructions, and steers the worker to remove unshipped debris before continuing. `cfo hygiene <task>` reports suspicious test debris without deleting it.
