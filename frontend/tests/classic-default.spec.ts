import { expect, test, type Page } from "./site";
import { box, card, field, NAME, open, OTHER, PAGE, pickStyle, snapshot, typed } from "./session-board";

// The board draws every conversation as Classic, the harness's own terminal,
// until Modern Chat is ready to be the default (the Overlord, 2026-10-28:
// "the classic terminal worked well ... It should be the default for now").
// These browsers saved no style of their own.
test.use({ chatStyle: null });

const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const header = (page: Page) => page.locator(".context-pane .panel-header");
const styleButton = (page: Page) => header(page).getByRole("button", { name: "Chat style", exact: true });
const saved = (page: Page) => page.evaluate(() => localStorage.getItem("cfo-chat-style"));

const IVY = OTHER.goblin_name + " - " + OTHER.goblin_title;

// classicShown says the panel draws the terminal as it was before Modern
// Chat: no box, no terminal copy button beside it,
// and the choice of style on the session's picture in the panel's header
// (#1987).
async function classicShown(page: Page, name: string): Promise<void> {
  await expect(terminalInput(page)).toBeVisible();
  await expect(box(page, name)).toBeHidden();
  await expect(page.getByRole("button", { name: "Copy terminal text", exact: true })).toHaveCount(0);
  await expect(styleButton(page)).toBeVisible();
}

// modernShown says the panel draws the conversation and its box, with the
// choice of style on the session's picture in the panel's header, and none
// in the box, since the header owns it (#1987).
async function modernShown(page: Page, name: string): Promise<void> {
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
  await expect(field(page, name)).toBeVisible();
  await expect(styleButton(page)).toBeVisible();
  await expect(box(page, name).getByRole("button", { name: /^Style: / })).toHaveCount(0);
  await expect(page.getByRole("group", { name: "Chat style", exact: true })).toHaveCount(0);
}

for (const who of ["CFO", "goblin"] as const) {
  test(`the ${who}'s panel opens in Classic with no saved choice, and the picture in its header switches it both ways at once`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    const name = who === "CFO" ? "CFO" : NAME;

    // Act
    if (who === "CFO") await page.keyboard.press("Control+Alt+1");
    else await card(page, NAME).click();

    // Assert: the terminal alone, and opening it saved nothing.
    await classicShown(page, name);
    expect(await saved(page)).toBeNull();

    // Act and Assert: twice each way, since a style chosen once must not keep
    // the next choice from showing.
    for (let round = 0; round < 2; round++) {
      await pickStyle(page, "Modern chat");
      await modernShown(page, name);
      await expect(field(page, name)).toBeFocused();
      expect(await saved(page)).toBe("modern");

      await pickStyle(page, "Terminal");
      await classicShown(page, name);
      expect(await saved(page)).toBe("classic");
      // The keyboard goes to the terminal Classic draws.
      await expect(terminalInput(page)).toBeFocused();
    }
    await page.keyboard.type("x");
    await expect.poll(() => typed(relay)).toBe("x");
  });

  // The picture's menu opens from the keyboard on the style shown, checked,
  // Escape puts it away without a choice or closing the panel, the style
  // already shown changes nothing, and it is the same menu in Terminal as in
  // Modern chat (the Overlord, 2026-11-03: "make sure in terminal the avatar
  // modal to switch matches modern chat"; #1987).
  test(`the ${who}'s picture opens its chat style menu from the keyboard on the style shown`, async ({ page }) => {
    // Arrange
    const relay = await open(page);
    const name = who === "CFO" ? "CFO" : NAME;
    if (who === "CFO") await page.keyboard.press("Control+Alt+1");
    else await card(page, NAME).click();
    await classicShown(page, name);
    const menu = header(page).getByRole("menu", { name: "Chat style", exact: true });
    const item = (label: string) => menu.getByRole("menuitemradio", { name: label, exact: true });
    // How the open menu looks: its place, its surface and each item's words,
    // icon and look.
    const look = () => menu.evaluate((element) => {
      const style = getComputedStyle(element), box = element.getBoundingClientRect();
      return {
        place: [Math.round(box.left), Math.round(box.top), Math.round(box.width)], surface: [style.backgroundColor, style.borderTopColor, style.borderRadius, style.boxShadow],
        items: [...element.querySelectorAll("[role=menuitemradio]")].map((each) => [each.textContent, each.querySelector("path")?.getAttribute("d"), each.className, Math.round(each.getBoundingClientRect().height)]),
      };
    });

    // Act
    await styleButton(page).focus();
    await page.keyboard.press("Enter");

    // Assert
    await expect(styleButton(page)).toHaveAttribute("aria-expanded", "true");
    await expect(item("Terminal")).toHaveAttribute("aria-checked", "true");
    await expect(item("Modern chat")).toHaveAttribute("aria-checked", "false");
    await expect(item("Terminal")).toBeFocused();
    const terminalLook = await look();

    // Act and Assert: Escape puts it away and leaves the panel open.
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    await expect(styleButton(page)).toBeFocused();
    await expect(page.locator(".context-pane")).toBeVisible();

    // Act: Modern chat, chosen with the arrow keys.
    await page.keyboard.press("Enter");
    await page.keyboard.press("ArrowDown");
    await expect(item("Modern chat")).toBeFocused();
    await page.keyboard.press("Enter");

    // Assert
    await modernShown(page, name);
    await expect(field(page, name)).toBeFocused();
    expect(await saved(page)).toBe("modern");

    // Act and Assert: the style shown, chosen again, changes nothing.
    await styleButton(page).click();
    await expect(item("Modern chat")).toHaveAttribute("aria-checked", "true");
    expect(await look(), "the same menu in Terminal and Modern chat").toEqual(terminalLook);
    await item("Modern chat").click();
    await expect(menu).toHaveCount(0);
    await modernShown(page, name);
    expect(await saved(page)).toBe("modern");
    expect(typed(relay)).toBe("");
  });
}

