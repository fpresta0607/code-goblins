# cfo answer

`cfo answer <question-id|wake-seq> --option <choice> [--note "<text>"]`

The registered primary CFO answers a goblin's blocked question with `CFO: decision <seq>: <choice>. <note>`.
The notify reads answered so an ordinary `cfo drain` retires it without `--ack-blocking`, and the board records the choice, note, author and time even if the CFO drains the notify first.
For a native goblin paused in the same task generation, the answer is retained in its existing resume note even after its host has ended; normal resume carries it once, preserving earlier retained messages.
The retained answer settles the board decision as the CFO's answer without creating an in-turn delivery record.
Otherwise delivery follows [cfo send](send.md): an answer submitted during a turn waits for the next tool call, or the turn's end if it calls no tool first, and stays on its way on the board until the harness's conversation record shows it taken.

Both a retained answer and an answer submitted during a turn print `queued answer <ref>: <choice>`, with a reminder not to send it again; only completed delivery prints `answered <ref>: <choice>`.
The accepted decision is already recorded, and duplicate submissions are refused.
A missing host is an error unless the current native task generation is paused; a restarted task, unsupported backend or invalid choice is refused.
An argument shape the command cannot act on, such as a task ID where the wake sequence belongs or a choice given without `--option`, exits 2 with the reason and its usage.

`cfo answer <question-id> --option <choice> [--note "<text>"] --record-only [--in <where>]`

registered CFO records on the board a choice already given another way, for a goblin's notify already acknowledged or answered, and sends nothing; --in names where the Overlord gave it, such as chat, which the CFO's own question needs, and the card reads as his answer there

Read the [applicable decisions procedure](../contract/decisions.md) before acting.
