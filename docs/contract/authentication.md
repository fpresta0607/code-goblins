## Project authentication

Every project declares what it needs to authenticate in `data/projects/<name>/auth.json`.
The manifest holds names, probes, and links - never a credential.

```json
{
  "project": "acme-api",
  "services": [
    {
      "name": "neon",
      "method": "cli",
      "env": ["DATABASE_URL"],
      "probe": ["neonctl", "projects", "list"],
      "identity": {
        "var": "DATABASE_URL",
        "expect": "ep-acme-api",
        "note": "DATABASE_URL points at this project's Neon branch"
      },
      "login": ["neonctl", "auth"],
      "url": "https://console.neon.tech",
      "optional": false,
      "note": "serverless Postgres"
    },
    {
      "name": "github",
      "method": "cli",
      "env": ["GITHUB_TOKEN"],
      "shared": true,
      "probe": ["gh", "auth", "status"]
    }
  ]
}
```

- `method` is `env` (the variable *is* the credential), `cli` (the tool holds its own login and the variable is what makes direct API access possible), or `oauth` (a browser handshake).
- `env` names are credential names, resolved inside this project's scope.
- `shared: true` lets a service fall back to the store's shared scope.
  It is opt-in, so a credential that differs per project can never be answered from a scope that cannot say whose it is.
- `aliases` maps a declared name to the stored names that may satisfy it, tried after the declared name: `"aliases": {"FLY_API_TOKEN": ["FLY_PROD_API_TOKEN"]}`.
  Nothing is ever matched by resemblance.
- `probe` is a cheap command that exits zero only when the service genuinely answers.
  A `$NAME` in it is substituted from the resolved credential.
  A probe proves the transport, never the target.
- `identity` proves the target.
  Declare exactly one of `var` (a resolved variable whose value must contain `expect`, which needs no tool installed) or `command` (a command whose output must contain `expect`).
  A service with no `identity` stays liveness-only and the report says so.
