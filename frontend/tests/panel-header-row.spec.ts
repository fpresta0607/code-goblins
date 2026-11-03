import { expect, test, type Locator, type Page } from "./site";
import { NAME, open, openGoblin, pressRow, snapshot } from "./session-board";

// The Overlord, 2026-10-29: "Minimize height and maximize the space", and of
// the Chat and Task switch, "also put the switch in the middle less wide". A
// panel's header is one short row: the session's small picture and its name
// at the left, the switch in the middle and only as wide as its two icons,
// and the panel's controls at the right, led on the CFO's by the AFK switch.
// He kept the picture ("I like the avatar and the icon") and moved the last
// AFK report into the Task view ("could be something that you know is part
// of like the task panel"), and the harness's mark moved there too, as the
// harness, model and effort in words (#1987). No status line: the working
// line in the conversation says what the session does.

const ROW_PX = 56;
const REPORT = "/api/afk/report/index.html";
const header = (page: Page) => page.locator(".goblin-panel > .panel-header");

async function openCfo(page: Page) {
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await expect(page.locator("#panel-title")).toHaveText("CFO");
}

async function box(part: Locator) {
  await expect(part).toBeVisible();
  return (await part.boundingBox())!;
}

for (const width of [1440, 393, 360]) {
  for (const who of ["goblin", "CFO"]) {
    test(`${who}'s panel header at ${width}px is one short row with the switch in or near its middle`, async ({ page }, testInfo) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      await open(page, snapshot({ afk: { state: "off", page: REPORT } }));

      // Act
      if (who === "goblin") await openGoblin(page);
      else await openCfo(page);

      // Assert: one row, no taller than a control and its padding, with no
      // separate row above it or under it.
      const row = await box(header(page));
      expect(row.height).toBeLessThanOrEqual(ROW_PX);
      await expect(page.locator(".goblin-panel > .panel-top")).toHaveCount(0);
      await expect(header(page).locator(".panel-status")).toHaveCount(0);
      const parts = [".goblin-avatar", "#panel-title", ".panel-view-switch", ".panel-corner"];
      if (who === "CFO") parts.push(".afk-header");
      for (const part of parts) {
        const each = await box(header(page).locator(part));
        const middle = each.y + each.height / 2;
        expect(middle, part + " in the row").toBeGreaterThan(row.y);
        expect(middle, part + " in the row").toBeLessThan(row.y + row.height);
        expect(each.x + each.width, part + " inside the screen").toBeLessThanOrEqual(width);
      }
      expect(await header(page).evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);

      // Assert: the switch is only as wide as its two icons, between the
      // name and the controls, in the middle while both sides leave room,
      // and otherwise as near it as the controls leave room for, right
      // against them. In the panel's default width beside the board the
      // picture offers the chat style and each row shows every control, the
      // CFO's beside its AFK switch with its name whole (#1987).
      const views = header(page).locator(".panel-view-switch");
      const switchBox = await box(views);
      const icons = await views.getByRole("button").evaluateAll((buttons) => buttons.reduce((sum, button) => sum + button.getBoundingClientRect().width, 0));
      expect(switchBox.width).toBeLessThanOrEqual(icons + 8 + 2 + 1);
      const identity = await box(header(page).locator(".panel-identity"));
      const controls = await box(header(page).locator(".panel-top"));
      expect(switchBox.x).toBeGreaterThanOrEqual(identity.x + identity.width);
      expect(switchBox.x + switchBox.width).toBeLessThanOrEqual(controls.x);
      const offset = switchBox.x + switchBox.width / 2 - (row.x + row.width / 2);
      const gap = await header(page).evaluate((element) => parseFloat(getComputedStyle(element).columnGap));
      const isAgainstControls = Math.abs(controls.x - (switchBox.x + switchBox.width) - gap) <= 1;
      expect(Math.abs(offset) <= 2 || (offset < 0 && isAgainstControls), `the switch ${offset} px from the middle`).toBe(true);
      if (width === 1440) {
        await expect(header(page).getByRole("button", { name: "Chat style", exact: true })).toBeVisible();
        if (who === "CFO") expect(await header(page).locator("#panel-title").evaluate((title) => title.scrollWidth - title.clientWidth)).toBe(0);
        await expect(header(page).getByRole("button", { name: "More", exact: true })).toHaveCount(0);
      }
      await testInfo.attach(`header-${who}-${width}.png`, { body: await page.screenshot({ clip: { x: row.x, y: row.y, width: row.width, height: row.height } }), contentType: "image/png" });
    });
  }
}

