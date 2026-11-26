import { useState } from "react";
import type { Task } from "./types";
import { Icon } from "./Icon";
import { pullRequestLabel, safePullRequest } from "./workflow";

// A goblin handed off to a cloud session: the pull request its packet is in,
// and the prompt goblins handoff prints for the cloud session, to copy into
// one opened on the repository. A copy the browser refuses says so, and the
// prompt stays on the panel to select.
export function CloudHandoffDetails({ task }: { task: Task }) {
  const [copy, setCopy] = useState<"" | "copied" | "refused">("");
  const handed = task.cloud_handoff;
  const pr = safePullRequest(handed?.pull_request ?? "");
  if (!handed || !pr) return null;
  const onCopy = () => navigator.clipboard.writeText(handed.prompt).then(() => setCopy("copied"), () => setCopy("refused"));
  return <section className="lifecycle-details cloud-handoff" aria-label="In the cloud">
    <h3>In the cloud</h3>
    <p>Its packet is in <a href={pr} target="_blank" rel="noreferrer">{pullRequestLabel(pr)}</a>. Paste this into a cloud session on the repository:</p>
    <p className="cloud-prompt">{handed.prompt}</p>
    <button className="labelled-button" onClick={() => void onCopy()}><Icon name={copy === "copied" ? "check" : "copy"} /><span>{{ "": "Copy prompt", copied: "Copied", refused: "Not copied: select it above" }[copy]}</span></button>
  </section>;
}
