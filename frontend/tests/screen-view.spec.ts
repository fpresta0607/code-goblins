import { expect, test, type Locator, type Page, type WebSocketRoute } from "./site";
import { card, GOBLIN, NAME, open, openGoblin, OTHER, snapshot } from "./session-board";

// A goblin's own browser, live in its panel's Screen view (#2361): the board
// shows the page it has open, streams only while the view is on screen in a
// visible tab, and lets him take control from the board on his PC and hand
// it back with a few words. The supervisor's relay is played here: it tells
// the view who drives the browser, sends it frames, and records what the
// view sends.

const BROWSING = snapshot({}, { browser: true });
// IN_USE is a goblin whose browser runs and which it sent a command a short
// time ago (#2537).
const IN_USE = snapshot({}, { browser: true, browser_in_use: true });
const STATE = { type: "state", is_driven: false, can_drive: true, title: "Sign in", host: "shop.example.com", has_page: true };

interface ScreenRelay {
  // views counts the connections the board opened and closed those it
  // closed; said is everything the views sent, in order.
  views: number;
  closed: number;
  addresses: string[];
  said: Record<string, unknown>[];
  // tell sends the newest view a change of the browser's state, and frame a
  // frame, saying whether a view was open to take it.
  tell: (changes: Record<string, unknown>) => void;
  frame: (frame: Frame) => boolean;
}

interface Frame { data: string; width: number; height: number }

async function relayScreens(page: Page, first: Record<string, unknown> = STATE): Promise<ScreenRelay> {
  let newest: WebSocketRoute | null = null;
  let isOpen = false;
  let state = first;
  const relay: ScreenRelay = {
    views: 0, closed: 0, addresses: [], said: [],
    tell: (changes) => {
      state = { ...state, ...changes };
      if (isOpen) newest?.send(JSON.stringify(state));
    },
    frame: ({ data, width, height }) => {
      if (isOpen) newest?.send(JSON.stringify({ type: "frame", data, width, height }));
      return isOpen;
    },
  };
  await page.routeWebSocket("**/api/screen?*", (socket) => {
    relay.views++;
    relay.addresses.push(socket.url());
    newest = socket;
    isOpen = true;
    socket.onMessage((data) => { relay.said.push(JSON.parse(String(data)) as Record<string, unknown>); });
    socket.onClose(() => {
      relay.closed++;
      if (newest === socket) isOpen = false;
    });
    socket.send(JSON.stringify(state));
  });
  return relay;
}

// aFrame is a JPEG of a page width by height, as the browser's screencast
// sends one, drawn in the board's own page.
const aFrame = (page: Page, width: number, height: number): Promise<string> => page.evaluate(([width, height]) => {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d")!;
  context.fillStyle = "#f4f1e8";
  context.fillRect(0, 0, width, height);
  context.fillStyle = "#1f6f50";
  context.fillRect(width / 4, height / 3, width / 2, height / 6);
  return canvas.toDataURL("image/jpeg").split(",")[1];
}, [width, height]);

// fontOf is the family and weight a control is drawn in, and boardFont those
// of the board's own primary button, which the Screen view's controls use
// wherever the view is drawn.
const fontOf = (control: Locator) => control.evaluate((element) => getComputedStyle(element).fontFamily + " " + getComputedStyle(element).fontWeight);
const boardFont = (page: Page) => page.evaluate(() => getComputedStyle(document.body).fontFamily + " 700");
const acks = (relay: ScreenRelay) => relay.said.filter((message) => message.type === "ack").length;
const screen = (page: Page) => page.getByRole("region", { name: NAME + "'s browser", exact: true });
interface Rect { x: number; y: number; width: number; height: number }
const isApart = (a: Rect, b: Rect) => a.x >= b.x + b.width || b.x >= a.x + a.width || a.y >= b.y + b.height || b.y >= a.y + a.height;
const switchTo = (page: Page, view: "Screen" | "Chat") => page.getByRole("group", { name: "Panel view", exact: true }).getByRole("button", { name: view, exact: true }).click();
const tellBoard = (page: Page, next: object) => page.evaluate((detail) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail })), next);
// pixel is the colour the screen's canvas holds at x, y, and isMark says it
// is the goblin's pointer ring.
const pixel = (page: Page, x: number, y: number) => screen(page).locator("canvas").evaluate((canvas: HTMLCanvasElement, [x, y]) => Array.from(canvas.getContext("2d")!.getImageData(x, y, 1, 1).data.slice(0, 3)), [x, y]);
const isMark = ([red, green, blue]: number[]) => red > 170 && green < 120 && blue < 80;

