import { expect, test } from "./site";
import snapshot from "./fixtures/queued-snapshot.json" with { type: "json" };

test("paused, resumed, stopped and restarted cards show Windows teardown at a readable size", async ({ page }) => {
  const tasks = [...["paused", "working", "stopped"].map((phase) => ({
    id: phase === "stopped" ? "finished:teardown-stop" : `teardown-${phase}`,
    title: `${phase} teardown fixture`, project: "fixture", phase,
    verified: false, archived: phase === "stopped", generation: "generation-1", teardown: ["chrome.exe pid 42"],
    lifecycle: { phase: phase === "working" ? "running" : phase, action: phase === "paused" ? "pause" : phase === "working" ? "resume" : "stop", at: "2026-09-30T07:00:00Z", kept: ["worktree"], stopped: ["chrome.exe pid 42"], problems: [], handoff_saved: true, validation_restarts: false },
  })), { id: "teardown-restarted", title: "restarted teardown fixture", project: "fixture", phase: "working", verified: false, generation: "generation-2", teardown: ["chrome.exe pid 42"] }];
  await page.route("**/api/**", async (route) => {
    if (new URL(route.request().url()).pathname === "/api/events") {
      await route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify({ ...snapshot, tasks })}\n\n` });
    } else {
      await route.fulfill({ json: {} });
    }
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.getByRole("region", { name: "Paused", exact: true })).not.toContainText("released");
  for (const phase of ["paused", "working", "stopped", "restarted"]) {
    const card = page.locator(".task-card").filter({ hasText: `${phase} teardown fixture` });
    const notice = card.getByText("Finishing Windows teardown: chrome.exe pid 42", { exact: true });
    await expect(notice).toBeVisible();
    expect(await notice.evaluate((element) => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);
    await card.click();
    await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
    const details = page.locator(".lifecycle-panel");
    await expect(details.getByText("Finishing Windows teardown: chrome.exe pid 42", { exact: true })).toBeVisible();
    if (phase === "working") {
      await expect(details.locator(".plain-status")).toContainText("Resumed");
    }
    if (phase === "paused") {
      await expect(page.getByRole("button", { name: "Resume paused teardown fixture", exact: true }).first()).toBeEnabled();
    }
    if (phase === "restarted") {
      await expect(page.getByRole("region", { name: "In progress", exact: true }).locator(".task-card").filter({ hasText: "restarted teardown fixture" })).toBeVisible();
      await expect(details.getByRole("region", { name: "Task lifecycle" })).toHaveCount(0);
      await expect(details.getByRole("button", { name: "Pause restarted teardown fixture", exact: true })).toBeEnabled();
    }
    await page.getByRole("button", { name: "Back to the CFO", exact: true }).click();
  }
});
