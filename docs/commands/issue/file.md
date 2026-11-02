# cfo issue file

`cfo issue file --project <p> --title <t> [--body-file <f>] [--evidence <text>]... [--root <key>]`

Files a [root issue](../../contract/root-issues.md#root-issues) on the project's own GitHub repository with the `ops-root` label, or, when an open `ops-root` issue there has the same root, adds a dated comment with the new evidence instead of a second issue, and prints the issue's address.
The root is `--root` (a test's package and name, a workflow and job, an error's class), else the title.
An issue the command filed carries it in a hidden marker, and one filed by hand matches by the same title.
It reads the open `ops-root` issues first, in pages of a hundred, and files nothing when it cannot read them all.
It refuses text that holds a credential value the terminal's environment carries, a private key or a word shaped like a known kind of key, on any repository, and on a public one also text that names another of the fleet's projects whose repository is not public (asked of GitHub only for the projects the text names, and a project with no GitHub origin counts as private), a teammate the supervisor has read in a collaborative repository (kept in `state/teammates.json`), an @mention other than the repository's owner, or a path on this machine.
A refusal names what it found and never repeats a secret
