# Contract ownership

The shared core is [AGENTS.md](../../AGENTS.md).
Every row below names the sole full-text owner of an extracted section or the approved memory extension.
Subsections stay with their enclosing section; the core retains a trigger at every former top-level anchor.
The extraction preserves the existing obligations, including gate budgets, merge authority, hook trust, credentials, wake acknowledgments and custody.

## Section inventory

| Previous section | Current owner |
| --- | --- |
| Prime directives | [AGENTS.md](../../AGENTS.md#prime-directives) |
| The loop: ask away → done | [AGENTS.md](../../AGENTS.md#the-loop-ask-away--done) |
| Dispatching | [docs/contract/dispatch.md](dispatch.md#dispatching) |
| Heavy local runs | [docs/contract/dispatch.md](dispatch.md#heavy-local-runs) |
| Helper goblins | [docs/contract/dispatch.md](dispatch.md#helper-goblins) |
| Goblin names | [docs/contract/dispatch.md](dispatch.md#goblin-names) |
| Naming a project | [docs/contract/dispatch.md](dispatch.md#naming-a-project) |
| Project authentication | [docs/contract/authentication.md](authentication.md#project-authentication) |
| Credentials by need | [docs/contract/authentication.md](authentication.md#credentials-by-need) |
| The credential store | [docs/contract/authentication.md](authentication.md#the-credential-store) |
| Fetching keys with the browser | [docs/contract/authentication.md](authentication.md#fetching-keys-with-the-browser) |
| A browser step only he can take | [docs/contract/authentication.md](authentication.md#a-browser-step-only-he-can-take) |
| Status words are earned | [docs/contract/authentication.md](authentication.md#status-words-are-earned) |
| Project worktree environment | [docs/contract/worktrees.md](worktrees.md#project-worktree-environment) |
| Share caches, never share materialized environments | [docs/contract/worktrees.md](worktrees.md#share-caches-never-share-materialized-environments) |
| A release a week old, never a fresher one | [docs/contract/worktrees.md](worktrees.md#a-release-a-week-old-never-a-fresher-one) |
| Code that runs at every Python start | [docs/contract/worktrees.md](worktrees.md#code-that-runs-at-every-python-start) |
| Project local services | [docs/contract/services.md](services.md#project-local-services) |
| The fleet never idles | [docs/contract/dispatch.md](dispatch.md#the-fleet-never-idles) |
| Pausing and automatic resume | [docs/contract/dispatch.md](dispatch.md#pausing-and-automatic-resume) |
| Switching a running goblin | [docs/contract/dispatch.md](dispatch.md#switching-a-running-goblin) |
| Dispatch policy | [docs/contract/dispatch.md](dispatch.md#dispatch-policy) |
| Model routing | [docs/contract/dispatch.md](dispatch.md#model-routing) |
| Secondmates | [AGENTS.md](../../AGENTS.md#secondmates) |
| Delivery | [docs/contract/delivery.md](delivery.md#delivery) |
| What CI runs | [docs/contract/delivery.md](delivery.md#what-ci-runs) |
| The board's render budget | [docs/contract/delivery.md](delivery.md#the-boards-render-budget) |
| Merge trains | [docs/contract/delivery.md](delivery.md#merge-trains) |
| Supervision | [docs/contract/supervision.md](supervision.md#supervision) |
| The CEO's own session | [docs/contract/supervision.md](supervision.md#the-ceos-own-session) |
| Reporting surface | [docs/contract/decisions.md](decisions.md#reporting-surface) |
| User decisions on the board | [docs/contract/decisions.md](decisions.md#user-decisions-on-the-board) |
| AFK mode | [docs/contract/afk.md](afk.md#afk-mode) |
| Root issues | [docs/contract/root-issues.md](root-issues.md#root-issues) |
| Escalation | [AGENTS.md](../../AGENTS.md#escalation) |
| Cut from this build | [AGENTS.md](../../AGENTS.md#cut-from-this-build) |
| The CEO home | [docs/contract/home.md](home.md#the-ceo-home) |
| The janitor | [docs/contract/home.md](home.md#the-janitor) |
| Microsoft Defender | [AGENTS.md](../../AGENTS.md#microsoft-defender) |
| Memory | [AGENTS.md](../../AGENTS.md#memory) |
| Startup memory and retrieval | [docs/contract/memory.md](memory.md#startup-memory-and-retrieval) |
| Owners and startup | [docs/contract/memory.md](memory.md#owners-and-startup) |
| Catalog schema | [docs/contract/memory.md](memory.md#catalog-schema) |
| Budgets and exceptions | [docs/contract/memory.md](memory.md#budgets-and-exceptions) |
| Lossless planning and pinned offload | [docs/contract/memory.md](memory.md#lossless-planning-and-pinned-offload) |
| Restart is a non-event | [AGENTS.md](../../AGENTS.md#restart-is-a-non-event) |
| Commands | Each complete row is owned by its command reference below. |

## Command inventory

| Previous command | Targeted owner |
| --- | --- |
| `ceo remote on\|status\|off` | [ceo help remote](../commands/remote.md) |
| `ceo install [--projects-root <dir>] [--dev-drive on\|off] [--uninstall]` | [ceo help install](../commands/install.md) |
| `ceo uninstall [--dry-run [--json]] [--yes] [--remove-work] [--remove-tools]` | [ceo help uninstall](../commands/uninstall.md) |
| `ceo home migrate [--apply --plan <digest>] [--memory-from <dir>]` | [ceo help home migrate](../commands/home/migrate.md) |
| `ceo dev-drive [setup \| move --to <folder>]` | [ceo help dev-drive](../commands/dev-drive.md) |
| `ceo defender [--since <time\|day\|length>] [--json]` | [ceo help defender](../commands/defender.md) |
| `ceo home move [--to <dir>] [--apply --plan <digest>]` | [ceo help home move](../commands/home/move.md) |
| `ceo doctor [--fix]` | [ceo help doctor](../commands/doctor.md) |
| `ceo dictation setup` | [ceo help dictation setup](../commands/dictation/setup.md) |
| `ceo crawler setup` | [ceo help crawler setup](../commands/crawler/setup.md) |
| `ceo auth <project> [--check\|--fix] [--env]` | [ceo help auth](../commands/auth.md) |
| `ceo auth store [--project <p>] <NAME> [value]` | [ceo help auth store](../commands/auth/store.md) |
| `ceo auth request --project <p> [--task <id>] --why "<text>" [--link <url>] [--step "<text>"]... [--message-file <file>] [--env-file <file>] [--plain NAME]... [--label NAME="<words>"]... [--window] [--wait] [NAME...]`, `ceo auth request --wait-on\|--done <card> [--task <id>]`, `ceo auth request --withdraw <card> [--task <id>] --reason "<why>"` | [ceo help auth request](../commands/auth/request.md) |
| `ceo auth browser <provider> --task <id> [--release \| --sign-in]` | [ceo help auth browser](../commands/auth/browser.md) |
| `ceo auth fetch <request> --task <id>` | [ceo help auth fetch](../commands/auth/fetch.md) |
| `ceo auth list [--project <p>]` | [ceo help auth list](../commands/auth/list.md) |
| `ceo auth copy <NAME> --to <project> [--from <project>]` | [ceo help auth copy](../commands/auth/copy.md) |
| `ceo auth grant <task-id> <service> [<service>...]` | [ceo help auth grant](../commands/auth/grant.md) |
| `ceo auth grant <task-id> --mcp <server> [<server>...]` | [ceo help auth grant](../commands/auth/grant.md) |
| `ceo auth grant <task-id> --codex-credits` | [ceo help auth grant](../commands/auth/grant.md) |
| `ceo auth revoke <task-id> --codex-credits` | [ceo help auth revoke](../commands/auth/revoke.md) |
| `ceo auth revoke <task-id> <service> [<service>...]` | [ceo help auth revoke](../commands/auth/revoke.md) |
| `ceo auth revoke <task-id> --mcp <server> [<server>...]` | [ceo help auth revoke](../commands/auth/revoke.md) |
| `ceo auth refresh <task-id>` | [ceo help auth refresh](../commands/auth/refresh.md) |
| `ceo project check <project> [--area <area>] [--json] [--draft <file>]` \| `check --unfiled [--json]` \| `show <project>` \| `init <project>` | [ceo help project](../commands/project.md) |
| `ceo spawn <id> --project <p> --brief <b> [--harness <h>] [--mode <m>] [--delivery-exception "<authority and reason>"] [--model <m>] [--effort <e>] [--class <c>] [--title "<short title>"] [--yolo] [--overlap-ok "<why>"]` | [ceo help spawn](../commands/spawn.md) |
| `ceo title <id> "<short title>"` | [ceo help title](../commands/title.md) |
| `ceo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty] [--restart]` | [ceo help switch](../commands/switch.md) |
| `ceo pipeline` | [ceo help pipeline](../commands/pipeline.md) |
| `ceo gate tests-kept` | [ceo help gate tests-kept](../commands/gate/tests-kept.md) |
| `ceo gate test [--level fast\|affected\|full \| --focused] [--plan]`, `ceo gate lint [--plan]` | [ceo help gate test](../commands/gate/test.md) |
| `ceo gate prepush [--plan] [--limit <duration>]` | [ceo help gate prepush](../commands/gate/prepush.md) |
| `ceo gate turn [--as <name>] [--wait <duration>] [--memory <size>] [--low-priority] [--detach <log-file>] -- <program> [<argument>...]` | [ceo help gate turn](../commands/gate/turn.md) |
| `ceo gate turns [--json]` | [ceo help gate turns](../commands/gate/turns.md) |
| `ceo ci-runner job \| status \| parity --repo <owner/name>` | [ceo help ci-runner](../commands/ci-runner.md) |
| `ceo send <target> <text>` | [ceo help send](../commands/send.md) |
| `ceo send <target> --key <key>` | [ceo help send](../commands/send.md) |
| `ceo peek <target> [lines]` | [ceo help peek](../commands/peek.md) |
| `ceo fleet-view [--json]` | [ceo help fleet-view](../commands/fleet-view.md) |
| `ceo runtime [--json]` | [ceo help runtime](../commands/runtime.md) |
| `ceo services up <project> --task <id> [--wait <duration>]` \| `down <project> --task <id>` | [ceo help services](../commands/services.md) |
| `ceo tickets <project> [--brief <file>] [--files <paths>] [--json]` | [ceo help tickets](../commands/tickets.md) |
| `ceo tickets <project> --allow-public-tickets` | [ceo help tickets](../commands/tickets.md) |
| `ceo issue file --project <p> --title <t> [--body-file <f>] [--evidence <text>]... [--root <key>]` | [ceo help issue file](../commands/issue/file.md) |
| `ceo brief <id> --project <p> [--kind <kind>] [--mode <m>] [--surface <s>] [--risk <r>] [--reason "<assessment>"] [--delivery-exception "<authority and reason>"]` | [ceo help brief](../commands/brief.md) |
| `ceo pr check <id> <url>` | [ceo help pr check](../commands/pr/check.md) |
| `ceo pr merge <url> [--method <m>] [--delete-branch] [--verified "<what verified it>"]` | [ceo help pr merge](../commands/pr/merge.md) |
| `ceo pr train <project>` | [ceo help pr train](../commands/pr/train.md) |
| `ceo afk on [--asked "<his words>"] \| off [--asked "<his words>"] \| status \| report \| log --kind <kind> --what "<what>" --evidence "<evidence>" [--link <url>] [--option "<choice>"]... [--recommend "<choice>"] [--run <command.ps1\|command.sh> [--admin]] \| strike --at <when> --reason "<why>" \| settle --at <when> --how "<what became of it>"` | [ceo help afk](../commands/afk.md) |
| `ceo allowance-floor [<claude\|codex> <percent>]` | [ceo help allowance-floor](../commands/allowance-floor.md) |
| `ceo start-hook accept <task-id> <file> [--fleet]` | [ceo help start-hook accept](../commands/start-hook/accept.md) |
| `ceo merge-local <id>` | [ceo help merge-local](../commands/merge-local.md) |
| `ceo cleanup <id>` | [ceo help cleanup](../commands/cleanup.md) |
| `ceo worktree add <id> <name> [--ref <commit>]` \| `remove <id> <name>` | [ceo help worktree](../commands/worktree.md) |
| `ceo reap [--dry-run] [--apply] [--force <pid\|task-id>]` | [ceo help reap](../commands/reap.md) |
| `ceo register` | [ceo help register](../commands/register.md) |
| `ceo process-plan` | [ceo help process-plan](../commands/process-plan.md) |
| `ceo notify <id>` with every report form it takes | [ceo help notify](../commands/notify.md) |
| `ceo helper start <parent-id> --brief <file> [--title "<short title>"]` \| `merge <parent-id>` | [ceo help helper](../commands/helper.md) |
| `ceo drain` | [ceo help drain](../commands/drain.md) |
| `ceo session-start` | [ceo help session-start](../commands/session-start.md) |
| `ceo memory` | [ceo help memory](../commands/memory.md) and [memory contract](memory.md) |
| `ceo hook <name>` | [ceo help hook](../commands/hook.md) |
| `<candidate> update [--recover]` | [ceo help update](../commands/update.md) |
| `goblins update [--check] [--to <tag> [--run <item>]]` | [ceo help update](../commands/update.md) |
| `ceo version` | [ceo help version](../commands/version.md) |

## First Mate trigger map

Reference: [First Mate at the reviewed revision](https://github.com/kunchenguid/firstmate/blob/bb3e6906548b5c03d3212e9f060bafde982e0cf1/AGENTS.md).
Its contract and skill-trigger routing inform this organization; Code Goblins commands and authority remain the owners here.
The [MIT notice](../../LICENSE) retains the upstream copyright and permissions and ships with this contract.
No upstream runtime, Relay, secondmate or terminal-backend behavior is activated.

| Upstream contract or trigger | Code Goblins owner and status |
| --- | --- |
| Shared identity, worker-role exception, authority and custody | Shared core, dispatch and supervision references; existing behavior preserved. |
| Skill trigger index and on-demand procedures | Core links, ceo-* skill descriptions and ceo help contract topics; active in this extraction. |
| Session start, complete digest and recovery interpretation | ceo-supervision and ceo session-start/native hooks retain custody and hook trust; [memory](memory.md) owns warm composition and bounded state. |
| Warm retrieved context, cold archive and authoritative memory tiers | [Memory](memory.md) and ceo memory own source coverage, selective retrieval, budgets and lossless dry runs; active for a verified catalog, with no automatic legacy migration. |
| Required Stow pass and capture receipts | The shared `stow` skill owns tier clocks, independent evidence, pending capture, open records and individually approved pinned offload; [memory](memory.md) owns its data interfaces. |
| Validation supervision and ask-user findings | [Delivery](delivery.md), `ceo-delivery` and docs/pipeline.md own adaptive verification with preserved frozen policy, run custody and CEO decisions; active. |
| Project intake and verification selection | [Delivery](delivery.md), `ceo-delivery`, project-check, project records and ceo verify own selection at task intake and scout completion; routing and delivery selection are active for newly spawned tasks. |
| Landing and cleanup | ceo-delivery and ceo-worktrees preserve merge authority and unlanded-work custody. |
| Away supervision and user decisions | ceo-afk, ceo-decisions and step-card preserve the native board and explicit switch authority. |

## Loading and installation

The binary embeds the core, every contract and command reference and each conditional skill entry point.
A fresh installed home receives the core and references from that embedded contract.
An update replaces the installed contract files and the one shared skill source; existing policy files remain the operator's, and [tracked checkout homes keep their own contract](#legacy-checkout-activation).
Codex and Pi discover the shared skills directly; Claude Code follows its junctions to the same files.
Skill entry points read the repository's reference in a source checkout and retrieve the binary's embedded reference with ceo help in an installed home; neither route depends on a relative link from the shared skill directory.
CLAUDE.md points to the same AGENTS.md; no harness gets a policy fork.
Missing files, broken local links, unlisted ownership and failed help reads are verification failures.
See [the load map](../load-map.md) for the harness-specific adapters and constraints.

## Legacy checkout activation

A home that is also a tracked checkout keeps its own AGENTS.md, CLAUDE.md and tracked references when installed or updated.
The next CEO still starts in that home and reads its on-disk contract, even when the new binary contains a newer core.
The new binary's ceo help returns its embedded reference independently of that stale checkout.
Shared skills can update without replacing tracked checkout instructions; their presence alone does not activate the new core.
Workers start in isolated project worktrees, read their launch instruction and brief, and load that worktree's project instructions where their harness supports them.
A worker on this repository receives the contract at the commit its worktree starts from, rather than a dirty contract from the primary checkout.
Existing worktrees and resumed conversations are not rewritten by landing this change.

Activation is a separate operator action after landing: reconcile the tracked checkout with the landed contract and references, preserving local policy changes, then load the new instructions in the intended session.
Do not infer permission to update a dirty checkout, move the home, restart a daemon, migrate a gate or discard work from this extraction.
A release installation and a source checkout update are different activation paths.
