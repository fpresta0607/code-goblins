import { expect, openTaskList, test, type Page } from "./site";
import { box, card, field, GOBLIN, open, openGoblin, OTHER, pasted, pickStyle, snapshot, typed } from "./session-board";

// watchTerminals records each terminal connection the page opens from now on
// until it is closed, over the fixture's own WebSocket, which open() installs,
// and returns what reads them.
async function watchTerminals(page: Page) {
  await page.evaluate(() => {
    const Native = window.WebSocket;
    const live = new Set<WebSocket>();
    Object.assign(window, { liveTerminals: () => [...live].map((socket) => new URL(socket.url).searchParams.toString()) });
    window.WebSocket = class extends Native {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols);
        live.add(this);
        this.addEventListener("close", () => live.delete(this));
      }
      close(code?: number, reason?: string) { live.delete(this); super.close(code, reason); }
    };
  });
  return () => page.evaluate(() => (window as unknown as { liveTerminals: () => string[] }).liveTerminals());
}
const GOBLIN_TERMINAL = new URLSearchParams({ task: GOBLIN.id, generation: GOBLIN.generation, token: "fixture" }).toString();
const CFO_TERMINAL = new URLSearchParams({ cfo: "cfo-term", token: "fixture" }).toString();

for (const { harness, isBracketed } of ["claude", "codex", "pi"].flatMap((harness) => [true, false].map((isBracketed) => ({ harness, isBracketed })))) {
  test(`${harness} Chat sends and answers harness controls before mounting a terminal, bracketed paste ${isBracketed ? "on" : "off"}`, async ({ page }) => {
    // Arrange
    const relay = await open(page, snapshot({}, { harness }), "", undefined, isBracketed ? "> ready\r\n" : "\x1b[?2004l> ready\r\n");
    const queued: { kind: string; text: string }[] = [];
    await page.route("**/api/actions", (route) => { queued.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: {} }); });
    await openGoblin(page);

    // Act
    await field(page).fill("Read the brief\r\nSecond line\nThird line \x1b[201~ stays in the paste");
    await field(page).press("Enter");

    // Assert
    const text = "Read the brief\rSecond line\rThird line  stays in the paste";
    const expected = isBracketed ? pasted(text) : text;
    await expect.poll(() => relay.typed).toEqual([expected, "\r"]);
    expect(relay.submissions).toHaveLength(1);
    await expect(page.locator(".xterm")).toHaveCount(0);
    await expect(field(page)).toHaveValue("");

    // Act
    await field(page).press("Shift+Tab");
    await expect.poll(() => typed(relay)).toBe(expected + "\r\x1b[Z");
    relay.say({ type: "screen", asking: false, open: false, working: false, draft: true });
    await expect(box(page)).toHaveAttribute("data-queues", "");
    const before = typed(relay);
    await field(page).fill("Keep this separate from the native draft");
    await field(page).press("Enter");

    // Assert
    await expect.poll(() => queued.map((message) => message.text)).toEqual(["Keep this separate from the native draft"]);
    expect(typed(relay)).toBe(before);
    await expect(field(page)).toHaveValue("");
    await expect(page.locator(".xterm")).toHaveCount(0);
  });
}

test("showing a terminal and returning to Chat keep one connection and stop its hidden animation", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  const connections = relay.views;
  await expect(page.locator(".xterm")).toHaveCount(0);

  // Act
  await pickStyle(page, "Terminal");
  await expect(page.locator(".xterm").first()).toBeVisible();
  await pickStyle(page, "Modern chat");

  // Assert
  await expect(page.locator(".xterm")).toHaveCount(0);
  expect(relay.views).toBe(connections);
  const spinners = await page.evaluate(() => document.getAnimations().filter((animation) => animation.playState === "running" && (animation as CSSAnimation).animationName === "terminal-spin").length);
  expect(spinners).toBe(0);
  await field(page).fill("Continue after switching back");
  await field(page).press("Enter");
  await expect.poll(() => typed(relay)).toBe(pasted("Continue after switching back") + "\r");
});