// The session's picture opens its chat style menu (#1987). The header's
// identity cuts what overflows it, so the picture's focus ring, its outline
// grown out by its width and offset, lies inside the identity, and the menu
// hangs from the header instead, drawn whole and over the panel, maximized or
// not.
for (const who of ["CFO", "goblin"] as const) {
  test(`the ${who}'s picture draws its focus ring inside the identity and opens its chat style menu whole, maximized or not`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width: 1440, height: 900 });
    await open(page);
    // Opened from the keyboard, so the picture's focus draws its ring.
    await page.keyboard.press(who === "CFO" ? "Control+Alt+1" : "Control+Alt+2");
    await expect(page.locator("#panel-title")).toHaveText(who === "CFO" ? "CFO" : NAME);
    const picture = header(page).getByRole("button", { name: "Chat style", exact: true });

    // Act
    await picture.focus();

    // Assert
    const ring = await picture.evaluate((button) => {
      const style = getComputedStyle(button);
      const grown = parseFloat(style.outlineWidth) + parseFloat(style.outlineOffset);
      const box = button.getBoundingClientRect(), identity = button.closest(".panel-identity")!.getBoundingClientRect();
      return {
        isFocusVisible: button.matches(":focus-visible"), outline: style.outlineStyle, width: box.width, height: box.height,
        past: Math.max(identity.left - (box.left - grown), identity.top - (box.top - grown), box.right + grown - identity.right, box.bottom + grown - identity.bottom),
      };
    });
    expect(ring.isFocusVisible).toBe(true);
    expect(ring.outline).toBe("solid");
    expect(ring.past).toBeLessThanOrEqual(0);
    expect(Math.min(ring.width, ring.height), "a 44 px target").toBeGreaterThanOrEqual(44);
    await expect(picture.locator(".goblin-avatar")).toBeVisible();

    for (const isMaximized of [false, true]) {
      // Act
      if (isMaximized) await pressRow(page, "Maximize the panel", "Maximize");
      await picture.click();

      // Assert: every item is drawn whole, and on top at its corners, each
      // probed inside its rounding, which a press there does not reach.
      const items = header(page).getByRole("menuitemradio");
      await expect(items).toHaveText(["Modern chat", "Terminal"]);
      for (const item of await items.all()) {
        const isOnTop = await item.evaluate((element) => {
          const box = element.getBoundingClientRect(), inset = parseFloat(getComputedStyle(element).borderTopLeftRadius) + 1;
          return [[box.left + inset, box.top + inset], [box.right - inset, box.bottom - inset]].every(([x, y]) => element.contains(document.elementFromPoint(x, y)));
        });
        expect(isOnTop, (await item.innerText()) + (isMaximized ? " maximized" : "")).toBe(true);
      }
      await page.keyboard.press("Escape");
      await expect(items).toHaveCount(0);
    }
  });
}

