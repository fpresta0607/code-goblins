# cfo allowance-floor

`cfo allowance-floor [<claude\|codex> <percent>]`

Say each provider's weekly allowance floor, the percent of its week the fleet keeps back, and whether it is the default or set in the home's `config/fleet.json` (`weekly_floor_percent`), or set one provider's floor there, keeping every other setting in the file.
A home that never set one keeps 5 percent of each week back.
At 0 nothing pauses, waits or refuses for that provider's weekly allowance until the provider itself refuses, and a week it then reports used up is waited out as a used-up session is.
The supervisor's pause, the scheduler, `cfo spawn` with its lane choice, `cfo route` and the board's dials all read this one setting and say the percent they used: `cfo spawn` reads it at once, the supervisor at its next fleet reading, within a minute.
It is the Supreme Overlord's setting, which you change on his word, and a goblin's or a gate agent's terminal, or an agent session that is not [the CFO's own](../contract/supervision.md#the-cfos-own-session), can read it but never change it