- `login` is a non-interactive command `--fix` may run; `url` and `confirm` are what the browser fallback and the sign-in request use.
- `optional: true` keeps a service a project can run without out of the blocking column.
- `default: true` marks a service for [default credential selection](#credentials-by-need).
  Mark only a service nearly every task of the project needs and none can spend with.
- A service's `name` is what a brief writes to ask for it, so it holds no comma, starts and ends with no space, and is not `none`.
- `formats`, beside `services`, is an optional list of hints for what credential values look like, which a credential request's card warns with and never refuses a value for (see [Credential requests](../native-board.md#credential-requests)).
- `logins`, beside `services`, names the browser logins the project's goblins fetch keys with, at most one for each provider: `{"provider": "stripe", "account": "PrecisionDocs", "account_id": "acct_1ABC"}`.
  Projects that name the same provider and account share one login, so the Overlord signs in once for them all, and `account_id` is the account a fetch reads keys on.
  A project that names none uses a login of its own (see [Browser key fetching](../native-board.md#browser-key-fetching)).
- Unknown fields are refused.
  `"shared": true` sat in two manifests doing nothing for as long as there was no field to receive it, so a manifest that does not mean what it says now fails to load instead of failing at the incident it causes.

### Credentials by need

A goblin's terminal carries the credentials of the services its task names, and of no other.
Any command a goblin starts can read what its terminal carries and spend with it, so a task that needs no service carries none.

- A brief names its services on one line under its Authentication heading, by their names in the project's manifest: `credentials: github, stripe`, or `credentials: none`.
  `cfo brief` writes the line as `credentials: none` and lists the manifest's services beside it, so a task carries a service only once you name it.
  A brief names a service, never a value.
- `cfo spawn` reads that line.
  A name the manifest does not declare refuses the spawn and says what the manifest does declare, and `--yolo` does not clear it.
- Every new task, including a helper, must state its Authentication choices under the [brief readiness contract](../commands/brief.md).
- Briefs generated from backlog rows or root issues explicitly record the manifest's `"default": true` service names, or `credentials: none`, and `mcp: none` when written.
  A later change to the manifest's defaults does not change those brief choices.
- Only the services a task carries are probed, and only one of those that is red refuses its spawn.
  A service that declares a variable one of them declares is checked with them, because an identity verdict is about the value.
- The spawn says what it withheld on a line of its own, such as ``auth: withheld 2 of acme-api's services (neon, stripe), grant one with `cfo auth grant <id> <service>` ``, and its instruction tells the goblin which services it carries.
- The task's record names its services (`credentials=` in `state/<id>.meta`, names only), and a resume, a switch and the comeback after a restart give the new terminal exactly those.
  A change to the brief or to the manifest's defaults after the spawn changes nothing for a running task.
- Which services a task carries is shown by name, never by value: in the Credentials column of `cfo fleet-view` (and `credentials` in its JSON), and under Workspace in the goblin's panel on the board.
  A task an older build started reads `all stored (older build)` until its next terminal.
  Under Connections in that panel a service the task does not carry, and each of its credentials, reads Withheld with the `cfo auth grant` that gives it, is not probed, and offers nothing to store.
- `cfo auth grant <id> <service>` is the one way a task comes by a service after its spawn, and it is yours to run.
  A goblin that needs one says so in a blocked report that names the service and why, and never asks for a value.
  The command is refused in a goblin's terminal and in a gate agent's.
- `cfo auth revoke <id> <service>` takes a service back, as when you granted it for one step of the task, and it is yours to run, refused where a grant is.
  The task's record no longer names it, so no later terminal of the task carries it, and its `auth.ps1` is rewritten without it.
  A terminal running then keeps what it already holds of the service until it ends, since nothing takes a variable back out of a running process: `cfo switch <id> --restart` starts one without it.
  A service the task does not carry is refused and nothing changes.
- A helper narrows what its parent carries and never widens it, before its preflight and its terminal.
  Its explicit `credentials:` and `mcp:` lines select subsets of the services and value-bearing MCP servers the parent carries: `credentials: none` and `mcp: none` give a read-only reviewer nothing.
  A name the parent does not carry refuses the start, saying what the parent carries, rather than widening it.
  The narrowed set is the helper's record from then on, so a resume, a switch and a credential refresh keep it without reading the parent again.
  A later grant or revoke changes the parent's record alone.
- A name stored in a project's scope that no service declares reaches no terminal.
  The spawn names it, and declaring a service for it in the manifest is what lets a brief name it.
  Taking a service out of a manifest therefore takes its credentials away from every later terminal and every later refresh.
- A token-authenticated MCP server whose variable the task does not carry is withheld from it, on the `mcp:` line the spawn already prints.
- An MCP server whose entry in the project's `.mcp.json` holds a value reaches a goblin by need, as a stored credential does.
  An entry holds a value when anything in a command server's `env` map or in a header is more than a reference to one variable: `${NAME}`, alone or after `Bearer`, holds nothing, and any other text or number does.
  Such a server is withheld unless the brief names it on an `mcp:` line beside `credentials:`, such as `mcp: supabase, linear`, or `mcp: none`.
  `cfo brief` writes the line as `mcp: none` and lists the servers it could name.
- A server whose entry holds no value passes as before, and a server that authenticates by `bearerTokenEnvVar` stays under the `credentials:` line, since it reaches a goblin only when its terminal carries that variable.
- The spawn names each server it withheld so and the line that grants one, such as ``mcp: withheld 2 servers whose entry holds a value (linear, supabase), grant one with `cfo auth grant <id> --mcp <server>` ``, and its instruction tells the goblin the same.
  A name the project's `.mcp.json` does not define refuses the spawn and says which names it does define, and `--yolo` does not clear it.
- The task's record names the servers it was given so (`mcp=` in `state/<id>.meta`, names only).
  `cfo auth grant <id> --mcp <server>` adds one and `cfo auth revoke <id> --mcp <server>` takes one out, and both are yours to run.
  A harness reads its MCP servers when it starts, so either reaches the goblin at its next terminal: `cfo switch <id> --restart`.
  Until then a revoked server stays with the harness running.
  A helper is given what its parent's record names when the helper starts, narrowed the same way by its own `mcp:` line.
- Every relaunch, a resume, a switch or the comeback after a restart, plans the task's MCP configuration again from the project's `.mcp.json`, for the servers its record names and the variables its new terminal carries, and writes it once the old harness has stopped.
  So a server granted since reaches the goblin, and one whose entry was given a value since is withheld.
- No delivery mode needs a stored credential.
  A goblin pushes and opens its pull request through `git` and `gh`, which hold their own login on this machine, and no `cfo` command it runs reads one.

A task an older build spawned has a record that names no services, and its terminal already holds everything its project's scope held, which nothing can take back out of a running terminal.
Its next terminal, at a resume, a switch or the comeback after a restart, carries the manifest's default services and no other.
Its record names them from then on, and the relaunch says so in its output and in the task's status log, which is where you read it after an automatic resume.
A relaunch also removes the credential script a refresh wrote for the task's last terminal, `auth.ps1` in its tasktmp folder, which for such a task holds everything its project had stored.
The new terminal starts with what the task carries in its environment, so nothing in the task's folder says more than its record.
A paused task keeps that script until it is relaunched.
`cfo auth refresh <id>`, a grant or a revoke rewrites it at once for what the task carries.
Grant it what it still needs.
Such a task was also handed every MCP server by its entry's shape, and its record names none.
Its next relaunch withholds every server whose entry holds a value: the relaunch names them and the line that grants one in its output, tells the goblin, and writes a `credentials:` record of what it took into the task's status log.
Its running harness keeps the servers it started with until then.

This is a gate on what `cfo` puts into a terminal, not a sandbox.
A goblin runs as the Supreme Overlord's Windows user, so the credential store, a CLI's own login and the task's own record stay within its reach.
What the gate ends is a test run, a gate step or a one-off script spending with a credential its task never named.

### The credential store

Credentials are namespaced on `(project, NAME)`.
`northwind/DATABASE_URL` and `acme-api/DATABASE_URL` are different credentials that cannot alias.
The shared scope is the fallback for a value that genuinely is one value everywhere, and it is where every credential stored before namespacing already lives.

Resolution order, printed under every service by `cfo auth <project> --check`:

1. the process environment, so an operator can override for one command
2.
`store/<project>`
3.
`store/shared`, only for a service the manifest declares `shared`
4. each declared alias, in order, through the same three steps

A shared value the manifest does not claim is reported rather than used, with the `cfo auth copy` command that would claim it.

A bare credential stored before namespacing migrates into the scope that now looks for it, on the ordinary read path rather than as a one-off command, so a goblin dispatched mid-migration still resolves.
The bare value is left where it is until nothing references it.
Migration only claims a name exactly one project's manifest declares: a bare `DATABASE_URL` that two projects declare cannot say whose database it names, so it stays put and the report prints the `cfo auth copy` that would claim it deliberately.

A project's own gitignored `.env` is the one origin allowed to overwrite.
The Supreme Overlord editing that file is how a credential is rotated, so a value that has changed since the last scan read it refreshes the store and the dispatch line names what changed, by name and origin, never by value.
A value that only differs from the store is not a rotation: the store keeps a fingerprint of what each file last offered for each key (its Seen record, beside the credential, never the value and never listed), so a credential stored afterwards with `cfo auth store`, a credential card or a run item stays until that `.env` line is edited.
A file value read for the first time has no history, so it never replaces a stored one either: the dispatch line and `cfo auth --fix` report it as kept, and editing that line rotates it.
Comparing values alone put PrecisionDocs' older `.env` Fly token back over a freshly stored org token on the next dispatch, so the token read unauthorized an hour after it read green.
When more than one env file carries a name, dotenv's own layering decides which one may rotate it: `.env.local` beats `.env.development` beats `.env`, and at equal filename the file nearer the project root beats a nested package's.
When one env file carries both a declared name and a declared alias for the same credential, the manifest breaks that tie: the declared name first, then the alias targets in declared order.
The two rules are not peers - the file decides first, and the manifest's order only settles a tie inside one file - so a dev default in `.env` never outranks a rotation written to `.env.local` under an alias.
Either way one store key is written at most once per run.
A goblin's own worktree is never an origin at all, adoption or refresh: it lives in the home, outside every project, and the scan skips an older build's under a project's `.worktrees/`, because a running agent writes there and only the Supreme Overlord rotates a credential.
Tool-derived origins keep the never-overwrite rule: a token `gh` or `flyctl` happens to hold is not a decision about this project, and letting one rotate under a deliberately stored value is how a stored credential disappears without anyone choosing it.

Worktree provisioning gives a goblin's worktree an env file only when the project's worktree manifest names it in `link`, and then as the worktree's own read-only copy, so nothing written in the worktree reaches the project's own file, and that file stays an origin the store follows while goblins work.
A worktree a build before this one provisioned shares the file as a hard link instead, one file under two names, so a goblin that edits `.env` there edits the Supreme Overlord's real file in place.
A resume or a switch of its task ends each such link and says so: a file the manifest names becomes the worktree's own copy, and one no manifest names is removed from the worktree, with the project's file left where it is.
While such a link lasts, the file is one a live goblin can write, so it is not an origin the store follows at all: adoption and refresh skip any local file whose hard link count is above one, which is the only thing that distinguishes a shared file from a private one when the inode is the same.
The count drops back to one when the link becomes a copy or `cfo cleanup <id>` returns the worktree, so adoption resumes by itself with no command and no state of its own.
While it is paused the dispatch line says so and names the files, because a credential that never rotates would otherwise look exactly like one that had nothing to rotate.
A count that cannot be read at all is skipped the same way and reported as its own cause, since returning a worktree will not fix a file the CFO could not inspect.

Credentials live in Windows Credential Manager, or in `~/.cfo/credentials/` with owner-only ACLs when the vault is unavailable (`CFO_CREDENTIAL_DIR` overrides the location).
They are never written into a repository and never printed - reports show provenance and a redacted shape only.

### Fetching keys with the browser

When a credential request's link is a page a reviewed recipe in `config/key-pages.json` reads, such as Stripe's test key page, the fleet can fetch the keys instead of the Supreme Overlord pasting them.
A goblin runs `cfo auth fetch <request> --task <its id>`, or he presses **Fetch it for me** on the request's card.
The supervisor reads the keys from the page in its own code and stores them straight into the vault, and the goblin hears the names stored and nothing else.
When the page asks for a sign-in, his sign-in card comes to the Command Center, he signs in himself in a window that opens only on his click, and the fetch carries on by itself.
Never sign in to his accounts, never type or read his passwords or codes, and never reveal or read a key in a browser a goblin drives: a key in a transcript is a key to roll.
A goblin's own hooks refuse its browser commands while a fetch reads in its browser, and a Claude Code goblin's wake you when any of its tool output holds something shaped like a secret key, which a Codex or pi goblin's do not yet (#1366).
That wake names the goblin, the tool and the kind of key, and the key is to be rolled if it is real.
A provider with no recipe gets one by pull request: its hosts, key page and signed-in pattern first, then the keys it reads, which a Claude Code goblin finds by borrowing the signed-in browser while they are masked.
His first real fetch is his own, after the install.

### A browser step only he can take

When a goblin's browser or computer-use work reaches a step only the Supreme Overlord can take, such as creating an account, a sign-in, a CAPTCHA, accepting terms, an identity check or a token to copy, it sends him one Command Center card and waits on it.
It never takes such a step itself, and never sends it as a chat message, a notify or a Scrawl walkthrough.
The card is `cfo auth request --project <p> --task <its id> --why "<what it is for>" --link <the exact https page> [--step "<a click he makes>"]... [--plain NAME --label NAME="<a few words>"]... [SECRET_NAME]... --wait`.

- Its link opens the exact screen he must act on, in his own browser.
- It asks only for what he must enter, each input labeled in a few words.
  A secret goes straight into the vault and never to the goblin, and a `--plain` value, such as an account's email, comes back to the goblin as `NAME=value`.
  A plain field refuses anything shaped like a key, except a value of at most 20 letters and digits in a field named as an ID (`NAME_ID`), such as an OAuth client ID; for the rest the card offers "It is not a secret: send it anyway".
- A step that is a click is a `--step`, as few as the step needs.
  A card that asks for nothing has a Done button.
- A message he sends himself, such as an email, goes in a file named with `--message-file`, never as a step that points at the file.
  His card shows its body with Copy, and Copy subject when it has a subject.
- When the step must happen inside the goblin's own browser session, such as a sign-in the session keeps, `--window` puts a button on the card that shows the session's window at the link on his screen, on a profile in the task's folder.
  Once the card closes the session runs headless again on that profile, so it keeps what he did there.

The goblin runs the command in the background, so it goes on the moment the card closes: when every input is in, when he presses Done, or when the goblin sees the step done itself and runs `cfo auth request --done <card> --task <its id>`.
Exit 3 means he skipped the card, and the goblin goes on without it.
Exit 4 means he answered in his own words instead, printed as `he answered instead: <his words>`, which the goblin acts on as his word.
A goblin takes back a card it no longer needs with `cfo auth request --withdraw <card> --task <its id> --reason "<why>"`, you take back any card with `--withdraw` and no `--task`, and a goblin's open cards are withdrawn once it is gone, replaced, or reports done or failed.
`--wait-on <card>` waits on a card again after its wait was cut short.
Every local file or folder path a card's text names, such as a notes file in a step, is a link.
In the desktop window it opens File Explorer at that folder, with the file selected, through the supervisor, which opens only an existing path on a drive of this PC that the card's own text names.
In a plain browser, where that cannot work, the link copies the path instead, and its folder turns into a check once it has.
Every Command Center card has a way out beside its decision buttons.
A question, a review with an answer box, or a Scrawl review card, which has no box for words since he answers on its page, has Skip.
Every other card, a command, a wait with no page, or a credential or step card, has Skip, which tells its goblin to go on without it and wakes you, and a pencil, Something else?, that opens a small window whose green Send gives whoever asked his written or dictated answer, with any pictures he pasted named as `Image: <path>` lines.
The card's place then shows one line with the command to give its secret later, `cfo auth store --project <p> <NAME>`, or, for a card with no secret left, `cfo auth request --reopen <card>`, which you or its goblin run to bring it back: the goblin is told to wait on it again.

### Status words are earned

| State | What it establishes | Blocking |
| --- | --- | --- |
| `green` | resolved, and everything the manifest declared as checkable passed | no |
| `missing` | the credential is nowhere the manifest allows this project to look | yes |
| `wrong_target` | the credential works and points at somebody else's instance | yes |
| `unauthorized` | the service answered and rejected the credential | yes |
| `expired` | the service said the credential expired; never printed on weaker evidence | yes |
| `unreachable` | nothing answered: refused connection, unresolvable host, timeout | yes |
| `failed` | the check failed and did not say why | yes |
| `unverified` | resolved, but the probe tool is absent or the check could not run | no |
| `skipped` | an optional service is unconfigured, which is a choice | no |

A probe, identity check or login that runs past its 20 s limit is ended with everything it started, the CLI behind an npm `.cmd` shim such as `vercel.cmd` included, and when it printed anything first its `unreachable` detail quotes that line, which separates a tool that answered and then did not exit from one that never answered.

Before asking the Supreme Overlord for anything, run `cfo auth <project> --fix`: it adopts what the machine already holds (a project's gitignored local `.env`, the token `gh` already owns, the token `flyctl` already holds) into that project's scope rather than asking twice.
Ask once, with the consolidated sign-in request that command prints, instead of letting goblins fail one credential at a time.

Never ask for a secret value in chat and never put one in a brief.
When a value is genuinely missing, file `cfo auth request --project <p> [--task <id>] --why "<what it is for>" [--link <url>] [--step "<text>"]...
NAME [NAME...]`, with the page to get the values from and a `--step` for each thing he does there: the Supreme Overlord pastes each value on the request's card in the Command Center, or types it in the terminal the card opens, and you are woken with the names stored, never the value.
If he pastes a secret into chat anyway, tell him to roll it and never repeat it.
Goblins follow the same rule, filing for their own task with `--task <its id>`, and send a browser step only he can take as one card ([A browser step only he can take](authentication.md#a-browser-step-only-he-can-take)).
A request stores a value.
It grants no service: a goblin whose task does not carry the service that declares the name says so in a blocked report, and you grant it with `cfo auth grant`.
