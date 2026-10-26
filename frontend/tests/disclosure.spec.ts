import { expect, openChat, openItem, openTaskList, test, type Locator, type Page } from "./site";
import { caret, nativeMarkers } from "./caret";
import { at, field, GOBLIN, open, openGoblin, snapshot, typed } from "./session-board";

const CATALOG = { harnesses: [{ id: "claude", name: "Claude Code", models: [
  { id: GOBLIN.model, name: "Current model", efforts: ["xhigh"], default_effort: "xhigh" },
  { id: "next-model", name: "Next model", efforts: ["high"], default_effort: "high" },
] }] };
const LOGIN = { id: "review-login", provider: "Stripe", account: "Review account", projects: ["code-goblins"], signed_in: true };

async function expectDisclosure(page: Page, selector: string, isOpen: boolean) {
  const disclosure = page.locator(selector);
  await expect(disclosure).toHaveJSProperty("open", isOpen);
  const accessibility = await page.context().newCDPSession(page);
  try {
    const { result } = await accessibility.send("Runtime.evaluate", { expression: `document.querySelector(${JSON.stringify(selector + " > summary")})` });
    const { nodes: [node] } = await accessibility.send("Accessibility.getPartialAXTree", { objectId: result.objectId, fetchRelatives: false });
    expect(node.role?.value).toBe("DisclosureTriangle");
    expect(node.properties?.find((property) => property.name === "expanded")?.value.value).toBe(isOpen);
  } finally { await accessibility.detach(); }
  await expect.poll(async () => (await caret(disclosure.locator("> summary")))?.turn).toBe(isOpen ? "matrix(0, 1, -1, 0, 0, 0)" : "none");
}

async function expectModal(page: Page, dialog: Locator) {
  await expect(dialog).toBeVisible();
  await expect.poll(() => dialog.evaluate((element) => element.matches(":modal") && element.contains(document.activeElement))).toBe(true);
  await page.keyboard.press("Tab");
  await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  await page.keyboard.press("Shift+Tab");
  await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
}

async function resizePanel(page: Page, width: number) {
  await page.locator(".context-pane").evaluate((element, next) => { element.style.width = `${next}px`; }, width);
  await expect(page.locator(".panel-view-switch")).toBeVisible();
}

async function preparePanel(page: Page, who = "goblin", fields: Record<string, unknown> = {}) {
  await page.addInitScript(() => localStorage.setItem("cfo-pane-width", "800"));
  const relay = await open(page, snapshot(fields));
  const posts: { path: string; body: unknown }[] = [];
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "POST") {
      posts.push({ path, body: route.request().postDataJSON() });
      if (path === "/api/credentials/save") return route.fulfill({ json: { saved: ["REVIEW_VALUE"] } });
      return route.fulfill({ json: { revision: 2, browser_logins: [] } });
    }
    if (path === "/api/engines") return route.fulfill({ json: CATALOG });
    if (path === "/api/connections") return route.fulfill({ json: { instance: "fixture", checking: false, entries: [], browser_logins: [LOGIN] } });
    if (path === "/api/workspace") return route.fulfill({ json: { repository: "code-goblins", harness: "claude", notes: [] } });
    return route.fulfill({ json: {} });
  });
  if (who === "goblin") await openGoblin(page);
  else await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await openTaskList(page);
  return { relay, posts };
}

