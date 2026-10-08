import { safeGitHubLink } from "./people.ts";
import type { Deployment } from "./types";

// DeploymentLook is how the deploy of a merged pull request shows beside it,
// apart from its checks: the words, a tip naming the deploy workflows and the
// commit, a tone, and the run's page.
export interface DeploymentLook { text: string; tip: string; tone: "pending" | "failed" | "cancelled" | "passed"; url: string }

// deploymentLook says how the deploy a merge started stands in plain words.
// It opens the run that failed or was cancelled, else the newest one, and
// nothing that is not on GitHub.
export function deploymentLook(deployment: Deployment): DeploymentLook {
  const tip = deployment.workflows.join(", ") + " at " + deployment.commit.slice(0, 7);
  const url = safeGitHubLink(deployment.link);
  switch (deployment.state) {
    case "deployed":
      return { text: "Deployed", tip, tone: "passed", url };
    case "failed":
      return { text: "Deploy failed", tip, tone: "failed", url };
    case "cancelled":
      return { text: "Deploy cancelled", tip, tone: "cancelled", url };
  }
  return { text: "Deploying", tip, tone: "pending", url };
}
