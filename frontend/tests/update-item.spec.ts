import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { expect, openItem, ORIGIN, test, type Page, type Route } from "./site";
import { parseSnapshot } from "../src/types";

// The Overlord, 2026-10-02: "make sure updates for code goblins comes just as
// its own special overlord command". A new release reaches him as an item of
// its own in the Command Center, with its own look, one Update button, and
// its progress and result on the card; a slim banner points to it.

async function openTheItem(page: Page) {
  await page.goto("/tests/fixtures/update-item.html");
  await page.getByLabel("Command Center, 1 waiting on you").click();
  const row = page.locator(".inbox-list li").first();
  await expect(row).toHaveClass(/release-row/);
  await expect(row).toContainText("Code Goblins");
  await expect(row).toContainText("Update to v0.5.0 from v0.4.2");
  await row.getByRole("button").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.locator(".update-card")).toBeVisible();
  return dialog;
}

test("a new release is its own item: the versions, what is new, what the update checks, and one Update button", async ({ page }) => {
  // Arrange
  let sent: Record<string, unknown> | null = null;
  await page.route("**/api/actions", async (route) => {
    sent = route.request().postDataJSON();
    await route.fulfill({ json: { id: String(sent?.id), kind: "run", run_id: "update-v0.5.0-1", status: "queued" } });
  });

  // Act
  const dialog = await openTheItem(page);

  // Assert: an event, not a goblin's chore: no command, the release goblin.
  const card = dialog.locator(".update-card");
  await expect(card.getByRole("heading", { name: "Update Code Goblins" })).toBeVisible();
  await expect(card.locator(".update-versions")).toHaveAccessibleName("From v0.4.2 to v0.5.0");
  await expect(card.locator(".update-notes li")).toHaveText(["The installer sorts out every machine by itself", "AFK mode holds questions for you", "Goblins that stall wake the CFO"]);
  await expect(card.getByRole("link", { name: "What's new" })).toHaveAttribute("href", "https://github.com/fpresta0607/code-goblins/releases/tag/v0.5.0");
  await expect(card.locator(".update-trust")).toContainText("Unsigned release. Update installs it only when each file matches the release's SHA-256, such as cfo.exe 3f9a6c0e…5bc21e.");
  await expect(card.locator(".run-command")).toHaveCount(0);
  await expect(card.locator(".goblin-avatar.persona-releases")).toBeVisible();

  // Act
  await card.getByRole("button", { name: "Update" }).click();

  // Assert: the click names the stored item, never a command.
  await expect.poll(() => sent).not.toBeNull();
  expect(sent).toMatchObject({ kind: "run", run_id: "update-v0.5.0-1", generation: "u".repeat(64) });
  expect(sent).not.toHaveProperty("command");
});