test("Disclosure opens and closes with its shared caret and releases ordinary content", async ({ page }) => {
  await page.goto("/tests/fixtures/disclosure.html");
  const preview = page.locator("#preview");
  const summary = preview.locator("> summary");
  await expectDisclosure(page, "#preview", false);
  await expect(preview.getByRole("textbox", { includeHidden: true })).toHaveCount(0);
  const closedCaret = await caret(summary);
  expect(closedCaret).toMatchObject({ width: "18px", height: "18px", weight: "1.6px" });
  expect(await nativeMarkers(page.locator("main"))).toEqual([]);

  await summary.press("Enter");

  await expectDisclosure(page, "#preview", true);
  await expect(preview.getByRole("textbox")).toHaveValue("Original");
  await preview.getByRole("textbox").fill("Unsaved preview");
  await summary.press("Space");
  await expectDisclosure(page, "#preview", false);
  await expect(preview.getByRole("textbox", { includeHidden: true })).toHaveCount(0);
  await summary.click();
  await expect(preview.getByRole("textbox")).toHaveValue("Original");
  await expectDisclosure(page, "#initially-open", true);
  await expect(page.getByText("Initial content", { exact: true })).toBeVisible();
  await page.locator("#initially-open > summary").click();
  await expectDisclosure(page, "#initially-open", false);
  await expect(page.getByText("Initial content", { exact: true })).toHaveCount(0);
});

test("controlled and persistent Disclosures share the caret while retaining only persistent edits", async ({ page }) => {
  await page.goto("/tests/fixtures/disclosure.html");
  const retained = page.locator("#retained");
  const controlled = page.locator("#controlled");
  const edit = retained.getByRole("textbox", { includeHidden: true });
  await expect(edit).toHaveCount(1);
  await expect(edit).toBeHidden();
  await retained.locator("> summary").click();
  await expectDisclosure(page, "#retained", true);
  await edit.fill("Keep this edit");

  await retained.locator("> summary").click();

  await expectDisclosure(page, "#retained", false);
  await expect(edit).toHaveValue("Keep this edit");
  await expect(edit).toBeHidden();
  await retained.locator("> summary").click();
  await expect(edit).toBeVisible();
  await expect(edit).toHaveValue("Keep this edit");
  await page.getByRole("button", { name: "Toggle controlled" }).click();
  await expectDisclosure(page, "#controlled", true);
  await expect(page.getByText("Controlled content", { exact: true })).toBeVisible();
  expect(await caret(controlled.locator("> summary"))).toEqual(await caret(retained.locator("> summary")));
  await controlled.locator("> summary").click();
  await expectDisclosure(page, "#controlled", false);
  await expect(page.getByRole("status", { name: "Controlled state" })).toHaveText("Closed");
  await expect(page.getByText("Controlled content", { exact: true })).toHaveCount(0);
  await controlled.locator("> summary").press("Enter");
  await expect(page.getByRole("status", { name: "Controlled state" })).toHaveText("Open");
  await page.getByRole("button", { name: "Toggle controlled" }).click();
  await expectDisclosure(page, "#controlled", false);
});

for (const who of ["goblin", "CFO"]) {
  test(`${who}'s panel selector switches between its conversation and its task view and keeps an unsaved choice`, async ({ page }) => {
    const { posts } = await preparePanel(page, who);
    const switcher = page.locator(".panel-view-switch");
    const chat = switcher.getByRole("button", { name: "Chat", exact: true });
    const task = switcher.getByRole("button", { name: "Task", exact: true });
    const harness = page.locator("details.harness");

    // preparePanel left the panel on its task view, so the conversation is
    // one press away, and the task view is reached by the keyboard too.
    await chat.click();
    await expect(chat).toHaveAttribute("aria-pressed", "true");
    await expect(page.locator(".goblin-panel > .panel-task")).toBeHidden();
    await task.focus();
    await page.keyboard.press("Enter");
    await expect(task).toHaveAttribute("aria-pressed", "true");
    await expect(page.locator(".goblin-panel > .panel-task")).toBeVisible();
    await expectDisclosure(page, "details.harness", false);
    await harness.locator("> summary").click();
    await expectDisclosure(page, "details.harness", true);
    if (who === "goblin") await harness.getByRole("combobox", { name: "Model", exact: true }).selectOption("next-model");
    else {
      await harness.getByRole("button", { name: "Harness: Claude Code" }).click();
      await page.getByRole("menuitemradio", { name: "Codex", exact: true }).click();
    }

    await chat.click();
    await expect(chat).toHaveAttribute("aria-pressed", "true");
    await expect(page.locator(".goblin-panel > .panel-task")).toBeHidden();
    await task.click();
    await expect(page.locator(".goblin-panel > .panel-task")).toBeVisible();
    await expectDisclosure(page, "details.harness", true);
    if (who === "goblin") await expect(harness.getByRole("combobox", { name: "Model", exact: true })).toHaveValue("next-model");
    else await expect(harness.getByRole("button", { name: "Harness: Codex" })).toBeVisible();
    expect(posts).toEqual([]);
  });
}

