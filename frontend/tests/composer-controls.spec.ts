import { expect, test } from "./site";
import { box, field, GOBLIN, NAME, open, openGoblin, PAGE, pasted, pickStyle, pressRow, snapshot, typed } from "./session-board";

// A fake microphone, so the box can dictate.
test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });

// The box keeps no choice of style: Speech history beside its + opens at a
// press, Clear message follows it on a phone alone, and the microphone stays
// after the model at the row's end. What he writes, the caret and the
// placeholder start at the top of the box (the Overlord, 2026-11-03: "why
// input cursor and text not at top of chat input box??"). The session's
// picture in the panel's header switches the style both ways, and the
// header's row opens the session in a Windows Terminal window of its own, or
// its More does (#1987).
for (const who of ["CFO", "goblin"] as const) {
  for (const width of [1440, 390]) {
    test(`Codex ${who} box adds a file and opens Speech history, and its header opens a terminal window and changes style at ${width}px`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      const relay = await open(page, snapshot({ cfo_harness: "codex" }, { harness: "codex" }));
      const name = who === "CFO" ? "CFO" : NAME;
      const kept = "C:/session-files/example.txt";
      const uploads: { task: string; name: string; text: string }[] = [];
      const windows: unknown[] = [];
      await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [] } }));
      await page.route("**/api/terminal/open", (route) => { windows.push(route.request().postDataJSON()); return route.fulfill({ json: {} }); });
      await page.route("**/api/composer/file**", async (route) => {
        const query = new URL(route.request().url()).searchParams;
        uploads.push({ task: query.get("task") ?? "", name: query.get("name") ?? "", text: route.request().postDataBuffer()?.toString() ?? "" });
        await route.fulfill({ status: 201, json: { path: kept } });
      });
      if (who === "CFO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      const composer = box(page, name);

      // Assert: the field's first line starts at the top of the box, empty
      // with its placeholder and with words in it.
      const sink = () => field(page, name).evaluate((textarea) => {
        const input = textarea.closest(".composer-input")!;
        const style = getComputedStyle(input);
        return textarea.getBoundingClientRect().top - (input.getBoundingClientRect().top + parseFloat(style.borderTopWidth) + parseFloat(style.paddingTop));
      });
      await expect(field(page, name)).toHaveAttribute("placeholder", "Message " + name);
      expect(Math.abs(await sink()), "the empty field at the top of the box").toBeLessThanOrEqual(1);
      await field(page, name).fill("A line");
      expect(Math.abs(await sink()), "the written field at the top of the box").toBeLessThanOrEqual(1);
      await field(page, name).fill("");

      // Act: the visible plus opens the chooser and keeps the chosen file.
      const choosing = page.waitForEvent("filechooser");
      await composer.getByRole("button", { name: "Add files", exact: true }).click();
      await (await choosing).setFiles({ name: "example.txt", mimeType: "text/plain", buffer: Buffer.from("read this attachment") });

      // Assert
      const attached = composer.getByRole("list", { name: "Attached files" });
      await expect(attached).toContainText("example.txt");
      await expect(field(page, name)).toHaveValue("");
      expect(uploads).toEqual([{ task: who === "CFO" ? "" : GOBLIN.id, name: "example.txt", text: "read this attachment" }]);
      expect(typed(relay)).toBe("");

      // Assert: the row leads with Add files and Speech history, then Clear
      // message on a phone alone, and the microphone follows the model.
      const isPhone = width === 390;
      const row = await composer.locator(".composer-row button").evaluateAll((buttons) => buttons.filter((each) => each.checkVisibility()).map((each) => each.getAttribute("aria-label")));
      expect(row.slice(0, 3)).toEqual(isPhone ? ["Add files", "Speech history", "Clear message"] : ["Add files", "Speech history", "Change model"]);
      expect(row.indexOf("Hold to dictate"), "the microphone follows the model").toBe(row.indexOf("Change model") + 1);
      const clear = composer.getByRole("button", { name: "Clear message", exact: true });
      if (isPhone) await expect(clear).toBeVisible();
      else await expect(clear).toBeHidden();

      // Act and Assert: the box has no style to choose, and Speech history
      // opens at a press and gives the keyboard back to it on Escape.
      await expect(composer.getByRole("button", { name: /^Style: / })).toHaveCount(0);
      await expect(page.getByRole("group", { name: "Chat style", exact: true })).toHaveCount(0);
      const speech = composer.getByRole("button", { name: "Speech history", exact: true });
      await speech.click();
      await expect(page.getByRole("dialog", { name: "Speech history" })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog", { name: "Speech history" })).toHaveCount(0);
      await expect(speech).toBeFocused();

      // Act: opening a separate terminal from the header preserves the draft
      // and view.
      await pressRow(page, "Open in Windows Terminal", "Open in terminal");
      await expect.poll(() => windows).toEqual([who === "CFO" ? { native: "cfo=cfo-term" } : { native: `task=${GOBLIN.id}&generation=${GOBLIN.generation}` }]);
      await expect(attached).toContainText("example.txt");

      // Act: Terminal in the picture's menu opens the terminal alone, and
      // Modern chat there chooses Modern again.
      await pickStyle(page, "Terminal");
      await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
      await expect(composer).toBeHidden();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
      await pickStyle(page, "Modern chat");
      await expect(field(page, name)).toBeVisible();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style")), "Modern chat chose Modern").toBe("modern");

      // Assert: changing views types nothing; Send submits the file once.
      await expect(attached).toContainText("example.txt");
      await expect(field(page, name)).toBeInViewport();
      expect(typed(relay)).toBe("");
      await composer.getByRole("button", { name: "Send", exact: true }).click();
      await expect.poll(() => typed(relay)).toBe(pasted(kept) + "\r");
      await expect(field(page, name)).toHaveValue("");
      await expect(attached).toHaveCount(0);

      // Act and Assert: on a phone, Clear message beside Speech history
      // empties the box and keeps the keyboard in it.
      if (!isPhone) return;
      await field(page, name).fill("Words to drop");
      await clear.click();
      await expect(field(page, name)).toHaveValue("");
      await expect(field(page, name)).toBeFocused();
      expect(typed(relay)).toBe(pasted(kept) + "\r");
    });
  }
}