test("Update follows the run step by step, and a rollback says why and offers the next try", async ({ page }) => {
  // Arrange
  await page.route("**/api/runs/update-v0.5.0-1/output", (route) => route.fulfill({ json: { state: "running",
    output: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n      cfo.exe matches the release's SHA256SUMS: 3f9a\n[3/4] Install Code Goblins v0.5.0\nStopping the supervisor (pid 21116).\n" } }));
  const dialog = await openTheItem(page);
  const card = dialog.locator(".update-card");

  // Act
  await page.evaluate(() => window.updateStep("running"));

  // Assert
  await expect(card.getByRole("status")).toHaveText("Updating");
  await expect(card.locator(".update-steps li.done")).toHaveText(["Download Code Goblins v0.5.0", "Check each file's SHA-256"]);
  await expect(card.locator(".update-steps li.now")).toHaveText("Restart the board on v0.5.0");
  await expect(card.getByRole("button", { name: "Update" })).toHaveCount(0);

  // Act
  await page.evaluate(() => window.updateStep("rolledBack"));

  // Assert
  const back = dialog.locator(".update-card");
  await expect(back.getByRole("status")).toHaveText("Rolled back");
  await expect(back.locator(".update-result")).toHaveText("Code Goblins v0.4.2 serves again, and v0.5.0 was not installed; what it printed above says why.");
  await expect(back.locator(".update-steps li.failed")).toHaveText("Restart the board on v0.5.0");

  // Act
  await back.getByRole("button", { name: "Try again" }).click();

  // Assert: the board's next item for the same release, ready to update.
  await expect(dialog.locator(".update-card").getByRole("status")).toHaveText("Ready");
  await expect(dialog.locator(".update-card").getByRole("button", { name: "Update" })).toBeEnabled();
});

test("an update that installed says so, and its item moves to History", async ({ page }) => {
  // Arrange
  const dialog = await openTheItem(page);

  // Act
  await page.evaluate(() => window.updateStep("updated"));

  // Assert
  const card = dialog.locator(".update-card");
  await expect(card.getByRole("status")).toHaveText("Updated");
  await expect(card.locator(".update-result")).toHaveText("v0.5.0 runs. The board reloads on it now.");
  await page.getByRole("button", { name: "Close the Command Center" }).click();
  await page.getByLabel("Command Center").click();
  await page.getByText("History").click();
  await expect(page.locator(".inbox-list li").filter({ hasText: "Update to v0.5.0 from v0.4.2" })).toContainText("Updated v0.5.0");
});

test("the slim banner points to the item, and hides until the next version", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/update-item.html");
  const banner = page.locator(".release-banner");
  await expect(banner).toContainText("Code Goblins v0.5.0 is ready · you run v0.4.2");

  // Act
  await banner.getByRole("button", { name: "Open" }).click();

  // Assert
  await expect(page.getByRole("dialog").locator(".update-card")).toBeVisible();

  // Act
  await page.getByRole("button", { name: "Close the Command Center" }).click();
  await banner.getByRole("button", { name: "Hide until the next version" }).click();

  // Assert: hidden for this version, here and after a reload.
  await expect(banner).toHaveCount(0);
  await page.reload();
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toBeVisible();
  await expect(page.locator(".release-banner")).toHaveCount(0);
});

test("a board built from a clone gets the clone's steps instead of Update", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/update-item.html");

  // Act
  await page.evaluate(() => window.updateStep("source"));

  // Assert
  const banner = page.locator(".release-banner");
  await expect(banner).toContainText("Code Goblins v0.5.0 is out · this board was built from a clone: run git pull, then .\\install.cmd -Dev in the clone");
  await expect(banner.getByRole("button", { name: "Open" })).toHaveCount(0);
  await expect(page.getByLabel("Command Center, 1 waiting on you")).toHaveCount(0);
});

for (const draft of ["answer", "changed answer", "diff comment"] as const) {
  test(`a postponed update reload resumes after the ${draft} is sent or cleared`, async ({ page }) => {
    // A diff comment is typed after opening the task, its changes and the
    // file's diff, which outruns the default limit on a loaded machine.
    if (draft === "diff comment") test.slow();
    const snapshot = parseSnapshot({ healthy: true, instance: "update-reload", revision: 1, cfo_runs: true, build: "old-build",
      tasks: [{ id: "billing", title: "Billing", generation: "g1", phase: "working", verified: false, reported_at: "2026-10-06T14:00:00Z" }],
      questions: [
        { id: "reply", identity: "r".repeat(64), task: "", text: "Which queue?", options: ["A", "B"], status: "pending", created_at: "2026-10-06T14:00:00Z" },
        { id: "history", identity: "h".repeat(64), task: "billing", generation: "g1", text: "Which database?", options: ["SQLite", "PostgreSQL"], status: "succeeded", answered_by: "cfo", answer: "SQLite", answer_kind: "option", answered_option: "SQLite", answered_at: "2026-10-06T14:01:00Z", created_at: "2026-10-06T14:00:00Z" },
      ],
      runs: [{ id: "update-v0.5.0-1", identity: "u".repeat(64), title: "Update Code Goblins", state: "running", created_at: "2026-10-06T14:02:00Z", update: { from: "v0.4.2", to: "v0.5.0" } }],
    });
    await page.addInitScript((first) => {
      localStorage.setItem("cfo-first-open", "shown");
      class SnapshotSource extends EventTarget {
        private publish = (event: Event) => {
          if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) }));
        };
        constructor() {
          super();
          window.addEventListener("fixture-snapshot", this.publish);
          queueMicrotask(() => this.publish(new CustomEvent("fixture-snapshot", { detail: first })));
        }
        close() { window.removeEventListener("fixture-snapshot", this.publish); }
      }
      Object.defineProperty(window, "EventSource", { value: SnapshotSource });
    }, snapshot);
    await page.route("**/api/**", async (route) => {
      const { pathname } = new URL(route.request().url());
      if (pathname === "/api/announce") return route.fulfill({ json: { claimed: [] } });
      if (pathname.endsWith("/files")) return route.fulfill({ json: [{ path: "example.go", status: "modified" }] });
      if (pathname.endsWith("/diff")) return route.fulfill({ json: { path: "example.go", patch: "@@ -1 +1 @@\n-old\n+new\n", code: "", head: "h", revision: "", fingerprint: "f", binary: false, code_omitted: false } });
      return route.fulfill({ status: 404, json: { error: "No fixture for this resource" } });
    });
    const posts: Route[] = [];
    await page.route("**/api/actions", (route) => { posts.push(route); });
    const html = await readFile(join(process.env.BOARD_TEST_SITE!, "index.html"), "utf8");
    let loads = 0;
    await page.route(ORIGIN + "/", (route) => {
      const build = ++loads === 1 ? "old-build" : "new-build";
      return route.fulfill({ contentType: "text/html", body: html.replace("</head>", `<meta name="cfo-build" content="${build}"></head>`) });
    });
    await page.goto("/");
    if (draft === "answer") {
      await openItem(page, "Which queue?");
    } else if (draft === "changed answer") {
      await page.locator(".command-center-menu > summary").click();
      await page.locator(".command-center-menu").getByText("History", { exact: false }).click();
      await page.getByRole("button", { name: "Change the CFO's answer to Billing" }).click();
    } else {
      await page.locator(".task-card").filter({ hasText: "Billing" }).click();
      const taskView = page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true });
      if (await taskView.count()) await taskView.click();
      await page.locator("details.changes-section > summary").click();
      await page.locator("details.file-review > summary").filter({ hasText: "example.go" }).click();
      await page.getByRole("button", { name: "Comment on new line 1" }).click();
    }
    if (draft !== "diff comment") await page.getByRole("radio", { name: /^Other/ }).check();
    const written = page.getByRole("textbox", { name: draft === "diff comment" ? "Comment to the CFO on New line 1" : "Your written answer", exact: true });
    await written.fill("Please keep my draft");

    snapshot.build = "new-build";
    snapshot.revision++;
    const updating = snapshot.runs?.[0];
    if (!updating) throw new Error("The fixture must include its Update item");
    snapshot.runs = [{ ...updating, state: "succeeded", exit_code: 0, finished_at: new Date(Date.now() - 2000).toISOString() }];
    await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), snapshot);

    await expect(page.locator(".update-banner")).toBeVisible();
    await page.waitForTimeout(1200);
    expect(loads).toBe(1);
    await expect(written).toHaveValue("Please keep my draft");
    expect(await page.evaluate(() => document.hidden)).toBe(false);

    if (draft === "diff comment") {
      await page.getByRole("button", { name: "Cancel comment" }).click();
      expect(posts).toHaveLength(0);
    } else {
      await page.getByRole("button", { name: draft === "answer" ? "Send decision" : "Change to my answer", exact: true }).click();
      await expect.poll(() => posts.length).toBe(1);
      await page.waitForTimeout(300);
      expect(loads).toBe(1);
      const body = posts[0].request().postDataJSON();
      expect(body).toMatchObject({ kind: draft === "answer" ? "cfo_answer" : "answer_change", text: "Please keep my draft" });
      await posts[0].fulfill({ json: { id: body.id, kind: body.kind, question_id: body.question_id, status: "queued" } });
    }

    await expect.poll(() => loads).toBe(2);
    await expect(page.locator('meta[name="cfo-build"]')).toHaveAttribute("content", "new-build");
  });
}
