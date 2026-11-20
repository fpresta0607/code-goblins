# goblins issue unowned

`goblins issue unowned`

Lists, for every project of the home, the open issues that are not the root clean-up's (no `ops-root` label) and that no live or queued task names, and apart from them those that only finished or parked tasks name, each with those tasks.
It reads only: it writes nothing on GitHub or in the home.
A project is a folder of `data/projects` whose checkout the projects root holds, and its issues go on its project record's `issue_repository` or else its origin; projects that share one repository are listed together once.
A task is live while the home keeps its record, queued while its row is under `## Queued`, parked under `## Parked`, and finished when only its status log, its outcome or its `## Done` row is left.
An issue a task names is one its brief names to take up, as `goblins cleanup` reads it, one its backlog row's line names (by address, as `owner/name#N`, `#N`, "issue N", "issues N, M and K" or "issues N to M"), and one its pull requests name, which GitHub links them to close or their body names after a closing word or "Part of".
It reads every task's pull requests through GitHub's GraphQL API, fifty to a query, and each repository's open issues a hundred to a page, up to ten pages; a repository with more is named as not read rather than listed in part.
Each repository's line counts its open issues, its `ops-root` ones, those a live or queued task names, those only finished or parked tasks name, and those no task names, and the last two are listed under it with their titles and addresses.
What it could not read, a project that does not resolve, a repository GitHub will not show, a live task's record or a pull request GitHub does not show, is named at the end, since an issue only that would have named is listed as unnamed.