// The session's usage, a context and a week, so the row ends in two pies.
const USAGE = { context_tokens: 593_000, context_window: 1_000_000, model: "claude-opus-5-5" };
const WEEK = { provider: "claude", status: "available", percent_remaining: 12, read_at: "2026-10-28T09:00:00Z", resets_at: "2026-11-02T09:00:00Z", source: "oauth", floor_percent: 5 };

// The Overlord, 2026-11-03, of the CFO's box: "in the chat input for the
// usage. Whenever I start the dictation, it gets clipped." While he
// dictates, the microphone is its red dot and level bars, wider than the
// microphone, and the row still lies inside the box's border, the CFO's and
// a goblin's, on a phone, a narrow desktop and a wide one: the usage pies,
// the recording control and every button whole, where the model's name, and
// on the smallest phone its effort after it, may give way with an ellipsis
// (#1987).
test.describe("while he dictates", () => {
  for (const [label, width, height, isTouch] of [["a wide desktop", 1920, 1080, false], ["the desktop side panel", 1440, 960, false], ["a narrow desktop", 1024, 768, false], ["a phone", 390, 844, true], ["a small phone", 360, 640, true]] as const) {
    test.describe(`on ${label}`, () => {
      test.use({ viewport: { width, height }, hasTouch: isTouch, isMobile: isTouch });

      for (const who of ["CFO", "goblin"] as const) {
        test(`the ${who}'s row lies inside the box with its usage pies, its recording control and every button whole`, async ({ page }) => {
          // Arrange: a session whose usage is known.
          const name = who === "CFO" ? "CFO" : NAME;
          await open(page, snapshot({ subscriptions: [WEEK] }), "rebase onto main", { ...PAGE, usage: USAGE });
          if (who === "CFO") await page.keyboard.press("Control+Alt+1");
          else await openGoblin(page);
          const composer = box(page, name);
          await expect(composer.locator(".usage-pie").first()).toBeVisible();
          const row = () => composer.locator(".composer-input").evaluate((input) => {
            const border = input.getBoundingClientRect();
            const controls = [...input.querySelectorAll(".composer-row button, .composer-row .usage-bar")].filter((control) => control.getClientRects().length);
            const isWhole = (element: Element) => {
              const bounds = element.getBoundingClientRect();
              return bounds.width > 0 && bounds.left >= border.left - 0.5 && bounds.right <= border.right + 0.5;
            };
            return {
              names: controls.map((control) => control.getAttribute("aria-label")),
              outside: [...controls, ...input.querySelectorAll(".composer-row .usage-pie, .composer-row .voice-bars, .composer-row .voice-dot")].filter((part) => !isWhole(part)).map((part) => part.getAttribute("aria-label") || part.getAttribute("class")),
              cut: controls.filter((control) => control.scrollWidth > control.clientWidth + 0.5).map((control) => control.getAttribute("aria-label")),
              rows: new Set(controls.map((control) => { const bounds = control.getBoundingClientRect(); return Math.round(bounds.top + bounds.height / 2); })).size,
              isRowScrolled: input.querySelector(".composer-row")!.scrollWidth > input.querySelector(".composer-row")!.clientWidth,
              modelEnd: getComputedStyle(input.querySelector(".engine-control-model")!).textOverflow,
            };
          });

          // Act: he starts dictating.
          if (isTouch) await composer.getByRole("button", { name: "Dictate", exact: true }).tap();
          else {
            await composer.getByRole("button", { name: "Hold to dictate", exact: true }).hover();
            await page.mouse.down();
          }
          const recording = composer.getByRole("button", { name: isTouch ? "Stop dictating" : "Listening", exact: true });
          await expect(recording.locator(".voice-bars")).toBeVisible();

          // Assert: the row, once the recording control has its width, lies
          // whole inside the box on one row, and only the model's words may
          // end in an ellipsis.
          await expect.poll(async () => (await row()).outside, "every control, pie and bar inside the box's border").toEqual([]);
          const shown = await row();
          expect(shown.names).toEqual(["Add files", "Speech history", ...isTouch ? ["Clear message"] : [], "Change model", isTouch ? "Stop dictating" : "Listening", "Usage"]);
          expect(shown.cut, "no control's words cut").toEqual([]);
          expect(shown.rows, "one row").toBe(1);
          expect(shown.isRowScrolled, "the row never scrolls sideways").toBe(false);
          expect(shown.modelEnd).toBe("ellipsis");

          // Act: he stops.
          if (isTouch) await recording.tap();
          else await page.mouse.up();

          // Assert: the microphone is back, and the row with it.
          await expect(composer.getByRole("button", { name: isTouch ? "Dictate" : "Hold to dictate", exact: true })).toBeVisible();
          await expect.poll(async () => (await row()).outside).toEqual([]);
        });
      }
    });
  }
});
