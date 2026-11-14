import { expect, test, type Locator, type Page } from "./site";
import { recordingBegins } from "./dictation-hold";
import { box, field, GOBLIN, NAME, open, openGoblin, PAGE, pasted, pickStyle, pressRow, snapshot, typed, type Relay } from "./session-board";

// A fake microphone, so the box can dictate.
test.use({ permissions: ["microphone"], launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required"] } });

// The box keeps no choice of style: Speech history beside its + opens at a
// press, Clear message follows it on a phone alone, and the microphone stays
// after the model at the row's end. What he writes, the caret and the
// placeholder start at the top of the box (the Overlord, 2026-11-03: "why
// input cursor and text not at top of chat input box??"). The session's
// picture in the panel's header switches the style both ways, and the
// header's row opens the session in a Windows Terminal window of its own, or
// its More does (#1987), except on a phone, which has none to open (#2222).
for (const who of ["CEO", "goblin"] as const) {
  for (const width of [1440, 390]) {
    test(`Codex ${who} box adds a file and opens Speech history, and its header ${width === 390 ? "offers no terminal window" : "opens a terminal window"} and changes style at ${width}px`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      const relay = await open(page, snapshot({ ceo_harness: "codex" }, { harness: "codex" }));
      const name = who === "CEO" ? "CEO" : NAME;
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
      if (who === "CEO") await page.keyboard.press("Control+Alt+1");
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
      expect(uploads).toEqual([{ task: who === "CEO" ? "" : GOBLIN.id, name: "example.txt", text: "read this attachment" }]);
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
      // and view; a phone offers none.
      if (isPhone) await expect(page.locator(".context-pane .panel-top").getByRole("button", { name: "Open in Windows Terminal", exact: true })).toHaveCount(0);
      else {
        await pressRow(page, "Open in Windows Terminal", "Open in terminal");
        await expect.poll(() => windows).toEqual([who === "CEO" ? { native: "ceo=ceo-term" } : { native: `task=${GOBLIN.id}&generation=${GOBLIN.generation}` }]);
      }
      await expect(attached).toContainText("example.txt");

      // Act: Terminal in the picture's menu opens the terminal alone, and
      // Modern chat there chooses Modern again.
      await pickStyle(page, "Terminal");
      await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
      await expect(composer).toBeHidden();
      expect(await page.evaluate(() => localStorage.getItem("ceo-chat-style"))).toBe("classic");
      await pickStyle(page, "Modern chat");
      await expect(field(page, name)).toBeVisible();
      expect(await page.evaluate(() => localStorage.getItem("ceo-chat-style")), "Modern chat chose Modern").toBe("modern");

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
const USAGE = { context_tokens: 593_000, context_window: 1_000_000 };
const WEEK = { provider: "claude", status: "available", percent_remaining: 12, read_at: "2026-10-28T09:00:00Z", resets_at: "2026-11-02T09:00:00Z", source: "oauth", floor_percent: 5 };

// The Overlord, 2026-11-03, of the CEO's box: "in the chat input for the
// usage. Whenever I start the dictation, it gets clipped." While he
// dictates, the microphone is its red dot and level bars, wider than the
// microphone, and the row still lies inside the box's border, the CEO's and
// a goblin's, on a phone, a narrow desktop and a wide one: the usage pies,
// the recording control and every button whole, where the model's name, and
// on the smallest phone its effort after it, may give way with an ellipsis
// (#1987). On a phone the usage pies give their room to the recording until
// he stops, while desktop keeps them (the Overlord's mobile corrections,
// 2026-11-04).
test.describe("while he dictates", () => {
  for (const [label, width, height, isTouch] of [["a wide desktop", 1920, 1080, false], ["the desktop side panel", 1440, 960, false], ["a narrow desktop", 1024, 768, false], ["a phone", 390, 844, true], ["a small phone", 360, 640, true]] as const) {
    test.describe(`on ${label}`, () => {
      test.use({ viewport: { width, height }, hasTouch: isTouch, isMobile: isTouch });

      for (const who of ["CEO", "goblin"] as const) {
        test(`the ${who}'s row lies inside the box with its usage pies, its recording control and every button whole`, async ({ page }) => {
          // Arrange: a session whose usage is known.
          const name = who === "CEO" ? "CEO" : NAME;
          await open(page, snapshot({ subscriptions: [WEEK] }), "rebase onto main", { ...PAGE, usage: USAGE });
          if (who === "CEO") await page.keyboard.press("Control+Alt+1");
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
          expect(shown.names).toEqual(["Add files", "Speech history", ...isTouch ? ["Clear message", "Change model", "Stop dictating"] : ["Change model", "Listening", "Usage"]]);
          expect(shown.cut, "no control's words cut").toEqual([]);
          expect(shown.rows, "one row").toBe(1);
          expect(shown.isRowScrolled, "the row never scrolls sideways").toBe(false);
          expect(shown.modelEnd).toBe("ellipsis");

          // Act: he stops.
          if (isTouch) await recording.tap();
          else await page.mouse.up();

          // Assert: the microphone is back, and the row with it, its usage
          // pies included.
          await expect(composer.getByRole("button", { name: isTouch ? "Dictate" : "Hold to dictate", exact: true })).toBeVisible();
          await expect(composer.locator(".usage-pie").first()).toBeVisible();
          await expect.poll(async () => (await row()).outside).toEqual([]);
        });
      }

      // The Overlord's mobile corrections, 2026-11-04: a second or later
      // dictated insert keeps its caret and newest passage in view, with the
      // phone's keyboard down while he speaks.
      if (isTouch) test("each dictated insert into a draft taller than the box keeps the caret and the newest words in view", async ({ page }) => {
        // Arrange: a draft past the box's cap, its keyboard put away.
        await open(page, snapshot(), "rebase onto main");
        await openGoblin(page);
        const composer = box(page);
        const draft = Array.from({ length: 12 }, (_, at) => "line " + (at + 1)).join("\n");
        await field(page).fill(draft);
        await field(page).blur();
        const end = () => field(page).evaluate((textarea: HTMLTextAreaElement) => ({ value: textarea.value, caret: textarea.selectionStart, hidden: textarea.scrollHeight - textarea.clientHeight - textarea.scrollTop }));
        expect((await end()).hidden, "the box is past its cap and shows its end").toBeLessThanOrEqual(1);

        for (const times of [1, 2, 3]) {
          // Act: a dictation.
          const begun = recordingBegins(page);
          await composer.getByRole("button", { name: "Dictate", exact: true }).tap();
          const stop = composer.getByRole("button", { name: "Stop dictating", exact: true });
          await expect(stop.locator(".voice-bars")).toBeVisible();
          await begun;
          await page.waitForTimeout(1200);
          await stop.tap();

          // Assert: the words land at the end, after the caret, in view.
          await expect(field(page)).toHaveValue(draft + " rebase onto main".repeat(times));
          const shown = await end();
          expect(shown.caret, "the caret after the newest words").toBe(shown.value.length);
          expect(shown.hidden, "the newest words in view after insert " + times).toBeLessThanOrEqual(1);
        }
      });
    });
  }
});

// The Overlord, 2026-11-04: "Cancel button doesn't work in the modals". Every
// way out of a Modern chat popover, its close, its Cancel, a press
// outside it, its own control pressed again and Escape where there is a
// keyboard, closes it with nothing changed: the draft and the chat style stay
// as they were, nothing is typed into the terminal, nothing is sent and no
// file chooser opens, for the CEO and a goblin, on a phone and on the desktop.
type Way = "its close" | "a press outside" | "its control again" | "Escape";
const POPOVERS = [
  { what: "Speech history", control: "Speech history", shown: (page: Page, name: string) => box(page, name).getByRole("dialog", { name: "Speech history" }), ways: ["its close", "a press outside", "Escape"] as Way[], isTouchOnly: false },
  { what: "the add menu", control: "Add files", shown: (page: Page, name: string) => box(page, name).locator(".composer-add-popover"), ways: ["a press outside", "its control again"] as Way[], isTouchOnly: true },
  { what: "the model menu", control: "Change model", shown: (page: Page, name: string) => box(page, name).getByRole("menu", { name: "Model" }), ways: ["a press outside", "its control again", "Escape"] as Way[], isTouchOnly: false },
  { what: "the chat style menu", control: "Chat style", shown: (page: Page) => page.locator(".context-pane .panel-header").getByRole("menu", { name: "Chat style" }), ways: ["a press outside", "its control again", "Escape"] as Way[], isTouchOnly: false },
] as const;
const DRAFT = "keep this draft";
// A session's pane once its terminal has connected, so what the relay says
// reaches it.
async function openConnected(page: Page, relay: Relay, who: "CEO" | "goblin") {
  const views = relay.views;
  if (who === "CEO") await page.keyboard.press("Control+Alt+1");
  else await openGoblin(page);
  await expect.poll(() => relay.views).toBeGreaterThan(views);
}
for (const who of ["CEO", "goblin"] as const) {
  for (const width of [1440, 390]) {
    test.describe(`the ${who}'s Modern chat at ${width}px`, () => {
      const isTouch = width === 390;
      test.use({ viewport: { width, height: 900 }, hasTouch: isTouch, isMobile: isTouch });
      for (const popover of POPOVERS.filter((each) => isTouch || !each.isTouchOnly)) {
        for (const way of popover.ways.filter((each) => !isTouch || each !== "Escape")) {
          test(`${way} closes ${popover.what} with nothing changed`, async ({ page }) => {
            // Arrange
            const relay = await open(page);
            const name = who === "CEO" ? "CEO" : NAME;
            await page.route("**/api/dictations", (route) => route.fulfill({ json: { messages: [] } }));
            await page.route("**/api/engines", (route) => route.fulfill({ json: { harnesses: [] } }));
            const sent: string[] = [];
            page.on("request", (request) => { if (request.method() !== "GET") sent.push(request.method() + " " + new URL(request.url()).pathname); });
            let isChooserOpened = false;
            page.on("filechooser", () => { isChooserOpened = true; });
            await openConnected(page, relay, who);
            relay.say({ type: "screen", asking: false, open: false, working: false, submission: "" });
            await field(page, name).fill(DRAFT);
            const before = { typed: typed(relay), style: await page.evaluate(() => localStorage.getItem("ceo-chat-style")), sent: sent.length };
            const press = (target: Locator) => isTouch ? target.tap() : target.click();
            const control = popover.control === "Chat style" ? page.locator(".context-pane .panel-header").getByRole("button", { name: "Chat style", exact: true }) : box(page, name).getByRole("button", { name: popover.control, exact: true });
            await press(control);
            await expect(popover.shown(page, name)).toBeVisible();

            // Act
            if (way === "its close") await press(popover.shown(page, name).getByRole("button", { name: "Close speech history", exact: true }));
            else if (way === "a press outside") await press(page.getByRole("region", { name: "Messages", exact: true }).getByRole("log"));
            else if (way === "its control again") await press(control);
            else await page.keyboard.press("Escape");

            // Assert
            await expect(popover.shown(page, name)).toHaveCount(0);
            await expect(field(page, name)).toHaveValue(DRAFT);
            await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
            await page.waitForTimeout(300);
            expect(typed(relay), "nothing is typed into the terminal").toBe(before.typed);
            expect(await page.evaluate(() => localStorage.getItem("ceo-chat-style")), "the chat style stays").toBe(before.style);
            expect(sent.slice(before.sent), "nothing is sent").toEqual([]);
            expect(isChooserOpened, "no file chooser opens").toBe(false);
          });
        }
      }

      test("the list of the harness's own screen sends its Escape alone on Cancel, keeps the draft, and goes once the screen closes", async ({ page }) => {
        // Arrange
        const relay = await open(page);
        const name = who === "CEO" ? "CEO" : NAME;
        await openConnected(page, relay, who);
        await field(page, name).fill(DRAFT);
        relay.say({ type: "screen", asking: true, open: true, working: false, submission: "menu-1", panel: { rows: ["Select model"], choices: [{ text: "Default", number: 1, focused: true }, { text: "Opus", number: 2 }], hints: ["Enter to confirm · Esc to exit"] } });
        const list = page.getByRole("group", { name: name + " asks", exact: true });
        await expect(list).toBeVisible();
        const before = typed(relay);

        // Act
        await (isTouch ? list.getByRole("button", { name: "Cancel", exact: true }).tap() : list.getByRole("button", { name: "Cancel", exact: true }).click());

        // Assert
        await expect.poll(() => typed(relay).slice(before.length)).toBe("\x1b");
        relay.say({ type: "screen", asking: false, open: false, working: false, submission: "" });
        await expect(list).toHaveCount(0);
        await expect(field(page, name)).toHaveValue(DRAFT);
        await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
      });
      test("a local login dialog shows its instructions and result, and Continue sends Enter without touching the draft", async ({ page }) => {
        const relay = await open(page);
        const name = who === "CEO" ? "CEO" : NAME;
        await openConnected(page, relay, who);
        await field(page, name).fill(DRAFT);
        const before = typed(relay);
        const panel = page.getByRole("group", { name: name + " asks", exact: true });
        relay.say({ type: "screen", asking: false, open: true, working: false, submission: "login-1", panel: { rows: ["Sign in", "Paste your code below"], choices: [], hints: ["Press Enter to submit · Esc to cancel"] } });
        await expect(panel).toContainText("Paste your code below");
        relay.say({ type: "screen", asking: false, open: true, working: false, submission: "login-1", panel: { rows: ["Login successful"], choices: [], hints: ["Press Enter to continue"] } });
        await expect(panel).toContainText("Login successful");
        await expect(panel).toContainText("Press Enter to continue");
        await panel.getByRole("button", { name: "Continue", exact: true }).click();
        await expect.poll(() => typed(relay).slice(before.length)).toBe("\r");
        relay.say({ type: "screen", asking: false, open: false, working: false, submission: "" });
        await expect(panel).toHaveCount(0);
        await expect(field(page, name)).toHaveValue(DRAFT);
      });

      test("a menu the harness drew for something other than his own input is not shown and takes no keys", async ({ page }) => {
        // Arrange
        const relay = await open(page);
        const name = who === "CEO" ? "CEO" : NAME;
        await openConnected(page, relay, who);
        const list = page.getByRole("group", { name: name + " asks", exact: true });
        const before = typed(relay);

        // Act: the screen is bound to no input he submitted, as one a
        // watcher's wake line or a board delivery drew is not.
        relay.say({ type: "screen", asking: true, open: true, working: false, submission: "", panel: { rows: ["Select model"], choices: [{ text: "Default", number: 1, focused: true }, { text: "Opus", number: 2 }], hints: ["Enter to confirm · Esc to exit"] } });
        await page.waitForTimeout(300);

        // Assert: nothing of that screen shows, and its keys stay his.
        await expect(list).toHaveCount(0);
        await field(page, name).press("ArrowDown");
        await field(page, name).press("Enter");
        await page.waitForTimeout(300);
        expect(typed(relay), "no key of the screen reaches the terminal").toBe(before);
      });
    });
  }
}
