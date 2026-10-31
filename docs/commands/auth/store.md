# cfo auth store

`cfo auth store [--project <p>] <NAME> [value]`

Store one credential in a project's scope, or in the shared scope when `--project` is omitted.
Omit the value to read it from stdin, which keeps the secret out of shell history; a value typed at a console is read without being shown.
A value of the wrong shape is refused with exit code 2, saying why and storing nothing: a control character, fewer than four characters (eight for a name ending in KEY, SECRET or TOKEN), or, for a name that says its kind of key (Stripe, GitHub, OpenRouter, Anthropic, OpenAI, Resend), the wrong prefix or fewer than 20 characters