for (const kind of ["stop", "forget"] as const) {
  for (const width of [800, 1000]) {
    for (const dismissal of ["Cancel", "Escape"] as const) {
      test(`${kind} stays modal across ${width}px to ${1800 - width}px and ${dismissal} preserves its task`, async ({ page }) => {
        const { relay, posts } = await preparePanel(page);
        await openChat(page);
        await field(page).fill("Keep my message draft");
        await openTaskList(page);
        await resizePanel(page, width);
        const harness = page.locator("details.harness");
        await harness.locator("> summary").click();
        const model = harness.getByRole("combobox", { name: "Model", exact: true });
        await model.selectOption("next-model");
        if (kind === "forget") await page.locator("details.connections > summary").click();
        const trigger = kind === "stop" ? page.locator(".task-controls").getByRole("button", { name: /^Stop / }) : page.getByRole("button", { name: /^Forget Stripe/ });
        await trigger.click();
        const dialog = page.getByRole("dialog", { name: kind === "stop" ? "Stop this task?" : "Forget this login?", exact: true });
        const identity = kind === "stop" ? "Swipes scroll the phone terminal" : "Stripe · Review account";
        await expect(dialog).toContainText(identity);
        await expectModal(page, dialog);
        const mounted = await dialog.elementHandle();

        await resizePanel(page, 1800 - width);

        await expectModal(page, dialog);
        expect(await mounted!.evaluate((element) => element.isConnected && element.matches(":modal"))).toBe(true);
        await expect(dialog).toContainText(identity);
        if (dismissal === "Cancel") await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
        else await page.keyboard.press("Escape");
        await expect(dialog).toHaveCount(0);
        await expect(trigger).toBeFocused();
        await openTaskList(page);
        await expect(model).toHaveValue("next-model");
        await openChat(page);
        await expect(field(page)).toHaveValue("Keep my message draft");
        expect(posts).toEqual([]);
        expect(typed(relay)).toBe("");
        await openTaskList(page);
        await trigger.click();
        const action = kind === "forget" ? "Forget" : dismissal === "Cancel" ? "Stop and delete" : "Pause instead (Recommended)";
        await dialog.getByRole("button", { name: action, exact: true }).click();
        await expect(dialog).toHaveCount(0);
        await expect.poll(() => posts.length).toBe(1);
        expect(posts[0]).toMatchObject({ path: kind === "forget" ? "/api/browser-logins/forget" : "/api/tasks/lifecycle", body: {
          task: GOBLIN.id, generation: GOBLIN.generation,
          ...(kind === "forget" ? { login: LOGIN.id } : { action: dismissal === "Cancel" ? "stop" : "pause" }),
        } });
      });
    }
  }
}

