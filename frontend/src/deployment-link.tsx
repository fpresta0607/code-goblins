import type { Deployment } from "./types";
import { Icon, type IconName } from "./Icon";
import { deploymentLook } from "./deployment";
import "./hosted-checks.css";

const MARKS: Record<string, IconName> = { pending: "clock", failed: "warning", cancelled: "close", passed: "check" };

// The deploy of a merged pull request beside it, apart from its checks and in
// their look: Deploying, Deployed, Deploy failed or Deploy cancelled, in its
// tone, with the workflows and the commit in its tip, opening the run.
export function DeploymentLink({ deployment, className }: { deployment: Deployment; className: string }) {
  const look = deploymentLook(deployment);
  const content = <><Icon name={MARKS[look.tone]} /><span>{look.text}</span></>;
  const classes = className + " deployment hosted-" + look.tone;
  return look.url
    ? <a className={classes} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": " + look.tip} data-tip={look.tip}>{content}</a>
    : <span className={classes} aria-label={look.text + ": " + look.tip} data-tip={look.tip}>{content}</span>;
}
