# goblins register

`goblins register`

Make this session the primary CEO the board delivers questions, answers, reviews and terminal input to.
The SessionStart hooks do it on their own; run it by hand when the board reports the registration stale or missing.
It refuses anywhere but in [the CEO's own session](../contract/supervision.md#the-ceos-own-session), the harness native terminal `ceo` runs, whose live `goblins host` must answer; there your harness takes the home's session lock, and takes it over, audited, from a live holder that is not that session.
A session in no native terminal, a Herdr pane included, or in any other native terminal registers nothing, and a registration an older build wrote for a CEO in a Herdr pane reads as not reachable until the CEO is started again in a native terminal