test("Chat sends through a replacement terminal hidden before its first size claim", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  await pickStyle(page, "Terminal");
  await expect(page.locator(".terminal-view:not(.staged) > .xterm")).toBeVisible();
  // Modern chat is chosen in the same turn the replacement view opens its
  // surface in the shown terminal, so its connection cannot open and claim a
  // size first.
  await page.locator(".context-pane .panel-header").getByRole("button", { name: "Chat style", exact: true }).click();
  await page.evaluate(() => {
    const observer = new MutationObserver(() => {
      if (document.querySelectorAll(".xterm").length < 2) return;
      observer.disconnect();
      [...document.querySelectorAll<HTMLElement>("[role=menuitemradio]")].find((item) => item.textContent?.trim() === "Modern chat")?.click();
    });
    observer.observe(document.body, { childList: true, subtree: true });
  });

  // Act
  relay.drop();
  await expect.poll(() => relay.views).toBe(2);
  await expect(page.locator(".xterm")).toHaveCount(0);
  await expect(page.locator(".terminal-view")).toHaveCount(1);
  await field(page).fill("Continue after the replacement");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => relay.typed).toEqual([pasted("Continue after the replacement"), "\r"]);
  expect(relay.submissions).toHaveLength(1);
  await expect(page.locator(".xterm")).toHaveCount(0);
});

for (const isCfo of [true, false]) {
  for (const completion of ["repaint", "fallback"] as const) {
    test(`${isCfo ? "CFO" : "goblin"} terminal reopens after its first ${completion} completes while hidden`, async ({ page }) => {
      const relay = await open(page, snapshot(), "", undefined, "> ready\r\n", false);
      if (isCfo) await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
      await page.evaluate(() => document.fonts.ready);
      const connections = relay.views;
      const now = new Date("2026-10-27T12:00:05Z");
      await page.clock.install({ time: now });
      // The installed clock runs until it pauses, so it pauses a second on,
      // never at a moment a loaded machine has already passed.
      await page.clock.pauseAt(new Date(now.getTime() + 1000));

      await pickStyle(page, "Terminal");
      await page.clock.runFor(200);
      await expect.poll(() => relay.resizes.length).toBeGreaterThan(0);
      await expect(page.locator(".terminal-view.staged")).toHaveCount(1);
      await pickStyle(page, "Modern chat");
      await expect(page.locator(".xterm")).toHaveCount(0);
      if (completion === "repaint") relay.write("First repaint complete\r\n");
      await page.clock.runFor(800);
      const sizes = relay.resizes.length;
      await pickStyle(page, "Terminal");
      await page.clock.runFor(200);

      const input = page.getByRole("textbox", { name: "Terminal input", exact: true });
      await expect(page.locator(".xterm")).toBeVisible();
      await expect(input).toBeFocused();
      await page.keyboard.type("x");
      await expect.poll(() => typed(relay)).toBe("x");
      expect(relay.resizes).toHaveLength(sizes);
      expect(relay.views).toBe(connections);
    });
  }

  test(`${isCfo ? "CFO" : "goblin"} terminal first opens its replayed screen after the connection closes`, async ({ page }) => {
    const relay = await open(page);
    if (isCfo) await page.keyboard.press("Control+Alt+1");
    else await openGoblin(page);
    await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
    const connections = relay.views;

    relay.end();
    await expect(page.locator(".terminal-foot.box")).toHaveText("No terminal is running for this task.");
    await pickStyle(page, "Terminal");

    await expect(page.locator(".xterm")).toBeVisible();
    await expect(page.locator(".terminal-view.staged")).toHaveCount(0);
    await expect(page.locator(".terminal-foot.box")).toBeVisible();
    await expect(page.locator(".terminal-connecting")).toHaveCount(0);
    expect(relay.resizes).toEqual([]);
    expect(relay.views).toBe(connections);
  });
}

test("Chat consumes and acknowledges a large native replay without mounting or resizing a terminal", async ({ page }) => {
  // Arrange
  const output = "A line from native history\r\n".repeat(4000);
  const relay = await open(page, snapshot(), "", undefined, output);
  await openGoblin(page);

  // Act
  await field(page).fill("Continue after replay");
  await field(page).press("Enter");

  // Assert
  await expect.poll(() => relay.acknowledged.at(-1)).toBe(Buffer.byteLength("\x1b[?2004h" + output));
  await expect.poll(() => relay.typed).toEqual([pasted("Continue after replay"), "\r"]);
  expect(relay.submissions).toHaveLength(1);
  expect(relay.resizes).toEqual([]);
  await expect(page.locator(".xterm")).toHaveCount(0);
});

test("Chat follows a harness change on the same native connection", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await openGoblin(page);
  const connections = relay.views;
  let expected = "";

  for (const [index, harness] of ["codex", "pi", "claude"].entries()) {
    // Act
    const next = snapshot({ revision: index + 2 }, { harness, model: "" });
    await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);
    await expect(box(page).getByRole("img", { name: harness === "codex" ? "Codex" : harness === "pi" ? "Pi" : "Claude Code", exact: true })).toBeVisible();
    await field(page).fill("Message to " + harness);
    await field(page).press("Enter");

    // Assert
    expected += pasted("Message to " + harness) + "\r";
    await expect.poll(() => typed(relay)).toBe(expected);
    expect(relay.submissions).toHaveLength(index + 1);
    expect(relay.views).toBe(connections);
    await expect(page.locator(".xterm")).toHaveCount(0);
  }
});

