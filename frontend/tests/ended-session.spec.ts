import { expect, test } from "@playwright/test";
import { parseSnapshot } from "../src/types";

test.use({ timezoneId: "UTC", viewport: { width: 1440, height: 900 } });

for (const workspace of ["Board", "Orchestration"]) {
  test(`${workspace} keeps the selected terminal visible when its task moves into retired history`, async ({ page }) => {
    const snapshot = parseSnapshot({ healthy: true, instance: "ended-board-proof", cfo_runs: true, revision: 1,
      tasks: [{ id: "input-proof", title: "Terminal fixes and handoff", project: "code-goblins", backend: "native", harness: "claude", generation: "s1", session: "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", phase: "working", verified: false }],
      sessions: [{ id: "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "input-proof", role: "goblin", task_id: "input-proof", generation: "s1", harness: "claude", phase: "working" }],
    });
    await page.addInitScript((initial) => {
      class SnapshotSource extends EventTarget {
        private publish = (event: Event) => {
          if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) }));
        };
        constructor() {
          super();
          window.addEventListener("fixture-snapshot", this.publish);
          queueMicrotask(() => this.publish(new CustomEvent("fixture-snapshot", { detail: initial })));
        }
        close() { window.removeEventListener("fixture-snapshot", this.publish); }
      }
      Object.defineProperty(window, "EventSource", { value: SnapshotSource });
    }, snapshot);
    await page.route("**/api/**", async (route) => {
      await route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    });
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("READY\r\n"));
    });
    await page.goto("/");
    if (workspace === "Board") {
      await page.getByRole("button", { name: "Open the terminal of Terminal fixes and handoff" }).click();
    } else {
      await page.getByRole("button", { name: "Orchestration", exact: true }).click();
      await page.getByRole("button", { name: /Terminal fixes and handoff/ }).click();
    }
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();

    snapshot.tasks[0] = { ...snapshot.tasks[0], id: "finished:input-proof", generation: "", phase: "done", archived: true, at: "2026-09-29T09:42:00Z", activity: "Terminal fixes verified. Pull request ready." };
    snapshot.sessions = [];
    snapshot.revision++;
    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot);

    await expect(page.getByRole("heading", { name: "Session retired" })).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Open in Windows Terminal", exact: true })).toHaveCount(0);
  });
}

for (const phase of ["retired", "paused", "stopped"]) {
  test(`a ${phase} session shows its report and handoff without opening a terminal`, async ({ page }, testInfo) => {
    let connections = 0;
    await page.routeWebSocket("**/api/terminal/native?*", () => { connections++; });

    await page.goto(`/tests/fixtures/ended-session.html?phase=${phase}`);

    await expect(page.getByRole("heading", { name: `Session ${phase}` })).toBeVisible();
    await expect(page.locator("time")).toHaveAttribute("datetime", "2026-09-29T09:42:00Z");
    await expect(page.getByText("Last report", { exact: true })).toBeVisible();
    await expect(page.getByText("Terminal fixes verified. Pull request ready.", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "Open handoff" })).toHaveAttribute("href", "/api/tasks/input-proof/handoff");
    await expect(page.getByRole("button", { name: "Open the terminal of Terminal fixes and handoff" })).toHaveCount(0);
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toHaveCount(0);
    expect(connections).toBe(0);
    await page.screenshot({ path: testInfo.outputPath(phase + ".png"), fullPage: true });
  });
}

test("the ended message fits a narrow panel and its handoff is keyboard accessible", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 680, height: 700 });
  await page.goto("/tests/fixtures/ended-session.html?phase=retired");
  await expect(page.getByRole("heading", { name: "Session retired" })).toBeVisible();
  const panel = page.getByRole("region", { name: "Session retired" });
  expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.getByRole("link", { name: /^Open pull request / }).focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "Open handoff" })).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath("narrow.png"), fullPage: true });
});

test("missing ended-session details are omitted without inventing a time or a link", async ({ page }) => {
  await page.goto("/tests/fixtures/ended-session.html?phase=retired&missing");
  await expect(page.getByRole("heading", { name: "Session retired" })).toBeVisible();
  await expect(page.locator("time")).toHaveCount(0);
  await expect(page.getByText("Last report", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Open handoff" })).toHaveCount(0);
});

test("a history entry's last update is not presented as its retirement time", async ({ page }) => {
  await page.goto("/tests/fixtures/ended-session.html?phase=retired&missing-time");
  await expect(page.getByRole("heading", { name: "Session retired" })).toBeVisible();
  await expect(page.locator("time")).toHaveCount(0);
});

test("retiring an open terminal replaces it and closes its connection", async ({ page }) => {
  let connections = 0, closed = 0;
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    connections++;
    socket.onClose(() => { closed++; });
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("READY\r\n"));
  });
  await page.goto("/tests/fixtures/ended-session.html?phase=working");
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();

  await page.evaluate(() => window.reportSession("retired"));

  await expect(page.getByRole("heading", { name: "Session retired" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toHaveCount(0);
  await expect.poll(() => closed).toBe(1);
  expect(connections).toBe(1);
});

test("a task ID reused for queued work closes the old terminal without opening another", async ({ page }) => {
  let connections = 0;
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    connections++;
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("READY\r\n"));
  });
  await page.goto("/tests/fixtures/ended-session.html?phase=working");
  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();

  await page.evaluate(() => window.reportSession("queued"));

  await expect(page.getByText("This task has not started yet.", { exact: true })).toBeVisible();
  await expect(page.locator(".xterm-helper-textarea")).toHaveCount(0);
  expect(connections).toBe(1);
});

test("resuming a paused session replaces its message with a fresh live terminal", async ({ page }) => {
  let connections = 0;
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    connections++;
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("READY\r\n"));
  });
  await page.goto("/tests/fixtures/ended-session.html?phase=paused");
  await expect(page.getByRole("heading", { name: "Session paused" })).toBeVisible();
  expect(connections).toBe(0);

  await page.evaluate(() => window.reportSession("working"));

  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Session paused" })).toHaveCount(0);
  expect(connections).toBe(1);
});

for (const phase of ["done", "stale", "unavailable"]) {
  test(`a ${phase} report or observation is not evidence of a retired session`, async ({ page }) => {
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("READY\r\n"));
    });
    await page.goto(`/tests/fixtures/ended-session.html?phase=${phase}`);
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: /^Session (retired|paused|stopped)$/ })).toHaveCount(0);
  });
}