// Where the row is narrowest, the CFO's panel dragged to its narrowest beside
// the board and a phone, in both chat styles and with an update waiting or
// not, what always shows still fits: the picture stays whole, the name is
// cut at the identity's own edge and never drawn under the switch, every
// control of the row, AFK and Close or Back with them, lies in
// the panel, and Update takes the line under the row.
const UPDATE = { harness: "claude", line: "✓ Update installed · Restart to update", installed: "2026-10-29T08:00:00Z", pending: true };
const NARROWEST = [
  { where: "a 360 px panel beside the board", viewport: { width: 1440, height: 900 }, pane: 360 },
  { where: "a 360 px phone", viewport: { width: 360, height: 800 }, pane: 0 },
  { where: "a 390 px phone", viewport: { width: 390, height: 844 }, pane: 0 },
];
for (const { where, viewport, pane } of NARROWEST) {
  for (const style of ["classic", "modern"]) {
    for (const isUpdateWaiting of [false, true]) {
      test(`the CFO's header row on ${where} in ${style}${isUpdateWaiting ? " with an update waiting" : ""} keeps its picture whole, its name off the switch, and every control in the panel`, async ({ page }) => {
        // Arrange
        await page.setViewportSize(viewport);
        await page.addInitScript(({ style, pane }) => {
          localStorage.setItem("cfo-chat-style", style);
          if (pane) localStorage.setItem("cfo-pane-width", String(pane));
        }, { style, pane });
        await open(page, snapshot(isUpdateWaiting ? { cfo_update: UPDATE } : {}));

        // Act
        await openCfo(page);

        // Assert: one row, and what the identity draws, cut at its edge
        // where it hides what overflows it, ends before the switch.
        const row = await box(header(page));
        expect(row.height).toBeLessThanOrEqual(ROW_PX);
        const drawn = await header(page).evaluate((element) => {
          const identity = element.querySelector(".panel-identity")!;
          const edge = identity.getBoundingClientRect();
          const isClipped = getComputedStyle(identity).overflowX !== "visible";
          return {
            identity: edge.right,
            picture: identity.querySelector(".goblin-avatar")!.getBoundingClientRect().right,
            views: element.querySelector(".panel-view-switch")!.getBoundingClientRect().left,
            parts: [...identity.children].map((part) => ({ part: part.id || part.className, right: isClipped ? Math.min(part.getBoundingClientRect().right, edge.right) : part.getBoundingClientRect().right })),
          };
        });
        expect(drawn.picture, "the picture is whole").toBeLessThanOrEqual(drawn.identity + 0.5);
        for (const { part, right } of drawn.parts) expect(right, part + " ends before the switch").toBeLessThanOrEqual(drawn.views + 0.5);

        // Assert: every control of the row lies in the panel.
        const panel = await box(page.locator(".context-pane"));
        const inPanel = async (control: Locator) => {
          const each = await box(control);
          const name = await control.evaluate((element) => element.getAttribute("aria-label") || element.textContent);
          expect(each.x, name + " in the panel").toBeGreaterThanOrEqual(panel.x - 0.5);
          expect(each.x + each.width, name + " in the panel").toBeLessThanOrEqual(panel.x + panel.width + 0.5);
          expect(each.y + each.height, name + " in the panel").toBeLessThanOrEqual(panel.y + panel.height + 0.5);
        };
        await expect(header(page).locator(".panel-corner button")).toBeVisible();
        await expect(header(page).getByRole("switch", { name: "AFK mode", exact: true })).toBeVisible();
        for (const control of await header(page).locator(".panel-top button:visible").all()) await inPanel(control);

        // Assert: Update is on the line under the row, in the panel.
        if (isUpdateWaiting) {
          const update = page.locator(".cfo-header-line").getByRole("button", { name: "Cancel the Claude Code update", exact: true });
          expect((await box(update)).y).toBeGreaterThanOrEqual(row.y + row.height);
          await inPanel(update);
        }
      });
    }
  }
}

// While his press of AFK waits on the supervisor the switch spins in its
// track, keeping the width the row was fitted to, so at the narrowest rows
// Back on a phone and Close beside the board stay in the panel.
const PRESSED = [
  { where: "a 360 px phone", viewport: { width: 360, height: 800 }, pane: 0, corner: /^Back to / },
  { where: "a 360 px panel beside the board", viewport: { width: 1440, height: 900 }, pane: 360, corner: /^Close panel$/ },
];
for (const { where, viewport, pane, corner } of PRESSED) {
  test(`on ${where} in classic, the CFO's AFK switch keeps its width while his press waits, and the corner button stays in the panel`, async ({ page }) => {
    // Arrange: AFK on, and the supervisor's answer to his press held.
    await page.setViewportSize(viewport);
    await page.addInitScript((pane) => {
      localStorage.setItem("cfo-chat-style", "classic");
      if (pane) localStorage.setItem("cfo-pane-width", String(pane));
    }, pane);
    await open(page, snapshot({ afk: { state: "on", since: new Date().toISOString(), from: "his own board", decided: 0, held: [] } }));
    let answer = () => {};
    const answered = new Promise<void>((resolve) => { answer = resolve; });
    await page.route("**/api/afk", async (route) => {
      await answered;
      await route.fulfill({ json: { state: "off" } });
    });
    await openCfo(page);
    const afk = header(page).getByRole("switch", { name: "AFK mode", exact: true });
    await expect(afk).toHaveAttribute("aria-checked", "true");
    const resting = await box(afk);

    // Act
    await afk.click();

    // Assert
    await expect(afk).toHaveAttribute("aria-busy", "true");
    expect((await box(afk)).width).toBe(resting.width);
    const button = header(page).locator(".panel-corner button");
    await expect(button).toHaveAccessibleName(corner);
    const panel = await box(page.locator(".context-pane"));
    const each = await box(button);
    expect(each.x).toBeGreaterThanOrEqual(panel.x - 0.5);
    expect(each.x + each.width).toBeLessThanOrEqual(panel.x + panel.width + 0.5);
    answer();
  });
}

