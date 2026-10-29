import { expect, test, type Locator, type Page } from "./site";
import { box, boxMenu, field, open, openGoblin, PAGE, send, snapshot } from "./session-board";

// What he types and what pops up over the box always fit the window (the
// Overlord, 2026-10-27): "as I'm typing in very, very long messages ... I
// don't want to get cut off and not be able to see what I'm typing below",
// and "the dictation Copy or history pane doesn't go over the top of the
// container, it gets clipped at the very top". The supervisor is played by
// tests/session-board.ts; every conversation is made up.

const LONG = Array.from({ length: 34 }, (_, at) => `Line ${at + 1}: a long dictated message keeps going so the box has to grow past its cap.`).join("\n");
const DICTATIONS = [
  { text: "check the phone spec once the swipe fix lands and then tell me what the iPhone does", at: Date.parse("2026-10-27T09:30:00Z") },
  { text: "pause the gate run until memory is back", at: Date.parse("2026-10-27T09:12:00Z") },
  { text: "open the review page", at: Date.parse("2026-10-27T08:50:00Z") },
  { text: "ship the voice bubble", at: Date.parse("2026-10-27T08:40:00Z") },
];

// inWindow says whether locator's box lies whole inside the window.
const inWindow = async (page: Page, locator: Locator) => {
  const bounds = (await locator.boundingBox())!;
  const size = page.viewportSize()!;
  return bounds.y >= 0 && bounds.x >= 0 && bounds.y + bounds.height <= size.height + 0.5 && bounds.x + bounds.width <= size.width + 0.5;
};

for (const [label, width, height] of [["a short desktop panel", 1280, 520], ["a phone", 390, 844], ["a small phone", 360, 640]] as const) {
  test(`a message longer than the box's cap keeps its caret and last lines in view, and Send on screen, in ${label}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page);
    await openGoblin(page);

    // Act: he types, the long way, the last lines of a message pasted before.
    await field(page).fill(LONG);
    await field(page).press("End");
    await field(page).pressSequentially(" and the last words");

    // Assert: the box stops at its cap and scrolls inside itself, at its end,
    // and the box, Send and the panel's top all stay inside the window.
    const measured = await field(page).evaluate((element: HTMLTextAreaElement) => ({ shown: element.clientHeight, scrolled: element.scrollTop + element.clientHeight, whole: element.scrollHeight, caret: element.selectionEnd, length: element.value.length }));
    expect(measured.whole, "the draft is taller than the box").toBeGreaterThan(measured.shown + 100);
    expect(measured.whole - measured.scrolled, "the box shows its last lines").toBeLessThanOrEqual(2);
    expect(measured.caret).toBe(measured.length);
    expect(await inWindow(page, box(page)), "the whole box is in the window").toBe(true);
    expect(await inWindow(page, send(page)), "Send is in the window").toBe(true);
    expect(await inWindow(page, page.locator("#panel-title")), "the panel's header kept its place").toBe(true);
  });
}

for (const [label, width, height] of [["the smallest desktop panel", 1280, 420], ["a phone", 390, 844]] as const) {
  test(`speech history opens whole over its box, never clipped, in ${label}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: DICTATIONS } }));
    await openGoblin(page);

    // Act
    await (await boxMenu(page)).getByRole("button", { name: "Speech history" }).click();

    // Assert: the card is whole in the window, its title the topmost thing
    // where it is drawn, and a list longer than the room scrolls inside it.
    const history = page.getByRole("dialog", { name: "Speech history" });
    await expect(history.getByText("check the phone spec", { exact: false })).toBeVisible();
    expect(await inWindow(page, history)).toBe(true);
    const title = history.getByText("Speech history", { exact: true });
    const point = (await title.boundingBox())!;
    expect(await page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.textContent, { x: point.x + 4, y: point.y + point.height / 2 })).toBe("Speech history");
    const list = history.locator("ul");
    expect(await list.evaluate((element) => element.scrollHeight <= element.clientHeight || getComputedStyle(element).overflowY === "auto")).toBe(true);

    // Escape closes it and gives the keyboard back.
    await page.keyboard.press("Escape");
    await expect(history).toHaveCount(0);
  });
}

