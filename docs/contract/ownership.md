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
| The CFO's own session | [docs/contract/supervision.md](supervision.md#the-cfos-own-session) |
| Reporting surface | [docs/contract/decisions.md](decisions.md#reporting-surface) |
| User decisions on the board | [docs/contract/decisions.md](decisions.md#user-decisions-on-the-board) |
| AFK mode | [docs/contract/afk.md](afk.md#afk-mode) |
| Root issues | [docs/contract/root-issues.md](root-issues.md#root-issues) |
| Escalation | [AGENTS.md](../../AGENTS.md#escalation) |
| Cut from this build | [AGENTS.md](../../AGENTS.md#cut-from-this-build) |
| The CFO home | [docs/contract/home.md](home.md#the-cfo-home) |
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
| `cfo remote on\|status\|off` | [cfo help remote](../commands/remote.md) |
| `cfo install [--projects-root <dir>] [--dev-drive on\|off] [--uninstall]` | [cfo help install](../commands/install.md) |
| `cfo uninstall [--dry-run [--json]] [--yes] [--remove-work] [--remove-tools]` | [cfo help uninstall](../commands/uninstall.md) |
| `cfo home migrate [--apply --plan <digest>] [--memory-from <dir>]` | [cfo help home migrate](../commands/home/migrate.md) |
| `cfo dev-drive [setup \| move --to <folder>]` | [cfo help dev-drive](../commands/dev-drive.md) |
| `cfo defender [--since <time\|day\|length>] [--json]` | [cfo help defender](../commands/defender.md) |
| `cfo home move [--to <dir>] [--apply --plan <digest>]` | [cfo help home move](../commands/home/move.md) |
| `cfo doctor [--fix]` | [cfo help doctor](../commands/doctor.md) |
| `cfo dictation setup` | [cfo help dictation setup](../commands/dictation/setup.md) |
| `cfo auth <project> [--check\|--fix] [--env]` | [cfo help auth](../commands/auth.md) |
| `cfo auth store [--project <p>] <NAME> [value]` | [cfo help auth store](../commands/auth/store.md) |
| `cfo auth request --project <p> [--task <id>] --why "<text>" [--link <url>] [--step "<text>"]... [--message-file <file>] [--env-file <file>] [--plain NAME]... [--label NAME="<words>"]... [--window] [--wait] [NAME...]`, `cfo auth request --wait-on\|--done <card> [--task <id>]`, `cfo auth request --withdraw <card> [--task <id>] --reason "<why>"` | [cfo help auth request](../commands/auth/request.md) |
| `cfo auth browser <provider> --task <id> [--release \| --sign-in]` | [cfo help auth browser](../commands/auth/browser.md) |
| `cfo auth fetch <request> --task <id>` | [cfo help auth fetch](../commands/auth/fetch.md) |
| `cfo auth list [--project <p>]` | [cfo help auth list](../commands/auth/list.md) |
| `cfo auth copy <NAME> --to <project> [--from <project>]` | [cfo help auth copy](../commands/auth/copy.md) |
| `cfo auth grant <task-id> <service> [<service>...]` | [cfo help auth grant](../commands/auth/grant.md) |
| `cfo auth grant <task-id> --mcp <server> [<server>...]` | [cfo help auth grant](../commands/auth/grant.md) |
| `cfo auth grant <task-id> --codex-credits` | [cfo help auth grant](../commands/auth/grant.md) |
| `cfo auth revoke <task-id> --codex-credits` | [cfo help auth revoke](../commands/auth/revoke.md) |
| `cfo auth revoke <task-id> <service> [<service>...]` | [cfo help auth revoke](../commands/auth/revoke.md) |
| `cfo auth revoke <task-id> --mcp <server> [<server>...]` | [cfo help auth revoke](../commands/auth/revoke.md) |
| `cfo auth refresh <task-id>` | [cfo help auth refresh](../commands/auth/refresh.md) |
| `cfo project check <project> [--area <area>] [--json] [--draft <file>]` \| `check --unfiled [--json]` \| `show <project>` \| `init <project>` | [cfo help project](../commands/project.md) |
| `cfo spawn <id> --project <p> --brief <b> [--harness <h>] [--mode <m>] [--model <m>] [--effort <e>] [--class <c>] [--title "<short title>"] [--yolo] [--overlap-ok "<why>"]` | [cfo help spawn](../commands/spawn.md) |
| `cfo title <id> "<short title>"` | [cfo help title](../commands/title.md) |
| `cfo switch <id> [--harness <h>] [--model <m>] [--effort <e>] [--force-dirty] [--restart]` | [cfo help switch](../commands/switch.md) |
| `cfo pipeline config-drift \| config-apply \| migrate <id> \| run <id> [--branch <b>] --intent-file <file> \| respond <id> [--branch <b> \| --run <run>] --action <fix\|approve> [--findings <ids>] [--instructions <text>] [--accept <ids>] [--reason <text>] \| recover <id> [--branch <b> \| --run <run>]` | [cfo help pipeline](../commands/pipeline.md) |
| `cfo gate tests-kept` | [cfo help gate tests-kept](../commands/gate/tests-kept.md) |
| `cfo gate test [--level fast\|affected\|full] [--plan]` | [cfo help gate test](../commands/gate/test.md) |
| `cfo gate prepush [--plan] [--limit <duration>]` | [cfo help gate prepush](../commands/gate/prepush.md) |
| `cfo gate turn [--as <name>] [--wait <duration>] [--memory <size>] [--low-priority] [--detach <log-file>] -- <program> [<argument>...]` | [cfo help gate turn](../commands/gate/turn.md) |
| `cfo gate turns [--json]` | [cfo help gate turns](../commands/gate/turns.md) |
| `cfo ci-runner job \| status \| parity --repo <owner/name>` | [cfo help ci-runner](../commands/ci-runner.md) |
| `cfo send <target> <text>` | [cfo help send](../commands/send.md) |
| `cfo send <target> --key <key>` | [cfo help send](../commands/send.md) |
| `cfo peek <target> [lines]` | [cfo help peek](../commands/peek.md) |
| `cfo fleet-view [--json]` | [cfo help fleet-view](../commands/fleet-view.md) |
| `cfo runtime [--json]` | [cfo help runtime](../commands/runtime.md) |
| `cfo services up <project> --task <id> [--wait <duration>]` \| `down <project> --task <id>` | [cfo help services](../commands/services.md) |
| `cfo tickets <project> [--brief <file>] [--files <paths>] [--json]` | [cfo help tickets](../commands/tickets.md) |
| `cfo tickets <project> --allow-public-tickets` | [cfo help tickets](../commands/tickets.md) |
| `cfo issue file --project <p> --title <t> [--body-file <f>] [--evidence <text>]... [--root <key>]` | [cfo help issue file](../commands/issue/file.md) |
| `cfo brief <id> --project <p> [--kind <kind>] [--mode <m>]` | [cfo help brief](../commands/brief.md) |
| `cfo pr check <id> <url>` | [cfo help pr check](../commands/pr/check.md) |
| `cfo pr merge <url> [--method <m>] [--delete-branch] [--verified "<what verified it>"]` | [cfo help pr merge](../commands/pr/merge.md) |
| `cfo pr train <project>` | [cfo help pr train](../commands/pr/train.md) |
| `cfo afk on [--asked "<his words>"] \| off [--asked "<his words>"] \| status \| report \| log --kind <kind> --what "<what>" --evidence "<evidence>" [--link <url>] [--option "<choice>"]... [--recommend "<choice>"] [--run <command.ps1\|command.sh> [--admin]] \| strike --at <when> --reason "<why>" \| settle --at <when> --how "<what became of it>"` | [cfo help afk](../commands/afk.md) |
| `cfo allowance-floor [<claude\|codex> <percent>]` | [cfo help allowance-floor](../commands/allowance-floor.md) |
| `cfo start-hook accept <task-id> <file> [--fleet]` | [cfo help start-hook accept](../commands/start-hook/accept.md) |
| `cfo merge-local <id>` | [cfo help merge-local](../commands/merge-local.md) |
| `cfo cleanup <id>` | [cfo help cleanup](../commands/cleanup.md) |
| `cfo worktree add <id> <name> [--ref <commit>]` \| `remove <id> <name>` | [cfo help worktree](../commands/worktree.md) |
| `cfo reap [--dry-run] [--apply] [--force <pid\|task-id>]` | [cfo help reap](../commands/reap.md) |
| `cfo register` | [cfo help register](../commands/register.md) |
| `cfo process-plan` | [cfo help process-plan](../commands/process-plan.md) |
| `cfo notify <id> --done --pr <url> \| --blocked "<question>" \| --failed "<reason>" \| --working "<what>" \| --waiting-on <task-id\|overlord\|ci\|deploy\|memory> "<why>" [--lavish <html-file>] [--link <https-url>] [--run <command-file>] [--follows <question-id> \| --separate] [--message-file <file>]` | [cfo help notify](../commands/notify.md) |
| `cfo helper start <parent-id> --brief <file> [--title "<short title>"]` \| `merge <parent-id>` | [cfo help helper](../commands/helper.md) |
| `cfo drain` | [cfo help drain](../commands/drain.md) |
| `cfo session-start` | [cfo help session-start](../commands/session-start.md) |
| `cfo memory retrieve \| routes \| budget \| migration-plan` | [cfo help memory](../commands/memory.md) and [memory contract](memory.md) |
| `cfo hook <name>` | [cfo help hook](../commands/hook.md) |
| `<candidate> update [--recover]` | [cfo help update](../commands/update.md) |
| `goblins update [--check] [--to <tag> [--run <item>]]` | [cfo help update](../commands/update.md) |
| `cfo version` | [cfo help version](../commands/version.md) |

## First Mate trigger map

Reference: [First Mate at the reviewed revision](https://github.com/kunchenguid/firstmate/blob/bb3e6906548b5c03d3212e9f060bafde982e0cf1/AGENTS.md).
Its contract and skill-trigger routing inform this organization; Code Goblins commands and authority remain the owners here.
The [MIT notice](../../LICENSE) retains the upstream copyright and permissions and ships with this contract.
No upstream runtime, Relay, secondmate or terminal-backend behavior is activated.

| Upstream contract or trigger | Code Goblins owner and status |
| --- | --- |
| Shared identity, worker-role exception, authority and custody | Shared core, dispatch and supervision references; existing behavior preserved. |
| Skill trigger index and on-demand procedures | Core links, cfo-* skill descriptions and cfo help contract topics; active in this extraction. |
| Session start, complete digest and recovery interpretation | cfo-supervision and cfo session-start/native hooks retain custody and hook trust; [memory](memory.md) owns warm composition and bounded state. |
| Warm retrieved context, cold archive and authoritative memory tiers | [Memory](memory.md) and cfo memory own source coverage, selective retrieval, budgets and lossless dry runs; active for a verified catalog, with no automatic legacy migration. |
| Required Stow pass and capture receipts | The shared `stow` skill owns tier clocks, independent evidence, pending capture, open records and individually approved pinned offload; [memory](memory.md) owns its data interfaces. |
| Validation supervision and ask-user findings | [Delivery](delivery.md), `cfo-delivery` and docs/pipeline.md own adaptive verification with preserved frozen policy, run custody and CFO decisions; active when the delivery follow-up lands. |
| Project intake and verification selection | [Delivery](delivery.md), `cfo-delivery`, project-check, project records and cfo verify own selection at task intake and scout completion; routing is active, while simple/intensive selection implementation remains with the delivery follow-up. |
| Landing and cleanup | cfo-delivery and cfo-worktrees preserve merge authority and unlanded-work custody. |
| Away supervision and user decisions | cfo-afk, cfo-decisions and step-card preserve the native board and explicit switch authority. |

## Loading and installation

The binary embeds the core, every contract and command reference and each conditional skill entry point.
A fresh installed home receives the core and references from that embedded contract.
An update replaces the installed contract files and the one shared skill source; existing policy files remain the operator's, and [tracked checkout homes keep their own contract](#legacy-checkout-activation).
Codex and Pi discover the shared skills directly; Claude Code follows its junctions to the same files.
Skill entry points read the repository's reference in a source checkout and retrieve the binary's embedded reference with cfo help in an installed home; neither route depends on a relative link from the shared skill directory.
CLAUDE.md points to the same AGENTS.md; no harness gets a policy fork.
Missing files, broken local links, unlisted ownership and failed help reads are verification failures.
See [the load map](../load-map.md) for the harness-specific adapters and constraints.

## Legacy checkout activation

A home that is also a tracked checkout keeps its own AGENTS.md, CLAUDE.md and tracked references when installed or updated.
The next CFO still starts in that home and reads its on-disk contract, even when the new binary contains a newer core.
The new binary's cfo help returns its embedded reference independently of that stale checkout.
Shared skills can update without replacing tracked checkout instructions; their presence alone does not activate the new core.
Workers start in isolated project worktrees, read their launch instruction and brief, and load that worktree's project instructions where their harness supports them.
A worker on this repository receives the contract at the commit its worktree starts from, rather than a dirty contract from the primary checkout.
Existing worktrees and resumed conversations are not rewritten by landing this change.

Activation is a separate operator action after landing: reconcile the tracked checkout with the landed contract and references, preserving local policy changes, then load the new instructions in the intended session.
Do not infer permission to update a dirty checkout, move the home, restart a daemon, migrate a gate or discard work from this extraction.
A release installation and a source checkout update are different activation paths.