// What a press in the CFO's header could not do lies over the conversation
// from the row's right end, and on the 360 px panel beside the board it is cut
// to the panel's room, so neither its border nor its first words are hidden
// at the panel's left edge.
test("on a 360 px panel beside the board, Restart PC's notice and the AFK refusal lie inside the panel", async ({ page }) => {
  // Arrange: AFK on, a restart that stopped short, and AFK's press refused.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem("cfo-pane-width", "360"));
  await open(page, snapshot({
    afk: { state: "on", since: new Date().toISOString(), from: "his own board", decided: 0, held: [] },
    pc_restart: { state: "stopped", problem: "not every working goblin paused", at: new Date().toISOString() },
  }));
  await page.route("**/api/afk", (route) => route.fulfill({ status: 403, json: { error: "Switch AFK in the Code Goblins window, or run cfo afk on in PowerShell." } }));
  await openCfo(page);
  const panel = await box(page.locator(".context-pane"));
  const inPanel = async (notice: Locator, what: string) => {
    const each = await box(notice);
    expect(each.x, what + " in the panel").toBeGreaterThanOrEqual(panel.x - 0.5);
    expect(each.x + each.width, what + " in the panel").toBeLessThanOrEqual(panel.x + panel.width + 0.5);
  };
  await inPanel(page.locator(".cfo-header-line > .notice"), "Restart PC's notice");

  // Act
  await header(page).getByRole("switch", { name: "AFK mode", exact: true }).click();

  // Assert
  await inPanel(header(page).getByRole("alert"), "the AFK refusal");
});

test("a goblin's name is cut short in the header row, never wrapped, and shows whole in its Task view", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 360, height: 900 });
  await open(page);
  await openGoblin(page);
  const title = header(page).locator("#panel-title");

  // Assert: the row cuts the name rather than wrap it onto a second line.
  await expect(title).toHaveCSS("text-overflow", "ellipsis");
  await expect(title).toHaveCSS("white-space", "nowrap");

  // Act
  await header(page).getByRole("button", { name: "Task", exact: true }).click();

  // Assert
  await expect(page.locator(".goblin-panel > .panel-task .panel-whole-name")).toHaveText(NAME + " · cg-touch");
  await expect(page.locator(".goblin-panel > .panel-task .panel-status")).toBeVisible();
});

test("the CFO's last AFK report opens from its Task view, not from the header", async ({ page }) => {
  // Arrange
  await open(page, snapshot({ afk: { state: "off", page: REPORT } }));
  await openCfo(page);
  const report = page.getByRole("button", { name: "Open your last AFK report", exact: true });
  await expect(header(page).getByRole("switch", { name: "AFK mode", exact: true })).toBeVisible();
  await expect(report).toHaveCount(0);

  // Act
  await header(page).getByRole("button", { name: "Task", exact: true }).click();

  // Assert
  await expect(page.locator(".goblin-panel > .panel-task").getByRole("button", { name: "Open your last AFK report", exact: true })).toBeVisible();
  await expect(header(page).getByRole("button", { name: "Open your last AFK report", exact: true })).toHaveCount(0);
});

test("the divider beside the panel is drawn before the pointer reaches it", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);
  const divider = page.getByRole("separator", { name: "Resize the panel", exact: true });
  await page.mouse.move(0, 0);

  // Act
  const handle = await divider.evaluate((element) => {
    const style = getComputedStyle(element, "::after");
    return { opacity: style.opacity, height: style.height };
  });

  // Assert
  expect(handle).toEqual({ opacity: "1", height: "56px" });
});
