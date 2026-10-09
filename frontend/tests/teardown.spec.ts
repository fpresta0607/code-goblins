import { expect, test } from "./site";
import snapshot from "./fixtures/queued-snapshot.json" with { type: "json" };

// The Overlord, 2026-10-08: "everything error wise goes to cfo". A card says
// nothing of Windows still closing a goblin's programs; its panel names them
// behind Details.
test("paused, resumed, stopped and restarted cards leave Windows teardown to the panel's Details", async ({ page }) => {
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
    await expect(card).toBeVisible();
    await expect(card.getByText(/Windows is still closing/)).toHaveCount(0);
    await card.click();
    await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
    const details = page.locator(".lifecycle-panel");
    // The panel names the process behind Details. Working has no line under
    // it (the Overlord, 2026-10-05), so there the sentence is the first thing
    // behind Details. A paused goblin has no line either, and its Details is
    // a description for a person (2026-10-09): what resumes it, then the
    // program Windows still closes, without its process id.
    const header = page.locator(".panel-header");
    const isWorking = phase === "working" || phase === "restarted";
    await expect(header.locator(".panel-activity")).toHaveCount(0);
    await header.locator(".raw-details > summary").click();
    if (phase === "paused") await expect(header.locator(".raw-details-text")).toHaveText("It stays paused until you resume it. Windows is still closing chrome.exe.");
    else {
      await expect(header.locator(".raw-details-text")).toContainText(isWorking ? "Windows is still closing chrome.exe." : "chrome.exe pid 42");
      await expect(header.locator(".raw-details-text")).toContainText("chrome.exe pid 42");
    }
    if (phase === "working") {
      await expect(header.locator(".panel-status")).toHaveText("Working");
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
