# cfo help

`cfo help [<command> [<subcommand>] | contract <topic>]`

With no topic, list available references without their bodies.
A listed command or procedure topic returns only its embedded reference.
A command group with only subtopics lists those subtopics.
Unknown or malformed help topics exit 2 and print no contract body.
A reference read or output failure exits 1.
Help resolves no fleet home and makes no fleet change.