// setHidden plays the tab going out of sight and coming back, as minimizing
// the window or switching tabs does.
const setHidden = (page: Page, isHidden: boolean) => page.evaluate((hidden) => {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => hidden ? "hidden" : "visible" });
  Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
  document.dispatchEvent(new Event("visibilitychange"));
}, isHidden);

async function openScreen(page: Page, relay: ScreenRelay) {
  await openGoblin(page);
  await switchTo(page, "Screen");
  await expect(screen(page)).toBeVisible();
  await expect.poll(() => relay.views).toBe(1);
}

for (const { width, height, label } of [{ width: 1440, height: 1200, label: "desktop" }, { width: 390, height: 844, label: "phone" }]) {
  test.describe(`at ${label} width`, () => {
    test.use({ viewport: { width, height } });

    test("the Screen view shows the goblin's page by its title and host and asks for each frame once it drew the last", async ({ page }, testInfo) => {
      // Arrange
      const relay = await relayScreens(page);
      await open(page, BROWSING);
      await openScreen(page, relay);
      const frame = { data: await aFrame(page, 1280, 800), width: 1280, height: 800 };

      // Act
      relay.frame(frame);

      // Assert
      const canvas = screen(page).getByRole("img", { name: NAME + "'s browser, showing Sign in on shop.example.com" });
      await expect(canvas).toBeVisible();
      await expect.poll(() => acks(relay)).toBe(1);
      const address = new URL(relay.addresses[0]);
      expect(Object.fromEntries(address.searchParams)).toEqual({ task: GOBLIN.id, generation: GOBLIN.generation, token: "fixture" });
      await expect(screen(page).getByText("Sign in", { exact: true })).toBeVisible();
      await expect(screen(page).getByText("shop.example.com")).toBeVisible();
      const box = (await canvas.boundingBox())!;
      expect(box.x + box.width).toBeLessThanOrEqual(width);
      expect(box.width / box.height).toBeCloseTo(1.6, 1);
      // On a phone the screen's box takes the picture's shape, so the page
      // never sits small between two bands (decision 1019951).
      if (label === "phone") {
        const stage = (await screen(page).locator(".screen-stage").boundingBox())!;
        expect(Math.abs(stage.height - box.height)).toBeLessThanOrEqual(2);
      }
      await testInfo.attach(`screen-${label}.png`, { body: await page.screenshot(), contentType: "image/png" });
    });

    test("Take control and Hand back work from the keyboard, and nothing typed on the screen stays on the board", async ({ page }, testInfo) => {
      // Arrange
      const relay = await relayScreens(page);
      const logged: string[] = [];
      page.on("console", (entry) => logged.push(entry.text()));
      await open(page, BROWSING);
      await openScreen(page, relay);
      relay.frame({ data: await aFrame(page, 640, 400), width: 1280, height: 800 });
      const take = screen(page).getByRole("button", { name: "Take control", exact: true });
      const typed = "hunter2-canary";

      // Act: he takes control with the keyboard.
      await take.focus();
      await page.keyboard.press("Enter");
      await expect.poll(() => relay.said.some((message) => message.type === "take")).toBe(true);
      relay.tell({ is_driven: true });

      // Assert: the keyboard is on the page's screen, which says so.
      const driving = screen(page).getByRole("application", { name: new RegExp("^" + NAME + "'s browser, showing Sign in on shop.example.com") });
      await expect(driving).toBeFocused();
      await expect(screen(page).getByRole("status")).toContainText("You are driving " + NAME + "'s browser");
      expect(await fontOf(screen(page).getByRole("button", { name: "Hand back", exact: true }))).toBe(await boardFont(page));
      await testInfo.attach(`driving-${label}.png`, { body: await page.screenshot(), contentType: "image/png" });

      // Act: he types into the page, leaves the screen with F6, and hands the
      // browser back with what he did.
      await page.keyboard.type(typed);
      await page.keyboard.press("Enter");
      await page.keyboard.press("F6");
      const words = screen(page).getByRole("textbox", { name: "What you did" });
      await expect(words).toBeFocused();
      await page.keyboard.type("Signed in, the code was on my phone");
      await page.keyboard.press("Enter");

      // Assert
      await expect.poll(() => relay.said.find((message) => message.type === "hand-back")).toEqual({ type: "hand-back", reason: "Signed in, the code was on my phone" });
      const keys = relay.said.filter((message) => message.type === "key" && message.event === "keyDown");
      expect(keys.map((message) => message.text).join("")).toBe(typed + "\r");
      expect(keys.every((message) => message.key !== "F6")).toBe(true);
      relay.tell({ is_driven: false });
      await expect(take).toBeVisible();
      await expect(words).toBeHidden();
      expect(await page.content()).not.toContain(typed);
      expect(await page.locator("body").ariaSnapshot()).not.toContain(typed);
      expect(logged.join("\n")).not.toContain(typed);
    });
  });
}

