import { useNow } from "./clock";
import { Icon } from "./Icon";
import { elapsedText } from "./session-usage";
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
// the board) or while typing is held, it only says the session works. Its
// hint says for how long, from since.
export function ReplyWorking({ terminal, ink, since }: { terminal: string; ink: string; since: string }) {
  const link = useTerminalLink(terminal);
  const worked = elapsedText(since, useNow(1_000));
  const tip = "Working" + (worked ? " " + worked : "");
  return <div className="reply-working">
    {link && !link.held
      ? <button type="button" className="reply-stop" aria-label="Stop" data-tip={tip + " · Stop"} onClick={() => link.escape()}>
        <WorkingIndicator ink={ink} /><Icon name="stop-circle" />
      </button>
      : <span className="reply-stop" data-tip={tip}><WorkingIndicator ink={ink} /></span>}
  </div>;
}