for (const action of ["save", "run"] as const) {
  for (const width of [800, 1000]) {
    test(`credential ${action} confirmation stays modal across ${width}px to ${1800 - width}px`, async ({ page }) => {
      const { relay, posts } = await preparePanel(page, "goblin", { credentials: [{
        id: "cred-review", generation: "credential-generation", identity: "credential-identity", by: "goblin", task: GOBLIN.id,
        project: GOBLIN.project, names: ["REVIEW_VALUE"], existing: ["REVIEW_VALUE"], state: "open", created_at: at,
      }] });
      await resizePanel(page, width);
      if (width === 800) await openChat(page);
      await openItem(page, "Paste REVIEW_VALUE for code-goblins");
      const card = page.locator('[data-credential-request="cred-review"]');
      const draft = card.getByLabel("Value for REVIEW_VALUE");
      const value = "draft-" + crypto.randomUUID();
      await draft.fill(value);
      const trigger = card.getByRole("button", { name: action === "save" ? "Store REVIEW_VALUE" : "Run in a terminal on this PC", exact: true });
      const dialog = page.getByRole("dialog", { name: "Replace a stored credential?" });

      for (const dismissal of ["Cancel", "Escape"]) {
        await resizePanel(page, width);
        await trigger.click();
        await expectModal(page, dialog);
        await resizePanel(page, 1800 - width);
        await expectModal(page, dialog);
        await expect(dialog).toContainText("REVIEW_VALUE already has a value for code-goblins");
        if (dismissal === "Cancel") await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
        else await page.keyboard.press("Escape");
        await expect(dialog).toHaveCount(0);
        await expect(trigger).toBeFocused();
        await expect(card).toBeVisible();
        await expect(draft).toHaveValue("•".repeat(value.length));
        expect(posts).toEqual([]);
        expect(typed(relay)).toBe("");
      }
      await trigger.click();
      await dialog.getByRole("button", { name: action === "save" ? "Replace and save" : "Replace and run" }).click();
      await expect(dialog).toHaveCount(0);
      await expect.poll(() => posts).toEqual([{ path: action === "save" ? "/api/credentials/save" : "/api/credentials/terminal", body: {
        id: "cred-review", generation: "credential-generation", replace: ["REVIEW_VALUE"],
        ...(action === "save" ? { values: { REVIEW_VALUE: value } } : {}),
      } }]);
      await expect(page.locator("dialog.credential-replace")).toHaveCount(0);
    });
  }
}

for (const kind of ["stop", "forget"]) {
  test(`${kind}'s portal is removed when its task generation leaves`, async ({ page }) => {
    const { relay, posts } = await preparePanel(page);
    if (kind === "forget") await page.locator("details.connections > summary").click();
    const trigger = kind === "stop" ? page.locator(".task-controls").getByRole("button", { name: /^Stop / }) : page.getByRole("button", { name: /^Forget Stripe/ });
    await trigger.click();
    const dialog = page.getByRole("dialog", { name: kind === "stop" ? "Stop this task?" : "Forget this login?", exact: true });
    await expectModal(page, dialog);
    const mounted = await dialog.elementHandle();

    await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({}, { generation: "replacement-generation" }));

    await expect(dialog).toHaveCount(0);
    expect(await mounted!.evaluate((element) => element.isConnected)).toBe(false);
    await expect(page.locator("dialog:modal")).toHaveCount(0);
    await openTaskList(page);
    await expect(page.locator(".task-controls")).toBeVisible();
    expect(posts).toEqual([]);
    expect(typed(relay)).toBe("");
  });
}

test("a queued task's portalled Remove confirmation cancels safely and removes only on confirmation", async ({ page }) => {
  await open(page, snapshot({}, { phase: "queued", generation: "", queue_revision: "queued-revision" }));
  const posts: unknown[] = [];
  await page.route("**/api/tasks/lifecycle", (route) => {
    posts.push(route.request().postDataJSON());
    return route.fulfill({ json: { revision: 2 } });
  });
  await page.locator(".task-board .task-card").filter({ hasText: "Zeke" }).click();
  const trigger = page.locator(".task-controls").getByRole("button", { name: /^Remove / });
  const dialog = page.getByRole("dialog", { name: "Remove this task?", exact: true });

  for (const dismissal of ["Cancel", "Escape"]) {
    await trigger.click();
    await expect(dialog.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
    await dialog.getByRole("button", { name: "Remove from queue" }).focus();
    await expectModal(page, dialog);
    if (dismissal === "Cancel") await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    else await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(trigger).toBeFocused();
    expect(posts).toEqual([]);
  }
  await trigger.click();
  await dialog.getByRole("button", { name: "Remove from queue" }).click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => posts).toEqual([{ task: GOBLIN.id, generation: "", revision: "queued-revision", action: "stop", operation: expect.any(String) }]);
});
