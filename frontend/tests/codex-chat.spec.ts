import { expect, test } from "./site";
import { at, card, field, GOBLIN, NAME, open, openGoblin, PAGE, pasted, snapshot, typed } from "./session-board";

test.use({ launchOptions: { args: ["--disable-webgl"] } });

for (const who of ["CFO", "goblin"] as const) {
  test(`Codex ${who} reads and updates Modern chat without hooks or a reload`, async ({ page }, testInfo) => {
    const data = snapshot({ cfo_harness: "codex" }, { harness: "codex", model: "gpt-6", runtime: { state: "working", at } });
    const relay = await open(page, data);
    if (who === "goblin") await openGoblin(page);
    else await page.keyboard.press("Control+Alt+1");
    const name = who === "CFO" ? "CFO" : NAME;
    const log = page.getByRole("log", { name: "Messages of " + name, exact: true });
    await expect(log).toContainText("On it.");
    await field(page, name).fill("continue from this draft");

    await page.evaluate((at) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: [
      { id: "m2", seq: 2, at, kind: "reply", blocks: [
        { kind: "thinking", text: "A published **summary**.\n\n- Read the file.\n- Keep its order." },
        { kind: "call", tool: "exec_command", subject: "git status --short", state: "done", result: " M frontend/src/App.tsx" },
        { kind: "call", tool: "apply_patch", subject: "src/example.ts", state: "done", edit: { added: 1, removed: 1, diff: ["-old", "+new"] } },
        { kind: "text", text: "The latest reply arrived." },
      ] },
    ] } })), at);
    await expect(log).toContainText("The latest reply arrived.");
    await expect(log.locator(".message-thinking-read li")).toHaveText(["Read the file.", "Keep its order."]);
    await log.getByText("src/example.ts", { exact: true }).click();
    await expect(log.locator(".message-diff")).toContainText("+new");
    await expect(field(page, name)).toHaveValue("continue from this draft");

    await field(page, name).press("Enter");
    await expect.poll(() => typed(relay)).toContain(pasted("continue from this draft") + "\r");
    relay.drop();
    await expect.poll(() => relay.views).toBe(2);
    await expect(log).toContainText("The latest reply arrived.");
    await expect(field(page, name)).toBeInViewport();

    await testInfo.attach(`codex-${who}-desktop.png`, { body: await page.screenshot(), contentType: "image/png" });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(field(page, name)).toBeInViewport();
    await testInfo.attach(`codex-${who}-phone.png`, { body: await page.screenshot(), contentType: "image/png" });
    await page.reload();
    if (who === "goblin") await openGoblin(page);
    else await page.keyboard.press("Control+Alt+1");
    await expect(log).toContainText("On it.");
  });
}

test("Codex messages keep scrolling and a gate-held draft is durable without terminal input", async ({ page }) => {
  const relay = await open(page, snapshot({}, { harness: "codex" }), "", {
    messages: Array.from({ length: 45 }, (_, seq) => ({ ...PAGE.messages[0], id: "m" + seq, seq, text: "History " + seq + ": " + "readable text ".repeat(40) })), more: false,
  });
  await openGoblin(page);
  const conversation = page.locator(".conversation");
  await conversation.evaluate((element) => { element.scrollTop = 0; });
  await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
  await expect(field(page)).toBeInViewport();
  relay.say({ type: "held", reason: "pipeline owns this task" });
  const actions: object[] = [];
  await page.route("**/api/actions", (route) => { actions.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });
  await field(page).fill("do this after the gate");
  await field(page).press("Enter");
  await expect.poll(() => actions.length).toBe(1);
  expect(actions[0]).toMatchObject({ kind: "message", task_id: GOBLIN.id, text: "do this after the gate" });
  expect(typed(relay)).toBe("");
});

test("Codex Classic keeps drawing new output on the same connection", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page, snapshot({}, { harness: "codex" }));
  await card(page, NAME).click();
  const terminal = page.getByRole("textbox", { name: "Terminal input", exact: true });
  await expect(terminal).toBeVisible();
  relay.write("\r\nCodex continued on the same live view\r\n");
  await expect(page.locator(".terminal-view:not(.staged) > .xterm .xterm-rows")).toContainText("Codex continued on the same live view");
  await terminal.press("Shift+Tab");
  await expect.poll(() => typed(relay)).toBe("\x1b[Z");
  expect(relay.views).toBe(1);
});
