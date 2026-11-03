import { Icon } from "./Icon";
import { QUEUED_NOTICE } from "./queued-message";
import { useTerminalLink } from "./terminal-link";
import { WorkingIndicator } from "./working-indicator";
import { WorkingWord } from "./working-word";
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
// next to dither all the whimsical verbs claude whilst working uses"), typed
// out and then giving way to more (working-word.tsx). Stop sits at the end of that one row,
// shown while he points at the row or the keyboard is on it, and always on a
// touch screen, which cannot point (2026-10-28: "on mobile put the button on
// the end of it"). With no Stop of its own (canStop false, as for a goblin
// whose messages queue through the board) it only says the session works.
// While he reads up the row stays as it was, so once the session stopped
// working or its typing is held, stopWhy says why its Stop cannot stop
// anything: Stop then looks disabled and a press says so over the box of
// task's session, never typing Escape into an idle harness. It reads no
// clock: a hint that counted how long it had worked drew it again every
// second, 83 times in 20 s on an idle panel.
export function ReplyWorking({ terminal, task, ink, canStop, stopWhy, said }: { terminal: string; task: string; ink: string; canStop: boolean; stopWhy: string; said: string }) {
  const link = useTerminalLink(terminal);
  return <div className="reply-working">
    <WorkingIndicator ink={ink} label={said || "Working"} />
    <WorkingWord said={said} />
    {canStop && <button type="button" className="reply-stop" aria-label="Stop" aria-disabled={!!stopWhy}
      onClick={() => { if (stopWhy) window.dispatchEvent(new CustomEvent(QUEUED_NOTICE, { detail: { task, text: stopWhy } })); else link?.escape(); }}><Icon name="stop-circle" /></button>}
  </div>;
}