// The proof the stream stops: a view out of sight closes its connection, so
// the relay, which stops the browser's screencast as a view closes, has no
// view to send a frame to, and no frame reaches the board until the view is
// seen again.
test("the Screen view streams only while it is seen: a hidden tab, the Chat view or leaving the goblin's panel closes it", async ({ page }) => {
  // Arrange
  const relay = await relayScreens(page);
  await open(page, BROWSING);
  await openScreen(page, relay);
  const frame = { data: await aFrame(page, 640, 400), width: 1280, height: 800 };
  relay.frame(frame);
  await expect.poll(() => acks(relay)).toBe(1);

  // Act: the tab goes out of sight.
  await setHidden(page, true);

  // Assert: the view closed, so no frame is sent while it is hidden.
  await expect.poll(() => relay.closed).toBe(1);
  expect(relay.frame(frame)).toBe(false);
  expect(acks(relay)).toBe(1);

  // Act: the tab is seen again.
  await setHidden(page, false);

  // Assert: the view opens again and draws.
  await expect.poll(() => relay.views).toBe(2);
  expect(relay.frame(frame)).toBe(true);
  await expect.poll(() => acks(relay)).toBe(2);

  // Act: he switches to the conversation.
  await switchTo(page, "Chat");

  // Assert
  await expect.poll(() => relay.closed).toBe(2);
  await expect(screen(page)).toHaveCount(0);

  // Act: back to the screen, then the panel goes back to the CEO.
  await switchTo(page, "Screen");
  await expect.poll(() => relay.views).toBe(3);
  await page.getByRole("button", { name: "Back to the CEO", exact: true }).click();

  // Assert
  await expect.poll(() => relay.closed).toBe(3);
  await expect(screen(page)).toHaveCount(0);
  expect(relay.frame(frame)).toBe(false);
});

test("a click on the screen lands on the page where it was drawn", async ({ page }) => {
  // Arrange
  const relay = await relayScreens(page, { ...STATE, is_driven: true });
  await open(page, BROWSING);
  await openScreen(page, relay);
  relay.frame({ data: await aFrame(page, 640, 400), width: 1280, height: 800 });
  const canvas = screen(page).getByRole("application");
  await expect(canvas).toBeVisible();
  const box = (await canvas.boundingBox())!;

  // Act
  await page.mouse.click(box.x + box.width / 4, box.y + box.height / 2);

  // Assert
  await expect.poll(() => relay.said.filter((message) => message.type === "mouse" && message.event !== "mouseMoved").length).toBe(2);
  const [pressed, released] = relay.said.filter((message) => message.type === "mouse" && message.event !== "mouseMoved");
  expect(pressed).toMatchObject({ event: "mousePressed", button: "left", click_count: 1 });
  expect(released).toMatchObject({ event: "mouseReleased", button: "left" });
  expect(pressed.x as number).toBeCloseTo(320, -1);
  expect(pressed.y as number).toBeCloseTo(400, -1);
});

test("a paste on the driven screen goes to the page as text, at most 4000 characters, and never shows on the board", async ({ page }) => {
  // Arrange
  const relay = await relayScreens(page, { ...STATE, is_driven: true });
  await open(page, BROWSING);
  await openScreen(page, relay);
  relay.frame({ data: await aFrame(page, 640, 400), width: 1280, height: 800 });
  const canvas = screen(page).getByRole("application");
  await expect(canvas).toBeVisible();
  const pasted = "pasted-canary-" + "x".repeat(4000);

  // Act
  await canvas.evaluate((element, text) => {
    const data = new DataTransfer();
    data.setData("text/plain", text);
    element.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
  }, pasted);

  // Assert
  await expect.poll(() => relay.said.filter((message) => message.type === "text")).toEqual([{ type: "text", text: pasted.slice(0, 4000) }]);
  expect(await page.content()).not.toContain("pasted-canary");
});

