import { watchLink, type TerminalLink } from "./terminal-link";

// How long Try again waits for the harness's own limit screen to close
// before it gives up.
const CLOSE_WAIT_MS = 10_000;

// tryAgain asks a session at a usage limit to continue, through the link of
// its terminal (terminal): "continue" as one message. While the harness's
// own screen for the limit is open the terminal takes no typing, so Escape
// closes it first, as its "Esc" says, and the message goes in once it has
// closed, or not at all if it stays open.
export function tryAgain(terminal: string, link: TerminalLink): void {
  if (!link.screen?.isOpen) {
    link.send("continue");
    return;
  }
  const stop = watchLink(terminal, (next) => {
    if (!next || next.screen?.isOpen) return;
    finish();
    next.send("continue");
  });
  const timer = setTimeout(() => finish(), CLOSE_WAIT_MS);
  const finish = () => { stop(); clearTimeout(timer); };
  link.escape();
}