for (const style of ["modern", "classic"] as const) {
  test(`a saved choice of ${style} is kept through a reload`, async ({ page }) => {
    // Arrange
    await page.addInitScript((chosen) => { if (!localStorage.getItem("cfo-chat-style")) localStorage.setItem("cfo-chat-style", chosen); }, style);
    await open(page);

    // Act
    await card(page, NAME).click();

    // Assert
    if (style === "modern") await modernShown(page, NAME);
    else await classicShown(page, NAME);
    await page.reload();
    await card(page, NAME).click();
    if (style === "modern") await modernShown(page, NAME);
    else await classicShown(page, NAME);
    expect(await saved(page)).toBe(style);
  });
}

test("a held terminal in Classic says typing is paused, with no box beside it", async ({ page }) => {
  // Arrange
  const relay = await open(page);
  await card(page, NAME).click();
  await classicShown(page, NAME);

  // Act
  relay.say({ type: "held", reason: "pipeline owns this task" });

  // Assert
  await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();
  await expect(box(page, NAME)).toBeHidden();
  await expect(page.locator(".classic-queue")).toBeHidden();
});

// Every session opens in the chosen style whichever one the panel showed
// before it: the CFO, then two goblins, then back, on a phone and a desktop.
// A change of style keeps the session and its own draft (#1987).
for (const style of ["classic", "modern"] as const) {
  for (const width of [1440, 390]) {
    test(`in ${style} at ${width}px the CFO and two goblins each open in ${style}, and each keeps its own draft`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((chosen) => { if (!localStorage.getItem("cfo-chat-style")) localStorage.setItem("cfo-chat-style", chosen); }, style);
      const relay = await open(page);
      const shown = (name: string) => style === "classic" ? classicShown(page, name) : modernShown(page, name);
      const visits = [["1", "CFO"], ["2", NAME], ["3", IVY], ["1", "CFO"], ["2", NAME], ["3", IVY]] as const;

      // Act and Assert: each lands in the chosen style, and in Modern each
      // finds the draft it was left with.
      for (const [index, [key, name]] of visits.entries()) {
        await page.keyboard.press("Control+Alt+" + key);
        await expect(page.locator("#panel-title")).toHaveText(name);
        await shown(name);
        if (style === "classic") continue;
        if (index < 3) await field(page, name).fill(name + " draft");
        else await expect(field(page, name)).toHaveValue(name + " draft");
      }

      // Act: a draft written to Zeke stays his through Classic, Ivy and the CFO.
      await page.keyboard.press("Control+Alt+2");
      if (style === "classic") await pickStyle(page, "Modern chat");
      await field(page, NAME).fill("Zeke's own draft");
      await pickStyle(page, "Terminal");
      await classicShown(page, NAME);
      for (const [key, name] of [["3", IVY], ["1", "CFO"], ["2", NAME]] as const) {
        await page.keyboard.press("Control+Alt+" + key);
        await expect(page.locator("#panel-title")).toHaveText(name);
        await classicShown(page, name);
      }
      await pickStyle(page, "Modern chat");

      // Assert
      await modernShown(page, NAME);
      await expect(field(page, NAME)).toHaveValue("Zeke's own draft");
      expect(typed(relay)).toBe("");
    });
  }
}

