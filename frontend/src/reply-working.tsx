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
// again every second, 83 times in 20 s on an idle panel.
export function ReplyWorking({ ink, said }: { ink: string; said: string }) {
  return <div className="reply-working">
    <WorkingIndicator ink={ink} label={said || "Working"} />
    <WorkingWord said={said} />
  </div>;
}
