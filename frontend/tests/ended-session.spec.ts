import { expect, test, type Page } from "@playwright/test";
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

type Connection = { task: string; generation: string; control: boolean; closed: boolean };
type Pane = { task: string; generation: string; open: boolean };

// Each mounted terminal opens one view of its pane; a shown Herdr pane may then
// trade that view for a sized one, which still belongs to the same mount.
function panesOf(connections: Connection[]): Pane[] {
  const panes: Pane[] = [];
  const latest = new Map<string, Pane>();
  for (const connection of connections) {
    let pane = latest.get(connection.task);
    if (!connection.control || !pane) {
      pane = { task: connection.task, generation: connection.generation, open: false };
      panes.push(pane);
      latest.set(connection.task, pane);
    }
    if (!connection.closed) pane.open = true;
  }
  return panes;
}

// Records each terminal transport the page opens and whether it was disposed:
// a native terminal's socket to its host, or Herdr's screen streams.
async function watchConnections(page: Page, backend: "native" | "herdr"): Promise<() => Promise<Pane[]>> {
  if (backend === "native") {
    const opened: Connection[] = [];
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      const query = new URL(socket.url()).searchParams;
      const connection = { task: query.get("task") || "", generation: query.get("generation") || "", control: false, closed: false };
      opened.push(connection);
      socket.onClose(() => { connection.closed = true; });
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("READY\r\n"));
    });
    return async () => panesOf(opened);
  }
  await page.addInitScript(() => {
    const streams: Connection[] = [];
    Object.assign(window, { streams });
    const originalFetch = window.fetch;
    window.fetch = (resource, options) => {
      if (resource !== "/api/terminal/stream") return originalFetch(resource, options);
      const body = JSON.parse(String(options?.body));
      const encoder = new TextEncoder();
      const connection = { task: body.task, generation: body.generation, control: !!body.control, closed: false };
      streams.push(connection);
      return Promise.resolve(new Response(new ReadableStream({ start(controller) {
        controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.ready", lease: body.control ? "sized" : "observed", identity: "proof" }) + "\n"));
        controller.enqueue(encoder.encode(JSON.stringify({ type: "terminal.frame", encoding: "ansi", seq: 1, full: true, width: 80, height: 24, bytes: btoa("READY\r\n") }) + "\n"));
        options?.signal?.addEventListener("abort", () => { connection.closed = true; controller.close(); }, { once: true });
      } })));
    };
  });
  await page.route("**/api/terminal/input", (route) => route.fulfill({ json: { seq: route.request().postDataJSON().seq } }));
  await page.route("**/api/terminal/history", (route) => route.fulfill({ json: { text: "READY", agent: "codex" } }));
  return async () => panesOf(await page.evaluate(() => (window as unknown as { streams: Connection[] }).streams.map((connection) => ({ ...connection }))));
}

for (const backend of ["native", "herdr"] as const) {
  test(`${backend}: resuming a paused session connects only once the resumed generation is live`, async ({ page }) => {
    const connections = await watchConnections(page, backend);
    await page.goto(`/tests/fixtures/ended-session.html?phase=paused&backend=${backend}`);
    await expect(page.getByRole("heading", { name: "Session paused" })).toBeVisible();

    await page.evaluate(() => window.reportSession("resuming", "s1-4b8e"));
    await expect.soft(page.getByText("Resuming session...", { exact: true })).toBeVisible();
    await page.evaluate(() => window.reportSession("working", "s2-9d41"));

    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
    expect(await connections()).toEqual([{ task: "input-proof", generation: "s2-9d41", open: true }]);
  });

  test(`${backend}: stopping a paused session never connects to its ended generation`, async ({ page }) => {
    const connections = await watchConnections(page, backend);
    await page.goto(`/tests/fixtures/ended-session.html?phase=paused&backend=${backend}`);
    await expect(page.getByRole("heading", { name: "Session paused" })).toBeVisible();

    await page.evaluate(() => window.reportSession("stopping", "s1-4b8e"));
    await expect.soft(page.getByText("Stopping session...", { exact: true })).toBeVisible();
    await page.evaluate(() => window.reportSession("stopped", "s1-4b8e"));

    await expect(page.getByRole("heading", { name: "Session stopped" })).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toHaveCount(0);
    expect(await connections()).toEqual([]);
  });
}

