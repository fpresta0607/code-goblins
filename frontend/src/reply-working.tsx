import type { HarnessRetry } from "./terminalStream";
import { WorkingIndicator } from "./working-indicator";
import { WorkingWord } from "./working-word";
import "./reply-working.css";

// The end of the reply in progress, in the place a finished reply shows when
// it arrived: one row that says the session works and holds nothing to
// press. It is the working dither in the harness's ink, and beside it the
// words the harness's own turn row says it is doing, as Claude Code's
// whimsical verbs, read by the relay from the session's terminal, or Working
// when it prints none, typed out and then giving way to more
// (working-word.tsx). It holds no Stop (the Overlord, 2026-11-03: "The stop
// button next to the When I'm the verb icon bothers me. Remove it."; #1987):
// the box stops a turn, with its own Stop in Send's place and with Escape.
// It reads no clock: a hint that counted how long it had worked drew it
// again every second, 83 times in 20 s on an idle panel. While the session's
// own hooks say it compacts the conversation, it reads Compacting alone, with
// no whimsical verb after it, since the session is not at work on his ask
// (the Overlord, 2026-11-04). While the terminal's turn row says it waits to
// send a request that failed again, it reads Retrying alone, with the attempt
// and the failure the terminal names after it, as long as the terminal shows
// them (#2236). A row held while he reads up keeps its room but shows
// nothing once the session no longer says the turn runs, so the rows he
// reads stay put and no finished turn reads as working.
export function ReplyWorking({ ink, said, isCompacting, isHeld, retry }: { ink: string; said: string; isCompacting: boolean; isHeld: boolean; retry?: HarnessRetry }) {
  const words = isCompacting ? "Compacting" : retry ? "Retrying" : said;
  return <div className={"reply-working" + (isCompacting ? " compacting" : retry ? " retrying" : "") + (isHeld ? " held" : "")} aria-hidden={isHeld || undefined}>
    <WorkingIndicator ink={isCompacting ? "var(--purple)" : ink} label={words || "Working"} />
    <WorkingWord said={words} isAlone={isCompacting || !!retry} />
    {retry && <span className="reply-retry">{"attempt " + retry.attempt + " of " + retry.attempts + " · " + retry.reason}</span>}
  </div>;
}
