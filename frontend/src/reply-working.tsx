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
// the box's Stop does, with no terminal shown. Beside the dither are the
// words the harness's own turn row says it is doing, as Claude Code's
// whimsical verbs, read by the relay from the session's terminal, or Working
// when it prints none (the Overlord, 2026-10-28: "also have words that appear
// next to dither all the whimsical verbs claude whilst working uses"); they
// change only when the harness's do. Stop sits at the end of that one row,
// shown while he points at the row or the keyboard is on it, and always on a
// touch screen, which cannot point (2026-10-28: "on mobile put the button on
// the end of it"). With no Stop of its own (canStop false, as for a goblin
// whose messages queue through the board) or while typing is held, it only
// says the session works.
export function ReplyWorking({ terminal, ink, canStop }: { terminal: string; ink: string; canStop: boolean }) {
  const link = useTerminalLink(terminal);
  const doing = link?.screen?.isWorking && link.screen.doing || "Working";
  return <div className="reply-working">
    <WorkingIndicator ink={ink} label={doing} />
    <span className="reply-doing" aria-hidden="true">{doing}</span>
    {link && canStop && !link.held && <button type="button" className="reply-stop" aria-label="Stop" onClick={() => link.escape()}><Icon name="stop-circle" /></button>}
  </div>;
}
