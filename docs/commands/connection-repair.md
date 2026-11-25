# goblins connection-repair

`goblins connection-repair <project> <connection> <action>`

The board uses this bounded connection-repair entry point for the named project, a name or a path as everywhere.
It checks that one project again, plans the repair from what that check finds, and runs it only when the project's connection still offers that action.
An unavailable home or a project folder that is not found is refused before repair.
Read the [authentication procedure](../contract/authentication.md) before handling sign-in or credentials.