test("the Style menu opens without its button's tip over it", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);
  const style = box(page).getByRole("button", { name: /^Style: / });
  await style.hover();
  await expect(page.getByRole("tooltip")).toHaveText("Chat style and speech history");

  // Act
  await style.click();

  // Assert
  await expect(box(page).getByRole("group", { name: "Chat style" })).toBeVisible();
  await expect(page.getByRole("tooltip")).toHaveCount(0);
});

// The Overlord, 2026-10-28: "what happened to being able to click the
// microphone and see recent messages".
test("a click on the box's microphone opens the speech history and dictates nothing", async ({ page }) => {
  // Arrange
  await open(page);
  await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: DICTATIONS } }));
  await openGoblin(page);
  const microphone = box(page).getByRole("button", { name: "Hold to dictate", exact: true });

  // Act
  await microphone.click();

  // Assert
  const history = page.getByRole("dialog", { name: "Speech history" });
  await expect(history.getByText("check the phone spec", { exact: false })).toBeVisible();
  await expect(microphone).not.toHaveClass(/recording/);
  await expect(field(page)).toHaveValue("");
});

test("a long message keeps its last lines in view while dictation adds to it", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 1280, height: 600 });
  await open(page);
  await openGoblin(page);
  await field(page).fill(LONG);
  await field(page).press("End");

  // Act: words land at the end without a keystroke, as a dictation's do.
  await field(page).evaluate((element: HTMLTextAreaElement) => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
    setter.call(element, element.value + "\nLine 35: dictated words at the end.");
    element.setSelectionRange(element.value.length, element.value.length);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });

  // Assert
  await expect.poll(() => field(page).evaluate((element: HTMLTextAreaElement) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(2);
});

