# goblins page check

`goblins page check <html-file> [--out <folder>]`

Checks a Scrawl page a goblin wrote without opening it in the goblin's own browser, so the goblin reads its page as files.
It opens the page with `lavish-axi <html-file> --no-open`, as `goblins review --lavish` does, then loads the review window in a hidden Chrome of its own, with a throwaway profile, at a desktop width of 1920 by 1080 and at a phone width of 390 by 844.
At each width lavish-axi's layout gate measures the page as it does in the Overlord's window, and the command saves a PNG picture of the whole page, grown to its full height up to 16384 pixels, as `desktop.png` and `phone.png` in `--out`, which defaults to a folder beside the page named after it with `-check`.
It then prints each picture's path and the layout issues lavish-axi holds open for the page, the ones his Layout issues inbox shows, each with its title, its width class, its selector and its explanation, and closes the hidden Chrome.
It exits 0 when there are none, 1 when there are any or the check fails, and 2 on a usage error.

Browsers are for people.
A goblin uses its browser or computer automation only for an end-to-end live test of a UI the Overlord can oversee, a sign-in or credential step that needs a person, or automating a person's procedure; what goes straight to an agent, a page, a picture or text, it reads as a file.

Check a page before publishing it with `goblins review --lavish`.
A check of a page the board already shows loads it in one more window, so the board may report its window closed when the hidden Chrome closes.
A page whose session lavish-axi ended is refused as `goblins review` refuses it.

Read the [applicable decisions procedure](../../contract/decisions.md) before acting.
