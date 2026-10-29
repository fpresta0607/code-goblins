import { Icon } from "./Icon";
import { useTerminalLink } from "./terminal-link";
import { WorkingIndicator } from "./working-indicator";
import "./reply-working.css";

// The end of the reply in progress, in the place a finished reply shows when
// it arrived: the working dither in the harness's ink (the Overlord,
// 2026-10-27: "The dither should go where the date is ... the stop would be
// something like an icon when you're hovering the dither, that you can then
// stop the agent"). Pointing at it or reaching it by keyboard shows Stop on
// it, which presses Escape through the link of the session's terminal, as
// the box's Stop does, with no terminal shown. With no terminal to stop
// through (terminal is empty, as for a goblin whose messages queue through
// the board) or while typing is held, it only says the session works. It
// reads no clock: a hint that counted how long it had worked drew it again
// every second, 83 times in 20 s on an idle panel.
export function ReplyWorking({ terminal, ink }: { terminal: string; ink: string }) {
  const link = useTerminalLink(terminal);
  return <div className="reply-working">
    {link && !link.held
      ? <button type="button" className="reply-stop" aria-label="Stop" onClick={() => link.escape()}>
        <WorkingIndicator ink={ink} /><Icon name="stop-circle" />
      </button>
      : <span className="reply-stop"><WorkingIndicator ink={ink} /></span>}
  </div>;
}
