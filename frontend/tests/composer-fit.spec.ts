import { expect, test, type Locator, type Page } from "./site";
import { box, field, open, openGoblin, PAGE, send, snapshot } from "./session-board";

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
    await box(page).getByRole("button", { name: "Speech history", exact: true }).click();

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

  // The Overlord, 2026-11-02: "I hate the container padding, the X is
  // squished and each record has no margin in the internal container."
  test(`speech history keeps one inset for its title, records and foot, its close with room, and a gap between records, in ${label}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: DICTATIONS } }));
    await openGoblin(page);

    // Act
    await box(page).getByRole("button", { name: "Speech history", exact: true }).click();

    // Assert
    const history = page.getByRole("dialog", { name: "Speech history" });
    await expect(history.locator("li")).toHaveCount(DICTATIONS.length);
    const shape = await history.evaluate((card) => {
      const rect = (selector: string) => card.querySelector(selector)!.getBoundingClientRect();
      const text = (selector: string) => {
        const range = document.createRange();
        range.selectNodeContents(card.querySelector(selector)!);
        return range.getBoundingClientRect();
      };
      const edge = card.getBoundingClientRect(), close = rect(".voice-close"), title = rect(".voice-card-title"), list = rect("ul");
      const records = [...card.querySelectorAll("li")].map((record) => record.getBoundingClientRect());
      return {
        insets: [text(".voice-card-title").left, text("li .voice-meta").left, text(".voice-footer").left].map((left) => Math.round(left - edge.left)),
        closeRoom: Math.min(close.top - edge.top, edge.right - close.right),
        closeCentred: Math.abs((close.top + close.bottom) / 2 - (title.top + title.bottom) / 2),
        closeOverList: close.bottom - list.top,
        gaps: records.slice(1).map((record, at) => record.top - records[at].bottom),
        recordInset: text("li .voice-meta").left - records[0].left,
      };
    });
    expect(new Set(shape.insets).size, "title, records and foot start at one inset: " + shape.insets.join(", ")).toBe(1);
    expect(shape.closeRoom, "the close keeps off the card's edges").toBeGreaterThanOrEqual(8);
    expect(shape.closeCentred, "the close sits on the title's row").toBeLessThanOrEqual(2);
    expect(shape.closeOverList, "the close stays out of the list").toBeLessThanOrEqual(0);
    for (const gap of shape.gaps) expect(gap, "a gap between records").toBeGreaterThanOrEqual(4);
    expect(shape.recordInset, "a record's words keep off its edge").toBeGreaterThanOrEqual(10);
  });

  // The Overlord, 2026-11-02: "When you click off the modal for speech
  // dictation it should close."
  test(`a press off speech history closes it, and one on a record does not, in ${label}`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height });
    await open(page);
    await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: DICTATIONS } }));
    await openGoblin(page);
    await box(page).getByRole("button", { name: "Speech history", exact: true }).click();
    const history = page.getByRole("dialog", { name: "Speech history" });

    // Act and Assert: a press on a record's words keeps it.
    await history.getByText("open the review page", { exact: true }).click();
    await expect(history).toBeVisible();

    // Act and Assert: a press in the box beneath it closes it.
    await field(page).click();
    await expect(history).toHaveCount(0);
    await expect(field(page)).toBeFocused();

    // Act and Assert: opened again, Escape closes it with the keyboard on
    // the page rather than in the box.
    await box(page).getByRole("button", { name: "Speech history", exact: true }).click();
    await expect(history).toBeVisible();
    await page.evaluate(() => { if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); });
    await page.keyboard.press("Escape");
    await expect(history).toHaveCount(0);
  });
}


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
// app draws its box (the CEO's review of step 3, 2026-10-27: "No loose rows
// under it. Model short (Opus 5.5), never cut at 360 px. Every icon gets a
// hover label. Test against the box border."), on his desktop's side panel
// and on a phone's touch screen. The hover labels went with item 42, which
// keeps tips out of the box, and the usage pies end the row since the
// Overlord, 2026-10-28: "The usage circle goes in place of the arrow in the box".
// Since 2026-11-03 a phone's row also holds Speech history and Clear message
// beside its + ("trash button only needed on mobile and you can put it next
// to plus icon"; #1987), so on a 360 px phone the model's name gives way with
// an ellipsis, its mark and effort whole; from a 390 px phone up it is whole.
const USAGE = { input_tokens: 1_400_000, output_tokens: 289_000, cache_read_tokens: 208_000_000, cost_usd: 3.209, context_tokens: 593_000, context_window: 1_000_000 };
const WEEK = { provider: "claude", status: "available", percent_remaining: 12, read_at: "2026-10-28T09:00:00Z", resets_at: "2026-11-02T09:00:00Z", source: "oauth", floor_percent: 5 };
for (const [label, width, height, isTouch] of [["the desktop side panel", 1440, 960, false], ["a phone", 390, 844, true], ["a small phone", 360, 640, true]] as const) {
  test.describe(`on ${label}`, () => {
    test.use({ viewport: { width, height }, hasTouch: isTouch, isMobile: isTouch });

    test(`the box's controls and usage pies lie in one row inside its border, the model's name ${width === 360 ? "giving way" : "whole"}`, async ({ page }) => {
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
        return [...input.querySelectorAll(".composer-row button, .composer-row .usage-bar")].filter((control) => control.getClientRects().length).map((control) => {
          const bounds = control.getBoundingClientRect();
          return {
            name: control.getAttribute("aria-label"),
            isInside: bounds.width > 0 && bounds.left >= border.left - 0.5 && bounds.right <= border.right + 0.5 && bounds.top >= text.bottom - 0.5 && bounds.bottom <= border.bottom + 0.5,
            middle: Math.round(bounds.top + bounds.height / 2), isCut: control.scrollWidth > control.clientWidth + 0.5,
          };
        });
      });

      // Assert
      expect(row.map((control) => control.name)).toEqual(["Add files", "Speech history", ...isTouch ? ["Clear message"] : [], "Change model", isTouch ? "Dictate" : "Hold to dictate", "Usage"]);
      expect(row.filter((control) => !control.isInside).map((control) => control.name), "every control inside the box's border, under the words").toEqual([]);
      expect(new Set(row.map((control) => control.middle)).size, "one row").toBe(1);
      expect(row.filter((control) => control.isCut).map((control) => control.name), "no control's words cut").toEqual([]);
      await expect(box(page).locator(".composer-tools")).toHaveCount(0);
      const words = await model.evaluate((button) => [".mark", ".engine-control-model", ".effort-level"].map((part) => {
        const element = button.querySelector(part)!;
        return { isCut: element.scrollWidth > element.clientWidth + 0.5, end: getComputedStyle(element).textOverflow };
      }));
      expect(words.map((part) => part.isCut), "the mark, the name and the effort, each whole or not").toEqual([false, width === 360, false]);
      expect(words[1].end, "a name that gives way ends in an ellipsis").toBe("ellipsis");
      // A phone's row has room for the two pies alone, the side panel's for
      // their percentages too.
      await expect(box(page).locator(".usage-percent:visible")).toHaveCount(isTouch ? 0 : 2);
    });

    // The Overlord, 2026-10-28: "place send button in line with message cfo
    // height in chat input". Send, and Stop in its place while the session
    // works, sits at the end of the line he writes on, no taller than that
    // line, and stays beside his last line as the box grows.
    test("Send sits at the end of the line he writes on, as tall as that line", async ({ page }) => {
      // Arrange: a goblin waiting on CI sits at its prompt, so its screen
      // alone says whether a turn runs (a working task's empty box shows Stop).
      const relay = await open(page, snapshot({}, { phase: "waiting", waiting_on: "ci" }));
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
