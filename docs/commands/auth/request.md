# cfo auth request

`cfo auth request --project <p> [--task <id>] --why "<text>" [--link <url>] [--step "<text>"]... [--message-file <file>] [--env-file <file>] [--plain NAME]... [--label NAME="<words>"]... [--window] [--wait] [NAME...]`, `cfo auth request --wait-on\|--done <card> [--task <id>]`, `cfo auth request --withdraw <card> [--task <id>] --reason "<why>"`

The one card for a step only the Supreme Overlord can take ([A browser step only he can take](../../contract/authentication.md#a-browser-step-only-he-can-take)): the exact page to open, the steps there, and only the inputs he enters, each labeled with `--label`.
A NAME is a secret for the vault and a `--plain` NAME a value that comes back to the asker; a card that asks for nothing has his Done.
`--window` brings the goblin's own browser session to him, `--wait` waits on the card and prints what came of it the moment it closes, `--done` closes it once the asker sees the step done, `--withdraw` takes it back, and `--reopen` brings back a card he closed.
Every card has his Skip and Other.
Ask the Supreme Overlord for credential values by name: he pastes each value on the request's card in the Command Center, on the board on this machine only, and the board stores it the way `cfo auth store` does, refreshes the project's running goblins, each for the services its task carries, and wakes you with the names stored, never a value.
A stored name reaches a goblin only when a service of the manifest declares it and the goblin's task carries that service.
Names only: anything shaped like a value (a known key prefix, an equals sign, long random text) is refused without being repeated.
Each `--step`, up to eight, is one step of getting the values on the `--link` page, such as "Reveal the test secret key and copy it", which the card shows as a numbered step in its header after the page to open: say what to do there, not that he should open it.
With `--message-file <file>` the card also carries a message he sends himself, such as an email: the file's optional `To:` line (an email address or an https page) and `Subject:` line, a blank line, then the body, read once when the card is filed and refused, without being repeated, when a word of it is shaped like a key; the card shows the body in a box with Copy, and Copy subject when it has one, so he never opens the file, and a card whose step is only sending it needs no page or step.
A goblin files one for its own task with `--task <its id>`.
With `--env-file <file>` each value is also set in that env file at the root of the project's main checkout, which must be ignored and untracked by git (checked before filing and again before each write).
The card stores each row the moment he pastes it, after the same shape check `cfo auth store` makes, and the request closes once every name is in the scope; each name takes one save, and a request nobody finishes expires after 24 hours (see docs/native-board.md)
