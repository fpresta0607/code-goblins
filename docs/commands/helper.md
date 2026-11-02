# cfo helper

`cfo helper start <parent-id> --brief <file> [--title "<short title>"]` \| `merge <parent-id>`

A goblin's own helper: `start` asks the supervisor for one helper goblin, which starts only when memory allows, one per goblin at a time and never a helper's, on a branch cut from the goblin's last commit, and says which helper and branch or why not and when to ask again; its brief's Authentication section decides what it carries, a subset of its parent's grants or none ([Credentials by need](../contract/authentication.md#credentials-by-need)); `merge` merges the helper's branch into the goblin's own with a merge commit and retires the helper ([Helper goblins](../contract/dispatch.md#helper-goblins))