// Reading back out of Classic belongs to the session it was taken on while
// the panel shows it: showing another session, or closing the panel, ends
// it, so the goblin opens in Classic again and the saved Classic stays
// (#1987). Only Modern chat in the style menu saves Modern (#1584).
for (const width of [1440, 390]) {
  test(`reading back out of Classic ends once the panel leaves the goblin at ${width}px, so it opens in Classic again`, async ({ page }) => {
    // Arrange: Zeke's full-screen Codex is held, and a wheel up over it
    // reads back his conversation.
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
    const relay = await open(page, snapshot({}, { harness: "codex" }), "", PAGE, "\x1b[?1049h> ready\r\n");
    await card(page, NAME).click();
    const terminal = page.locator(".terminal-view:not(.staged) > .xterm .xterm-screen");
    const readBack = async () => {
      await classicShown(page, NAME);
      relay.say({ type: "held", reason: "pipeline owns this task" });
      await expect(page.locator(".context-pane").getByText("Typing is paused.", { exact: true })).toBeVisible();
      await terminal.hover();
      await page.mouse.wheel(0, -45);
      await modernShown(page, NAME);
    };
    await readBack();

    // Act and Assert: the CFO and Ivy, then Zeke again.
    for (const [key, name] of [["1", "CFO"], ["3", IVY], ["2", NAME]] as const) {
      await page.keyboard.press("Control+Alt+" + key);
      await expect(page.locator("#panel-title")).toHaveText(name);
      await classicShown(page, name);
    }

    // Act and Assert: the panel closed and Zeke opened again.
    await readBack();
    if (width === 390) await page.getByRole("button", { name: "Back to Board", exact: true }).click();
    else {
      await page.getByRole("button", { name: "Back to the CFO", exact: true }).click();
      await page.getByRole("button", { name: "Close panel", exact: true }).click();
    }
    await expect(page.locator(".context-pane")).toBeHidden();
    await card(page, NAME).click();
    await classicShown(page, NAME);
    expect(await saved(page)).toBe("classic");
    expect(typed(relay)).toBe("");
  });
}

// #2050, 2026-11-04: a goblin's pane differed from the CFO's in look and
// controls, and one goblin chat opened blank. In each style, on a phone and on
// the desktop, a goblin's pane draws what the CFO's does, its conversation or
// its terminal, never a blank stage, with the same controls in its header and
// its box but for those only one has, each named here with why. Both are idle,
// since a turn's Stop is what any session offers while it works.
const CFO_ONLY: Record<string, string> = {
  "Close panel": "The CFO's pane is the panel's home, so its corner closes the panel.",
};
const GOBLIN_ONLY: Record<string, string> = {
  "Back to the CFO": "A goblin's corner returns the panel to its home, in the place Close panel holds there.",
};
// A phone's pane fills the screen, so both corners go back to the board alike.
const DESKTOP_CORNERS = ["Close panel", "Back to the CFO"];
// paneControls names every control shown in the panel's header and its box,
// by its accessible name, the session's own name taken out.
const paneControls = (page: Page, name: string) => page.locator(".context-pane").evaluate((pane, name) => [...pane.querySelectorAll<HTMLElement>(".panel-header button, .composer button, .panel-header [role=tab]")]
  .filter((control) => control.checkVisibility())
  .map((control) => (control.getAttribute("aria-label") || control.textContent || "").trim().replaceAll(name, "<name>"))
  .sort(), name);
for (const style of ["Modern chat", "Terminal"] as const) {
  for (const width of [390, 1440]) {
    test.describe(`at ${width}px`, () => {
      const isPhone = width === 390;
      test.use({ viewport: { width, height: 900 }, hasTouch: isPhone, isMobile: isPhone });
      test(`in ${style} a goblin's pane draws what the CFO's does, never blank, with the same controls`, async ({ page }, testInfo) => {
        // Arrange
        await page.addInitScript((saved) => localStorage.setItem("cfo-chat-style", saved), style === "Terminal" ? "classic" : "modern");
        await open(page, snapshot({}, { phase: "idle" }));
        const shown = async (who: "CFO" | "goblin") => {
          const name = who === "CFO" ? "CFO" : NAME;
          if (style === "Modern chat") await expect(page.getByRole("log", { name: "Messages of " + name })).toContainText("On it.");
          else await expect(page.locator(".terminal-view:not(.staged) > .xterm")).toBeVisible();
          await page.evaluate(() => document.fonts.ready);
          await expect.poll(() => page.evaluate(() => [...document.images].every((image) => image.complete))).toBe(true);
          await page.screenshot({ path: testInfo.outputPath(`${who}-${style === "Terminal" ? "terminal" : "modern"}-${width}.png`) });
          return paneControls(page, name);
        };

        // Act
        await page.keyboard.press("Control+Alt+1");
        await expect(page.locator("#panel-title")).toHaveText("CFO");
        const cfo = await shown("CFO");
        await (isPhone ? page.getByRole("button", { name: "Back to Board", exact: true }).tap() : Promise.resolve());
        await (isPhone ? card(page, NAME).tap() : card(page, NAME).click());
        await expect(page.locator("#panel-title")).toHaveText(NAME);
        const goblin = await shown("goblin");

        // Assert
        const named = (only: Record<string, string>) => Object.keys(only).filter((control) => !isPhone || !DESKTOP_CORNERS.includes(control)).sort();
        expect(cfo.filter((control) => !goblin.includes(control)), "only the CFO's").toEqual(named(CFO_ONLY));
        expect(goblin.filter((control) => !cfo.includes(control)), "only the goblin's").toEqual(named(GOBLIN_ONLY));
      });
    });
  }
}