// The Overlord, 2026-11-04: a session he left kept its terminal live behind
// the one he reads, connected and offering its box a link. Only the session
// the panel shows has a terminal, so leaving one closes its connection, and
// coming back connects afresh and types into the new connection alone.
test("a session he leaves keeps no terminal connection, and coming back connects afresh", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const liveTerminals = await watchTerminals(page);
  await openGoblin(page);
  const views = relay.views;

  // Act
  await card(page, OTHER.goblin_name + " - " + OTHER.goblin_title).click();
  await expect(page.locator("#panel-title")).toHaveText(OTHER.goblin_name + " - " + OTHER.goblin_title);
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);

  // Assert
  await expect.poll(liveTerminals).toEqual([new URLSearchParams({ task: OTHER.id, generation: OTHER.generation, token: "fixture" }).toString()]);
  await expect(page.locator(".host-terminal")).toHaveCount(1);

  // Act
  await page.keyboard.press("Control+Alt+1");
  await expect(page.locator("#panel-title")).toHaveText("CFO");

  // Assert
  await expect.poll(liveTerminals).toEqual([CFO_TERMINAL]);

  // Act
  await openGoblin(page);
  await field(page).fill("Back to Zeke");
  await field(page).press("Enter");

  // Assert
  expect(relay.views, "coming back connects afresh").toBeGreaterThan(views + 1);
  await expect.poll(liveTerminals).toEqual([GOBLIN_TERMINAL]);
  await expect.poll(() => relay.inputs.map((input) => new URL(input.address).searchParams.get("task") + " " + input.text)).toEqual(["cg-touch " + pasted("Back to Zeke"), "cg-touch \r"]);
});

// The CFO, decision 1016436: a panel that closes ends its terminal. No stream
// stays open behind a closed panel, on the Board or for the CFO in
// Orchestration, and opening the panel connects afresh.
for (const view of ["Board", "Orchestration"] as const) {
  test(`closing the CFO's panel on ${view} ends its terminal connection, and opening it connects afresh`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    const liveTerminals = await watchTerminals(page);
    if (view === "Orchestration") await page.getByRole("button", { name: "Orchestration", exact: true }).click();
    else await page.keyboard.press("Control+Alt+1");
    await expect(page.locator("#panel-title")).toHaveText("CFO");
    await expect.poll(liveTerminals).toEqual([CFO_TERMINAL]);
    const views = relay.views;

    // Act
    await page.getByRole("button", { name: "Close panel", exact: true }).click();

    // Assert
    await expect.poll(liveTerminals).toEqual([]);
    await expect(page.locator(".host-terminal")).toHaveCount(0);

    // Act
    await page.getByRole("button", { name: "Open CFO", exact: true }).click();

    // Assert
    await expect.poll(liveTerminals).toEqual([CFO_TERMINAL]);
    expect(relay.views, "opening it connects afresh").toBe(views + 1);
  });
}

test("Back on a phone ends the goblin's terminal connection, and his draft waits in the box when it opens again", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  const relay = await open(page);
  const liveTerminals = await watchTerminals(page);
  await openGoblin(page);
  await expect.poll(liveTerminals).toEqual([GOBLIN_TERMINAL]);
  await field(page).fill("Keep this until I am back");
  const views = relay.views;

  // Act
  await page.getByRole("button", { name: "Back to Board", exact: true }).click();

  // Assert
  await expect.poll(liveTerminals).toEqual([]);
  await expect(page.locator(".host-terminal")).toHaveCount(0);

  // Act
  await openGoblin(page);

  // Assert
  await expect.poll(liveTerminals).toEqual([GOBLIN_TERMINAL]);
  expect(relay.views, "opening it connects afresh").toBe(views + 1);
  await expect(field(page)).toHaveValue("Keep this until I am back");
});

test("the Task view of the session he reads keeps its one terminal connection", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  const liveTerminals = await watchTerminals(page);
  await openGoblin(page);
  await expect.poll(liveTerminals).toEqual([GOBLIN_TERMINAL]);
  const views = relay.views;

  // Act
  await openTaskList(page);
  await page.locator(".panel-view-switch").getByRole("button", { name: "Chat", exact: true }).click();
  await expect(field(page)).toBeVisible();

  // Assert
  expect(await liveTerminals()).toEqual([GOBLIN_TERMINAL]);
  expect(relay.views).toBe(views);
});
