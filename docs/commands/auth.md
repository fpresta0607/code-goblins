# cfo auth

`cfo auth <project> [--check\|--fix] [--env]`

Preflight a project's services against its manifest and print one honest line each, plus the resolution order behind every variable it declares.
`--fix` adopts credentials the machine already holds, runs non-interactive CLI logins, and confirms an OAuth page whose browser session is live.
`--env` shows the redacted credentials the terminal of a task that names every declared service would carry, then the shared cache redirects in full - a cache location is a path on this machine, not a secret.
Ends with one consolidated sign-in request covering everything still blocked
