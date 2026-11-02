# cfo auth refresh

`cfo auth refresh <task-id>`

Regenerate a task's `auth.ps1` from the store, for the services the task carries and no other.
Unknown and archived tasks are refused.
Storing or copying into a project scope does this automatically for every live task of that project and sends each one a one-line re-source notice, so a credential stored after spawn reaches a goblin that is already working and carries its service.
Writes only the task's own tasktmp file, never the worktree `.env`
