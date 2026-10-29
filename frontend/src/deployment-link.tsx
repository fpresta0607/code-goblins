import type { Deployment } from "./types";
import { Icon, type IconName } from "./Icon";
import { deploymentLook } from "./deployment";
import "./hosted-checks.css";

const MARKS: Record<string, IconName> = { pending: "clock", failed: "warning", cancelled: "close", passed: "check" };

// The deploy of a merged pull request beside it, apart from its checks and in
// their look: Deploying, Deployed, Deploy failed or Deploy cancelled, in its
// tone, opening the run, with the workflows and the commit in a few muted
// words beside it once it failed or was cancelled.
export function DeploymentLink({ deployment, className }: { deployment: Deployment; className: string }) {
  const look = deploymentLook(deployment);
  const content = <><Icon name={MARKS[look.tone]} /><span>{look.text}</span></>;
  const classes = className + " deployment hosted-" + look.tone;
  const detail = (look.tone === "failed" || look.tone === "cancelled") && <span className="pill-detail" aria-hidden="true">{look.tip}</span>;
  return <>{look.url
    ? <a className={classes} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": " + look.tip}>{content}</a>
    : <span className={classes} aria-label={look.text + ": " + look.tip}>{content}</span>}{detail}</>;
}