test("a Screen view whose browser closed gives way to the conversation and closes its socket", async ({ page }) => {
  // Arrange
  const relay = await relayScreens(page);
  await open(page, BROWSING);
  await openScreen(page, relay);
  const views = page.getByRole("group", { name: "Panel view", exact: true });

  // Act
  await page.evaluate((next) => window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: next })), snapshot({ revision: 2 }, { browser: false }));

  // Assert
  await expect(screen(page)).toHaveCount(0);
  await expect.poll(() => relay.closed).toBe(1);
  await expect(views.getByRole("button", { name: "Screen", exact: true })).toHaveCount(0);
  await expect(views.getByRole("button", { name: "Chat", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
});

test("a view on another machine watches the browser, can hand it back, and cannot take control", async ({ page }) => {
  // Arrange
  const relay = await relayScreens(page, { ...STATE, can_drive: false, drive_note: "Take control from the board on this PC." });
  await open(page, BROWSING);
  await openScreen(page, relay);

  // Assert: no Take control, and why.
  await expect(screen(page).getByText("Take control from the board on this PC.")).toBeVisible();
  await expect(screen(page).getByRole("button", { name: "Take control" })).toHaveCount(0);

  // Act: he drives it from the board on his PC.
  relay.tell({ is_driven: true });

  // Assert: this view can hand it back, but sends nothing to the page.
  await expect(screen(page).getByRole("button", { name: "Hand back", exact: true })).toBeVisible();
  await expect(screen(page).getByRole("application")).toHaveCount(0);
});

test("only a goblin whose browser runs offers its screen, and its card's Browser active opens it full size in a tab of its own", async ({ page, context }) => {
  // Arrange
  const relay = await relayScreens(page);
  await open(page, IN_USE);
  const opener = page.locator(".task-board .task-card-shell").filter({ hasText: NAME }).getByRole("button", { name: "Browser active: open " + NAME + "'s screen in a new tab" });
  const otherShell = page.locator(".task-board .task-card-shell").filter({ hasText: OTHER.goblin_name });
  const clock = page.locator(".task-board .task-card-shell").filter({ hasText: NAME }).locator(".card-clock");

  // Assert: the card's Browser active sits clear of its clock on the line
  // they share in a narrow column.
  const [openerBox, clockBox] = [(await opener.boundingBox())!, (await clock.boundingBox())!];
  expect(Math.abs(openerBox.y - clockBox.y)).toBeLessThan(openerBox.height);
  expect(isApart(openerBox, clockBox)).toBe(true);

  // Assert: a goblin with no browser offers no screen at all.
  await expect(otherShell.getByRole("button", { name: /Browser active/ })).toHaveCount(0);
  await card(page, OTHER.goblin_name).click();
  await expect(page.getByRole("group", { name: "Panel view", exact: true }).getByRole("button", { name: "Screen" })).toHaveCount(0);

  // Act
  const opened = context.waitForEvent("page");
  await opener.click();
  const tab = await opened;

  // Assert: the tab is the board's address of his screen, which shows it.
  const address = new URL(tab.url());
  expect(address.pathname + address.search).toBe("/?screen=" + GOBLIN.id);
  await tab.close();
  await page.goto(address.pathname + address.search);
  await expect(page.getByRole("heading", { level: 1, name: NAME + "'s browser" })).toBeVisible();
  await expect.poll(() => relay.views).toBe(1);
  relay.frame({ data: await aFrame(page, 1280, 800), width: 1280, height: 800 });
  await expect(page.getByRole("img", { name: NAME + "'s browser, showing Sign in on shop.example.com" })).toBeVisible();
  await expect.poll(() => acks(relay)).toBe(1);

  // Act: he drives it in a window too short for the frame at its full width.
  await page.setViewportSize({ width: 1440, height: 900 });
  relay.tell({ is_driven: true });

  // Assert: the screen shows the whole frame inside its stage.
  const driving = page.getByRole("application", { name: new RegExp("^" + NAME + "'s browser, showing Sign in on shop.example.com") });
  await expect(driving).toBeVisible();
  const [drawn, stage] = [(await driving.boundingBox())!, (await page.locator(".screen-stage").boundingBox())!];
  expect(drawn.y + drawn.height).toBeLessThanOrEqual(stage.y + stage.height);
  expect(drawn.x + drawn.width).toBeLessThanOrEqual(stage.x + stage.width);
  expect(drawn.width / drawn.height).toBeCloseTo(1.6, 1);
});

// A goblin's click shows on its screen as a ring at the place it clicked
// (#2537), drawn over the frame as the board hears of the click and at no
// other time, so a screen nobody's goblin clicks on costs nothing; it is not
// drawn while he drives.
test("the goblin's click shows as a ring at its place on the page, drawn once as it arrives", async ({ page }, testInfo) => {
  // Arrange
  await page.addInitScript(() => {
    const draw = CanvasRenderingContext2D.prototype.drawImage as (...args: unknown[]) => void;
    const counted = window as unknown as { drawn: number };
    counted.drawn = 0;
    CanvasRenderingContext2D.prototype.drawImage = function (this: CanvasRenderingContext2D, ...args: unknown[]) {
      counted.drawn++;
      draw.apply(this, args);
    } as typeof CanvasRenderingContext2D.prototype.drawImage;
  });
  const drawn = () => page.evaluate(() => (window as unknown as { drawn: number }).drawn);
  const relay = await relayScreens(page);
  await open(page, BROWSING);
  await openScreen(page, relay);
  relay.frame({ data: await aFrame(page, 1280, 800), width: 1280, height: 800 });
  await expect.poll(() => acks(relay)).toBe(1);
  const ring = [[211, 120], [189, 120], [200, 131], [200, 109]];
  const isRingShown = async () => (await Promise.all(ring.map(([x, y]) => pixel(page, x, y)))).every(isMark);
  expect(await isRingShown()).toBe(false);
  const drawnBefore = await drawn();

  // Act
  relay.tell({ pointer: { x: 200, y: 120 } });

  // Assert: a ring round the place, its middle the page's own.
  await expect.poll(isRingShown).toBe(true);
  expect(isMark(await pixel(page, 200, 120))).toBe(false);
  expect(await drawn()).toBe(drawnBefore + 1);
  await testInfo.attach("screen-pointer.png", { body: await page.screenshot(), contentType: "image/png" });
  await page.waitForTimeout(1000);
  expect(await drawn()).toBe(drawnBefore + 1);

  // Act: he takes control.
  relay.tell({ is_driven: true });

  // Assert
  await expect.poll(async () => isMark(await pixel(page, 211, 120))).toBe(false);
});

// A goblin's card reads Browser active only while its goblin uses its
// browser (#2537): a browser it holds but sent no command a short time ago
// shows on its Screen view and not on its card.
test("a card reads Browser active only while its goblin uses its browser, and its screen stays while the browser runs", async ({ page }, testInfo) => {
  // Arrange
  await open(page, BROWSING);
  const shell = page.locator(".task-board .task-card-shell").filter({ hasText: NAME });
  const opener = shell.getByRole("button", { name: "Browser active: open " + NAME + "'s screen in a new tab" });
  const screenButton = page.getByRole("group", { name: "Panel view", exact: true }).getByRole("button", { name: "Screen", exact: true });
  await card(page, NAME).click();

  // Assert: its browser runs unused.
  await expect(screenButton).toBeVisible();
  await expect(shell.getByText("Browser active")).toHaveCount(0);
  await testInfo.attach("card-browser-unused.png", { body: await shell.screenshot(), contentType: "image/png" });

  // Act: its goblin uses it.
  await tellBoard(page, snapshot({ revision: 2 }, { browser: true, browser_in_use: true }));

  // Assert
  await expect(opener).toBeVisible();
  await testInfo.attach("card-browser-in-use.png", { body: await shell.screenshot(), contentType: "image/png" });

  // Act: a while passes with no command.
  await tellBoard(page, snapshot({ revision: 3 }, { browser: true }));

  // Assert
  await expect(opener).toHaveCount(0);
  await expect(screenButton).toBeVisible();
});
