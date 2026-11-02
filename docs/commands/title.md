# cfo title

`cfo title <id> "<short title>"`

Give a task its short title (one line, at most 100 characters) when it was dispatched without one, or change it.
It is the one way to change the title of a ticket's issue once it is open.
For a running task it writes the title into the task's record, which the board shows; for a running task or one with a ticket it keeps the title for the supervisor, which writes it to the issue it opened for the task within ten minutes, even once that issue is closed, and to nothing else of a closed one.
A claimed issue keeps its author's title.
A task that is neither running nor ticketed is refused; a queued one takes its title from its backlog row or from `cfo spawn --title`