// The box's controls sit in one row inside its border, as the Claude desktop
// app draws its box (the CFO's review of step 3, 2026-10-27: "No loose rows
// under it. Model short (Opus 5.5), never cut at 360 px. Every icon gets a
// hover label. Test against the box border."), on his desktop's side panel
// and on a phone's touch screen. The usage pies end the row since the
// Overlord, 2026-10-28: "The usage circle goes in place of the arrow in the box".
const USAGE = { input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5" };
const WEEK = { provider: "claude", status: "available", percent_remaining: 12, read_at: "2026-10-28T09:00:00Z", resets_at: "2026-11-02T09:00:00Z", source: "oauth", floor_percent: 5 };
for (const [label, width, height, isTouch] of [["the desktop side panel", 1440, 960, false], ["a phone", 390, 844, true], ["a small phone", 360, 640, true]] as const) {
  test.describe(`on ${label}`, () => {
    test.use({ viewport: { width, height }, hasTouch: isTouch, isMobile: isTouch });

    test("the box's controls and usage pies lie in one row inside its border, the model whole", async ({ page }) => {
      // Arrange: a Claude Code session in auto mode, its effort and usage known.
      const relay = await open(page, snapshot({ subscriptions: [WEEK] }), "rebase onto main", { ...PAGE, usage: USAGE });
      await openGoblin(page);
      relay.say({ type: "screen", asking: false, open: false, working: false, submission: "", mode: "auto mode" });
      const model = box(page).getByRole("button", { name: "Change model" });
      await expect(model.locator(".engine-control-model")).toHaveText("Opus 5.5");
      await expect(model.locator(".effort-level")).toHaveText("xhigh");

      // Act
      const row = await box(page).locator(".composer-input").evaluate((input) => {
        const border = input.getBoundingClientRect();
        const text = input.querySelector("textarea")!.getBoundingClientRect();
        return [...input.querySelectorAll(".composer-row button, .composer-row .usage-bar")].map((control) => {
          const bounds = control.getBoundingClientRect();
          return {
            name: control.getAttribute("aria-label"), tip: control.getAttribute("data-tip"),
            isInside: bounds.width > 0 && bounds.left >= border.left - 0.5 && bounds.right <= border.right + 0.5 && bounds.top >= text.bottom - 0.5 && bounds.bottom <= border.bottom + 0.5,
            middle: Math.round(bounds.top + bounds.height / 2), isCut: control.scrollWidth > control.clientWidth + 0.5,
          };
        });
      });

      // Assert
      expect(row.map((control) => control.name)).toEqual(["Add files", "Style: modern", "Change model", isTouch ? "Dictate" : "Hold to dictate", "Usage"]);
      expect(row.filter((control) => !control.isInside).map((control) => control.name), "every control inside the box's border, under the words").toEqual([]);
      expect(new Set(row.map((control) => control.middle)).size, "one row").toBe(1);
      expect(row.filter((control) => control.isCut).map((control) => control.name), "no control's words cut").toEqual([]);
      expect(row.filter((control) => control.name !== "Usage" && !control.tip).map((control) => control.name), "every button has a hover label").toEqual([]);
      await expect(box(page).locator(".composer-tools")).toHaveCount(0);
    });

    // The Overlord, 2026-10-28: "place send button in line with message cfo
    // height in chat input". Send, and Stop in its place while the session
    // works, sits at the end of the line he writes on, no taller than that
    // line, and stays beside his last line as the box grows.
    test("Send sits at the end of the line he writes on, as tall as that line", async ({ page }) => {
      // Arrange
      const relay = await open(page);
      await openGoblin(page);
      const measure = () => box(page).locator(".composer-input").evaluate((input) => {
        const shape = (element: Element) => {
          const bounds = element.getBoundingClientRect();
          return { top: bounds.top, bottom: bounds.bottom, left: bounds.left, right: bounds.right, height: bounds.height, middle: bounds.left + bounds.width / 2 };
        };
        const words = shape(input.querySelector("textarea")!);
        const button = input.querySelector(".composer-send")!;
        const control = shape(button);
        const above = document.elementFromPoint(control.middle, control.top - 6);
        return { words, control, name: button.getAttribute("aria-label"), border: shape(input), isReachedAbove: !!above && button.contains(above) };
      });
      await field(page).fill("check the phone spec");
      const line = (await measure()).words.height;

      for (const [text, isWorking] of [["", false], ["check the phone spec", false], ["one\ntwo\nthree", false], ["", true]] as const) {
        // Act
        await field(page).fill(text);
        relay.say({ type: "screen", asking: false, open: false, working: isWorking, submission: "", mode: "auto mode" });
        await expect(box(page).locator(".composer-send")).toHaveAccessibleName(isWorking ? "Stop" : "Send");
        const { words, control, border, isReachedAbove } = await measure();

        // Assert
        expect(Math.abs(control.height - line), `${isWorking ? "Stop" : "Send"} is as tall as one line of words`).toBeLessThanOrEqual(1);
        expect(Math.abs(control.bottom - words.bottom), "it sits beside his last line").toBeLessThanOrEqual(1);
        expect(control.left, "it follows the words on their line").toBeGreaterThanOrEqual(words.right - 0.5);
        expect(control.right, "it stays inside the box's border").toBeLessThanOrEqual(border.right + 0.5);
        if (isTouch) expect(isReachedAbove, "a finger just above it still presses it").toBe(true);
      }
    });
  });
}

// The Overlord, 2026-10-27: "make sure that the cursor blinking, when you're
// selecting the chat box, is the same size to fit that font in text". The
// browser draws the caret as tall as the box's line, so the line must be the
// height of the letters themselves, empty and while he types.
test("the box's caret is the height of its letters, empty and while he types", async ({ page }) => {
  // Arrange
  await open(page);
  await openGoblin(page);

  for (const text of ["", "check the phone spec"]) {
    // Act
    await field(page).fill(text);
    await field(page).focus();
    const { line, letters } = await field(page).evaluate((box, text) => {
      const style = getComputedStyle(box);
      const block = document.createElement("div");
      const word = document.createElement("span");
      block.style.cssText = "position: absolute; visibility: hidden; white-space: pre";
      block.style.font = style.font;
      block.style.lineHeight = style.lineHeight;
      word.textContent = text || (box instanceof HTMLTextAreaElement ? box.placeholder : "");
      block.append(word);
      document.body.append(block);
      const sizes = { line: block.getBoundingClientRect().height, letters: word.getBoundingClientRect().height };
      block.remove();
      return sizes;
    }, text);

    // Assert
    expect(Math.abs(line - letters), `the line is ${line}px and the letters ${letters}px`).toBeLessThanOrEqual(1);
  }
});
