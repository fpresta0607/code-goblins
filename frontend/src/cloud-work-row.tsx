import { useState } from "react";
import { message, reportToCeo, request } from "./api";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { rowAge } from "./presentation";
import { cloudWorkBody, cloudWorkSummary, type CloudWork } from "./cloud-work";
import { withoutHarness } from "./task-words";
import { personaFor } from "./workflow";

// CloudWorkRow is one Cloud work ready item in what waits on him: the cloud's
// pull request into a handed-off goblin's branch, with its commits and what
// its body says it ran, and Bring back, which
// brings it onto the branch and resumes the goblin, and Leave it, which
// closes the item until its head moves. Both name the head the row shows, so
// a push since then is refused rather than brought back unseen, and a
// refusal goes to the CEO, never to a line on the board.
export function CloudWorkRow({ work, instance, now }: { work: CloudWork; instance: string; now: number }) {
  const [isSending, setSending] = useState(false);
  const goblin = withoutHarness(work.task.title) || work.task.id;
  const press = async (action: "bring_back" | "leave") => {
    if (isSending) return;
    setSending(true);
    try {
      await request("/api/tasks/cloud", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CEO-Token": instance }, body: JSON.stringify({ task: work.task.id, generation: work.task.generation, action, pull_request: work.sighting.pull_request, head: work.sighting.head }) });
    } catch (error: unknown) {
      reportToCeo((action === "leave" ? "Leave it on " : "Bring back of ") + work.sighting.pull_request, message(error));
    } finally {
      setSending(false);
    }
  };
  return <li className="cloud-work-row">
    <Avatar persona={personaFor(work.task)} small />
    <span className="inbox-text"><strong>Cloud work ready for {goblin}</strong><span className="inbox-summary"><a href={work.sighting.pull_request} target="_blank" rel="noreferrer">{cloudWorkSummary(work.sighting)}</a></span><span className="inbox-summary">{cloudWorkBody(work.sighting)}</span></span>
    <time>{rowAge(work.sighting.seen_at, now)}</time>
    <button type="button" className="labelled-button" aria-label={"Bring back " + work.sighting.pull_request + " onto " + goblin} disabled={isSending} onClick={() => void press("bring_back")}><Icon name="download" /><span>Bring back</span></button>
    <button type="button" className="labelled-button" aria-label={"Leave " + work.sighting.pull_request} disabled={isSending} onClick={() => void press("leave")}><Icon name="close" /><span>Leave it</span></button>
  </li>;
}