test("an ended summary never takes a live Herdr stream from three open terminals", async ({ page }) => {
  const connections = await watchConnections(page, "herdr");
  await page.goto("/tests/fixtures/ended-session.html?phase=retired&crew");
  for (const id of ["alpha", "beta", "gamma"]) {
    await page.evaluate((shown) => window.showTask(shown), id);
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  }

  await page.evaluate(() => window.showTask("finished:input-proof"));
  await expect(page.getByRole("heading", { name: "Session retired" })).toBeVisible();
  await page.evaluate(() => window.showTask("alpha"));

  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  expect(await connections()).toEqual([
    { task: "alpha", generation: "c1-7f3a", open: true },
    { task: "beta", generation: "c2-7f3a", open: true },
    { task: "gamma", generation: "c3-7f3a", open: true },
  ]);
});

const open = async (connections: () => Promise<Pane[]>) => (await connections()).filter((pane) => pane.open);

test("a paused fourth Herdr goblin resumed while shown keeps at most three live streams", async ({ page }) => {
  const connections = await watchConnections(page, "herdr");
  await page.goto("/tests/fixtures/ended-session.html?phase=paused&backend=herdr&crew");
  for (const id of ["alpha", "beta", "gamma"]) {
    await page.evaluate((shown) => window.showTask(shown), id);
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  }
  await page.evaluate(() => window.showTask("input-proof"));
  await expect(page.getByRole("heading", { name: "Session paused" })).toBeVisible();

  await page.evaluate(() => window.reportSession("resuming", "s1-4b8e"));
  await expect(page.getByText("Resuming session...", { exact: true })).toBeVisible();
  await page.evaluate(() => window.reportSession("working", "s2-9d41"));

  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  await expect.poll(() => open(connections)).toEqual([
    { task: "beta", generation: "c2-7f3a", open: true },
    { task: "gamma", generation: "c3-7f3a", open: true },
    { task: "input-proof", generation: "s2-9d41", open: true },
  ]);
  expect((await connections()).filter((connection) => connection.task === "alpha")).toEqual([{ task: "alpha", generation: "c1-7f3a", open: false }]);
});

test("a paused Herdr goblin resumed in the background stays within three live streams", async ({ page }) => {
  const connections = await watchConnections(page, "herdr");
  await page.goto("/tests/fixtures/ended-session.html?phase=paused&backend=herdr&crew");
  await expect(page.getByRole("heading", { name: "Session paused" })).toBeVisible();
  for (const id of ["alpha", "beta", "gamma"]) {
    await page.evaluate((shown) => window.showTask(shown), id);
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  }

  await page.evaluate(() => window.reportSession("resuming", "s1-4b8e"));
  await page.evaluate(() => window.reportSession("working", "s2-9d41"));
  await page.evaluate(() => window.showTask("beta"));

  await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  expect(await connections()).toEqual([
    { task: "alpha", generation: "c1-7f3a", open: true },
    { task: "beta", generation: "c2-7f3a", open: true },
    { task: "gamma", generation: "c3-7f3a", open: true },
  ]);
});

for (const backend of ["native", "herdr"] as const) {
  test(`${backend}: a failed resume opens no transport and a retry connects only the resumed generation`, async ({ page }) => {
    const connections = await watchConnections(page, backend);
    await page.goto(`/tests/fixtures/ended-session.html?phase=paused&backend=${backend}`);
    await expect(page.getByRole("heading", { name: "Session paused" })).toBeVisible();
    await page.evaluate(() => window.reportSession("resuming", "s1-4b8e", { action: "resume", phase: "resuming" }));

    await page.evaluate(() => window.reportSession("unavailable", "s2-9d41", { action: "resume", phase: "failed" }));
    await expect.soft(page.getByText("Resume failed. See Task for details.", { exact: true })).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toHaveCount(0);
    await page.evaluate(() => window.reportSession("resuming", "s2-9d41", { action: "resume", phase: "failed" }));
    await expect.soft(page.getByText("Resuming session...", { exact: true })).toBeVisible();
    await page.evaluate(() => window.reportSession("working", "s3-a17c"));

    await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
    expect(await connections()).toEqual([{ task: "input-proof", generation: "s3-a17c", open: true }]);
  });
}
