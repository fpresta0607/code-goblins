import { expect, openTaskList, type Page, servePages, test } from "./site";

const URL = "https://my-desktop.example.ts.net:4311";
const SERVE = "tailscale serve --bg --https=4311 http://127.0.0.1:4310";
// The board's push key, and an app's subscription, as their sizes are: the
// test's browser takes them as they are, so no real key is needed.
const SERVER_KEY = "B" + "A".repeat(86);
const APP = { endpoint: "https://web.push.apple.com/QGuQyavXutnMH", keys: { p256dh: "B" + "Q".repeat(86), auth: "A".repeat(22) } };
const OFF = { enabled: false, ready: false, tailscale: "ready", scrawl: false };
const ON = { enabled: true, ready: true, url: URL, login: "owner@example.com", scrawl: false };
const SCRAWL_ON = { ...ON, scrawl: true, scrawl_state: "on" };
const BYPASSED = { ...ON, scrawl_state: "bypassed", scrawl_issue: "Your own Tailscale route serves Scrawl to your tailnet without the owner check. Remove it in Tailscale, then turn Scrawl reviews on.", scrawl_fix: "tailscale serve --https=4388 off" };

type Status = Record<string, unknown>;

const BOARD = { healthy: true, instance: "fixture", revision: 1, attention: [], tasks: [], afk: { state: "off" }, cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo", cfo_terminal_since: "2026-10-08T09:00:00Z", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo" }] };

// open shows the CFO's Task panel's Remote access section, as this PC sees
// the board or, with origin, as a phone sees the private board. The
// supervisor is played by one status that each press but a fix moves on, and
// a press can be slowed, during which a read answers what it read before the
// press. The board's stream is the test's to move on with push, and machines
// are the machines on the tailnet whose boards answer.
async function open(page: Page, first: Status, { fail = false, origin = "", slow = false, machines = [] as Status[] } = {}) {
  const actions: unknown[] = [];
  const reads = { count: 0 };
  let status = first;
  let before: Status | null = null;
  await page.addInitScript((data) => {
    class LiveStream extends EventTarget {
      onerror: (() => void) | null = null;
      // The board's own stream takes the snapshots; another, such as the
      // CFO's message stream, is left without a word.
      constructor(url: string | URL) {
        super();
        if (new globalThis.URL(url, location.href).pathname !== "/api/events") return;
        Object.assign(window, { pushSnapshot: (next: string) => this.dispatchEvent(new MessageEvent("snapshot", { data: next })) });
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data })));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: LiveStream });
  }, JSON.stringify(BOARD));
  await page.route("**/api/**", (route) => route.fulfill({ json: {} }));
  await page.route("**/api/remote/machines", (route) => route.fulfill({ json: { machines } }));
  const pushes: unknown[] = [];
  await page.route("**/api/push", (route) => {
    if (route.request().method() === "POST") pushes.push(route.request().postDataJSON());
    return route.fulfill({ json: { key: SERVER_KEY } });
  });
  await page.route("**/api/remote", async (route) => {
    if (route.request().method() !== "POST") {
      reads.count++;
      return route.fulfill({ json: before ?? status });
    }
    const body: { on?: boolean; scrawl?: boolean; fix?: string } = route.request().postDataJSON();
    actions.push(body);
    expect(route.request().headers()["x-cfo-token"]).toBe("fixture");
    if (fail) return route.fulfill({ status: 409, json: { error: "Tailscale is not running on this PC; start Tailscale and sign in" } });
    if (body.fix !== undefined) return route.fulfill({ json: { ...status, run_id: "remote-fix-" + body.fix + "-1" } });
    before = slow ? status : null;
    status = body.scrawl === undefined ? body.on ? ON : OFF : body.scrawl ? SCRAWL_ON : ON;
    if (slow) await new Promise((resolve) => setTimeout(resolve, 1500));
    before = null;
    return route.fulfill({ json: status });
  });
  if (origin) {
    await servePages(page.context(), origin);
    await page.goto(origin + "/");
  } else await page.goto("/");
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await openTaskList(page);
  await page.locator(".disclosure.remote-access > summary").click();
  const push = (snapshot: Status) => page.evaluate((data) => (window as unknown as { pushSnapshot: (data: string) => void }).pushSnapshot(data), JSON.stringify({ ...BOARD, ...snapshot }));
  return { actions, reads, push, pushes, panel: page.getByRole("region", { name: "Remote access" }), setStatus: (next: Status) => { status = next; } };
}

test.use({ viewport: { width: 390, height: 844 } });

test("a signed-in Tailscale turns on with its switch, keeps its link, and Scrawl reviews switch under it", async ({ page }, testInfo) => {
  // Arrange
  const { actions, panel } = await open(page, OFF);
  const tailscale = panel.getByRole("switch", { name: "Tailscale", exact: true });
  await expect(tailscale).toHaveAttribute("aria-checked", "false");
  await expect(tailscale).toBeEnabled();

  // Act: turn Tailscale on.
  await tailscale.click();

  // Assert: on, with a green check and the private link to copy, and
  // nothing else to read.
  await expect(tailscale).toHaveAttribute("aria-checked", "true");
  await expect(panel.getByRole("img", { name: "Tailscale works", exact: true })).toBeVisible();
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (text: string) => { document.documentElement.dataset.copied = text; } } });
  });
  await panel.getByRole("button", { name: "Copy " + URL }).click();
  await expect(page.locator("html")).toHaveAttribute("data-copied", URL);
  await expect(panel.getByRole("link")).toHaveCount(1);
  await expect(panel.getByRole("link", { name: URL, exact: true })).toHaveAttribute("href", URL);
  await expect(panel.getByRole("button", { name: "Refresh" })).toHaveCount(0);
  await expect(panel.locator("p")).toHaveCount(0);
  await expect(panel.getByRole("combobox")).toHaveCount(0);

  // Act and assert: Scrawl reviews, indented under Tailscale, on and off.
  const scrawl = panel.locator(".remote-access-option").getByRole("switch", { name: "Scrawl reviews", exact: true });
  await scrawl.click();
  await expect(scrawl).toHaveAttribute("aria-checked", "true");
  await expect(panel.getByRole("img", { name: "Scrawl reviews works", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await panel.screenshot({ path: testInfo.outputPath("remote-access-phone.png") });
  await scrawl.click();
  await expect(scrawl).toHaveAttribute("aria-checked", "false");
  await expect(panel.getByRole("img", { name: "Scrawl reviews works", exact: true })).toHaveCount(0);

  // Act and assert: Tailscale off takes its link and Scrawl reviews with it.
  await tailscale.click();
  await expect(tailscale).toHaveAttribute("aria-checked", "false");
  await expect(panel.getByRole("button", { name: "Copy " + URL })).toHaveCount(0);
  await expect(panel.getByRole("switch", { name: "Scrawl reviews" })).toHaveCount(0);
  expect(actions).toEqual([{ on: true }, { scrawl: true }, { scrawl: false }, { on: false }]);
});

for (const [tailscale, note, isOffered, fix] of [
  ["missing", "Not installed", false, "winget install --exact --id Tailscale.Tailscale"],
  ["stopped", "Not running", false, ""],
  ["signed_out", "Signed out", false, ""],
  ["disconnected", "Disconnected", false, ""],
  ["unsuitable", "Tagged or no MagicDNS", false, ""],
  ["no_https", "HTTPS not approved", true, SERVE],
] as const) {
  test(`Tailscale ${tailscale.replace("_", " ")} keeps the switch off with a few words, and on this PC the command that mends it`, async ({ page }) => {
    // Arrange and act
    const { panel } = await open(page, { ...OFF, tailscale, tailscale_issue: "the supervisor's full reason", ...(fix ? { fix } : {}) });

    // Assert
    const toggle = panel.getByRole("switch", { name: "Tailscale", exact: true });
    await expect(toggle).toHaveAttribute("aria-checked", "false");
    await expect(toggle).toHaveAccessibleDescription(note);
    if (isOffered) await expect(toggle).toBeEnabled(); else await expect(toggle).toBeDisabled();
    await expect(panel.getByRole("button", { name: /^Copy / })).toHaveCount(fix ? 1 : 0);
    await expect(panel.getByRole("button", { name: "Run in PowerShell" })).toHaveCount(fix ? 1 : 0);
    if (fix) await expect(panel.getByRole("button", { name: "Copy " + fix, exact: true })).toBeVisible();
    await expect(panel.getByRole("link")).toHaveCount(0);
    await expect(panel.getByText("the supervisor's full reason")).toHaveCount(0);
  });
}

// The Overlord, 2026-10-18: the command that mends a row has "a play button
// that runs it directly in a PowerShell window, the same way a Command Center
// run card runs a command. It stays in the section, never as a Command Center
// card."
for (const [outcome, after, isMended] of [
  ["failed", { ...OFF, tailscale: "no_https", fix: SERVE }, false],
  ["succeeded", OFF, true],
] as const) {
  test(`a row's command runs in its own terminal in the section, never as a Command Center card, and once it ${outcome} the row is read again`, async ({ page }) => {
    // Arrange
    const { actions, reads, push, panel, setStatus } = await open(page, { ...OFF, tailscale: "no_https", fix: SERVE });
    await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      socket.send(Buffer.from("To approve Serve, visit the link\r\n"));
    });
    const fix = { id: "remote-fix-tailscale-1", identity: "r".repeat(64), title: "Mend Tailscale for remote access", shell: "powershell", command: SERVE, cwd: "C:\\home", remote_fix: "tailscale", created_at: "2026-10-18T12:00:00Z" };
    const play = panel.getByRole("button", { name: "Run in PowerShell" });

    // Act: he presses play, and the supervisor starts its terminal.
    await play.click();
    await push({ revision: 2, runs: [{ ...fix, state: "running", terminal: true }] });

    // Assert: its terminal is in the section, and nothing waits on him.
    await expect(panel.getByRole("region", { name: "Terminal of Mend Tailscale for remote access" })).toBeVisible();
    await expect(play).toBeDisabled();
    expect(actions).toEqual([{ fix: "tailscale", command: SERVE }]);
    await expect(page).toHaveTitle("Code Goblins");
    await expect(page.locator(".run-card")).toHaveCount(0);

    // Act: the command ends.
    setStatus(after);
    const readsBefore = reads.count;
    await push({ revision: 3, runs: [{ ...fix, state: outcome, terminal: false, exit_code: outcome === "failed" ? 1 : 0, output: "Serve is not enabled on your tailnet." }] });

    // Assert
    await expect.poll(() => reads.count).toBeGreaterThan(readsBefore);
    await expect(panel.getByRole("region", { name: /^Terminal of / })).toHaveCount(0);
    if (isMended) await expect(play).toHaveCount(0);
    else {
      await expect(play).toBeEnabled();
      await expect(panel.locator(".click-feedback")).toHaveText(/^Failed/);
    }
  });
}

test("a row's command that fails as it starts, before the board saw it run, still says why and reads the row again", async ({ page }) => {
  // Arrange: he presses play, and the supervisor answers with its run.
  const { reads, push, panel } = await open(page, { ...OFF, tailscale: "no_https", fix: SERVE });
  const play = panel.getByRole("button", { name: "Run in PowerShell" });
  await play.click();
  await expect(play).toBeEnabled();
  const readsBefore = reads.count;

  // Act: the first the board hears of the run is that it failed.
  await push({ revision: 2, runs: [{ id: "remote-fix-tailscale-1", identity: "r".repeat(64), title: "Mend Tailscale for remote access", shell: "powershell", command: SERVE, cwd: "C:\\home", remote_fix: "tailscale", created_at: "2026-10-18T12:00:00Z", state: "failed", exit_code: null, reason: "it could not start: no terminal host" }] });

  // Assert
  await expect.poll(() => reads.count).toBeGreaterThan(readsBefore);
  await expect(panel.locator(".click-feedback")).toHaveText(/^Failed/);
});

// The Overlord, 2026-10-18: "with tailscale connection should we be able to
// see and select the server we want to connect to".
const LAPTOP = "https://laptop.example.ts.net:4311";
const TWO_MACHINES = [{ name: "my-desktop", url: URL, is_this: true }, { name: "laptop", url: LAPTOP, is_this: false }];

test("on a phone, with another machine of his on the tailnet, the Tailscale switch picks the one to connect to, this one by default", async ({ page }) => {
  // Arrange: the laptop's board is asked for and has not opened yet.
  const { panel } = await open(page, ON, { origin: URL, machines: TWO_MACHINES });
  const opened: string[] = [];
  await page.route(LAPTOP + "/", (route) => {
    opened.push(route.request().url());
    return route.abort("aborted");
  });
  const machine = panel.getByRole("combobox", { name: "Machine" });
  await expect(machine).toHaveValue(URL);
  await expect(machine.getByRole("option")).toHaveText(["my-desktop", "laptop"]);

  // Act
  await machine.selectOption({ label: "laptop" });

  // Assert: the laptop's board is opened, and his pick holds meanwhile.
  await expect.poll(() => opened).toEqual([LAPTOP + "/"]);
  await expect(machine).toHaveValue(LAPTOP);
  await expect(machine).toBeDisabled();
});

// Another machine's gate refuses a page that comes from the board on this PC,
// and a phone with no other machine has nothing to pick.
for (const [where, origin, machines] of [
  ["the board on this PC", "", TWO_MACHINES],
  ["a phone with only this PC", URL, TWO_MACHINES.slice(0, 1)],
] as const) {
  test(`${where} shows no machine to pick`, async ({ page }) => {
    // Arrange and act
    const { panel } = await open(page, ON, { origin, machines: [...machines] });

    // Assert
    await expect(panel.getByRole("switch", { name: "Tailscale", exact: true })).toBeVisible();
    await expect(panel.getByRole("combobox")).toHaveCount(0);
  });
}

test("settings that cannot be read leave the switch off with an amber mark that says so", async ({ page }) => {
  // Arrange and act
  const { panel } = await open(page, { enabled: false, ready: false, scrawl: false, attention: "settings", issue: "remote access settings cannot be read; run cfo remote off to revoke access" });

  // Assert
  const toggle = panel.getByRole("switch", { name: "Tailscale", exact: true });
  await expect(toggle).toBeDisabled();
  await expect(toggle).toHaveAccessibleDescription("Settings unreadable");
  await expect(panel.getByRole("img", { name: "remote access settings cannot be read; run cfo remote off to revoke access", exact: true })).toBeVisible();
});

test("Scrawl reviews say what is wrong with an amber mark and a few words, and on this PC the command that mends it", async ({ page }) => {
  // Arrange and act
  const { actions, panel } = await open(page, BYPASSED);

  // Assert
  const scrawl = panel.getByRole("switch", { name: "Scrawl reviews", exact: true });
  await expect(scrawl).toHaveAttribute("aria-checked", "false");
  await expect(scrawl).toHaveAccessibleDescription("Direct route");
  const mark = panel.getByRole("img", { name: BYPASSED.scrawl_issue, exact: true });
  await expect(mark).toBeVisible();
  await mark.focus();
  await expect(mark).toBeFocused();
  await expect(panel.locator(".remote-access-option").getByRole("button", { name: "Copy " + BYPASSED.scrawl_fix, exact: true })).toBeVisible();
  await expect(panel.getByRole("button", { name: /Set up/ })).toHaveCount(0);
  await expect(panel.getByText(BYPASSED.scrawl_issue)).toHaveCount(0);

  // Act and assert: its play button runs the command it shows, for its row.
  await panel.locator(".remote-access-option").getByRole("button", { name: "Run in PowerShell" }).click();
  await expect.poll(() => actions).toEqual([{ fix: "scrawl", command: BYPASSED.scrawl_fix }]);
});

test("a phone on the private board turns Scrawl reviews off but never on, and is shown no commands", async ({ page }) => {
  // Arrange and act
  const { panel, setStatus } = await open(page, { ...SCRAWL_ON, scrawl_fix: "tailscale serve --https=4388 off" }, { origin: URL });

  // Assert
  const tailscale = panel.getByRole("switch", { name: "Tailscale", exact: true });
  const scrawl = panel.getByRole("switch", { name: "Scrawl reviews", exact: true });
  await expect(tailscale).toBeEnabled();
  await expect(scrawl).toBeEnabled();
  await expect(panel.getByRole("button", { name: /^Copy tailscale/ })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Run in PowerShell" })).toHaveCount(0);
  await scrawl.click();
  await expect(scrawl).toHaveAttribute("aria-checked", "false");
  await expect(scrawl).toBeDisabled();
  setStatus(OFF);
  await tailscale.click();
  await expect(tailscale).toHaveAttribute("aria-checked", "false");
  await expect(tailscale).toBeDisabled();
});

// CFO decision 1008966: his private board turns AFK mode off and never on,
// with no words for why, since on hands the CFO his authority and stays on
// the PC.
test("a phone on the private board turns AFK off but never on, and says nothing of it", async ({ page }) => {
  // Arrange
  const { push } = await open(page, ON, { origin: URL });
  const asked: unknown[] = [];
  await page.route("**/api/afk", (route) => {
    asked.push(route.request().postDataJSON());
    return route.fulfill({ json: { state: "off" } });
  });
  const afk = page.getByRole("switch", { name: "AFK mode" });

  // Assert: off, it cannot be turned on and has no tip.
  await expect(afk).toHaveAttribute("aria-checked", "false");
  await expect(afk).toBeDisabled();
  await expect(afk).not.toHaveAttribute("data-tip");

  // Act: AFK mode turned on at the PC reaches the phone, and he turns it off.
  await push({ afk: { state: "on", since: new Date().toISOString(), from: "his own board (goblins-window.exe pid 4242)", decided: 0, held: [] } });
  await expect(afk).toBeEnabled();
  await afk.click();

  // Assert
  await expect.poll(() => asked).toEqual([{ on: false }]);
});

for (const comeBack of ["focus", "visibilitychange"] as const) {
  test(`Tailscale signed in while the section is open is offered when he comes back to the board (${comeBack})`, async ({ page }) => {
    // Arrange
    const { panel, setStatus } = await open(page, { ...OFF, tailscale: "signed_out", tailscale_issue: "Tailscale is signed out on this PC; open Tailscale and sign in" });
    const tailscale = panel.getByRole("switch", { name: "Tailscale", exact: true });
    await expect(tailscale).toBeDisabled();

    // Act: he signs in to Tailscale and comes back to the board.
    setStatus(OFF);
    await page.evaluate((event) => (event === "focus" ? window : document).dispatchEvent(new Event(event)), comeBack);

    // Assert
    await expect(tailscale).toBeEnabled();
    await expect(tailscale).not.toHaveAccessibleDescription("Signed out");
  });
}

// The Overlord, 2026-10-27: "Make sure that whatever on-and-off detections
// are detected, that the setting is reflected, and that there's no errors in
// the board." A change made anywhere else reaches the open setting within
// seconds, with no coming back to the board.
const STOPPED = { ...ON, ready: false, attention: "stopped", issue: "Tailscale is not running on this PC; start Tailscale and sign in" };
for (const [change, before, after, isOn, note] of [
  ["Tailscale stops on this PC", ON, STOPPED, "true", "Not running"],
  ["Tailscale starts again", STOPPED, ON, "true", ""],
  ["cfo remote off", ON, OFF, "false", ""],
  ["cfo remote on", OFF, ON, "true", ""],
  ["a restart brings the board back at another address", { ...ON, ready: false, attention: "board_moved", issue: "The board moved to another address and its private route could not follow. Turn remote access off and on again on this PC." }, ON, "true", ""],
] as const) {
  test(`${change} shows in the open setting within seconds`, async ({ page }, testInfo) => {
    // Arrange
    await page.clock.install();
    const { panel, setStatus } = await open(page, before);
    const tailscale = panel.getByRole("switch", { name: "Tailscale", exact: true });
    await expect(tailscale).toHaveAttribute("aria-checked", before.enabled ? "true" : "false");

    // Act
    setStatus(after);
    await page.clock.runFor(5000);

    // Assert
    await expect(tailscale).toHaveAttribute("aria-checked", isOn);
    if (note) {
      await expect(tailscale).toHaveAccessibleDescription(note);
      await expect(panel.getByRole("img", { name: STOPPED.issue, exact: true })).toBeVisible();
    } else {
      await expect(tailscale).not.toHaveAccessibleDescription(/./);
      await expect(panel.getByRole("img", { name: /^The board moved/ })).toHaveCount(0);
    }
    if (isOn === "true" && !note) await expect(panel.getByRole("img", { name: "Tailscale works", exact: true })).toBeVisible();
    await panel.screenshot({ path: testInfo.outputPath("remote-access-after.png") });
  });
}

test("coming back to the board while a switch turns never undoes the turn", async ({ page }) => {
  // Arrange
  const { panel } = await open(page, OFF, { slow: true });
  const tailscale = panel.getByRole("switch", { name: "Tailscale", exact: true });

  // Act: he presses, and the window takes the focus while the supervisor
  // still works on it.
  await tailscale.click();
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));

  // Assert
  await expect(tailscale).toHaveAttribute("aria-checked", "true");
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(tailscale).toHaveAttribute("aria-checked", "true");
});

test("a switch that cannot turn on stays off and says why for a moment", async ({ page }) => {
  // Arrange
  const { panel } = await open(page, OFF, { fail: true });
  const tailscale = panel.getByRole("switch", { name: "Tailscale", exact: true });

  // Act
  await tailscale.click();

  // Assert
  await expect(panel.locator(".click-feedback")).toHaveText("Tailscale is not running on this PC.");
  await expect(tailscale).toHaveAttribute("aria-checked", "false");
  await expect(tailscale).toBeVisible();
  await expect(panel.getByRole("button", { name: "Copy " + URL })).toHaveCount(0);
});

// The board added to a Home Screen, on a browser that pushes, played by the
// test: its notification prompt answers with answer, it may already be
// subscribed, with the key heldKey, and what the badge asks of it is kept in
// window.badgeCalls.
async function installedApp(page: Page, { answer = "granted", isSubscribed = false, heldKey = SERVER_KEY, isRevoked = false }: { answer?: NotificationPermission; isSubscribed?: boolean; heldKey?: string; isRevoked?: boolean } = {}) {
  await page.addInitScript(({ answer, isSubscribed, app, heldKey, isRevoked }) => {
    const calls: string[] = [];
    const key = Uint8Array.from(atob(heldKey.replace(/-/g, "+").replace(/_/g, "/")), (char) => char.charCodeAt(0));
    const made = { endpoint: app.endpoint, options: { applicationServerKey: key.buffer }, toJSON: () => ({ ...app, expirationTime: null }), unsubscribe: async () => { calls.push("unsubscribe"); subscription = null; return true; } };
    let subscription: typeof made | null = isSubscribed ? made : null;
    let permission: NotificationPermission = isRevoked ? "denied" : isSubscribed ? "granted" : "default";
    const registration = { pushManager: {
      getSubscription: async () => subscription,
      subscribe: async (options: { applicationServerKey: Uint8Array }) => { calls.push("subscribe with a " + options.applicationServerKey.byteLength + "-byte key"); subscription = made; return made; },
    } };
    Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: { register: async (url: string) => { calls.push("register " + url); return registration; }, ready: Promise.resolve(registration), getRegistration: async () => registration } });
    Object.defineProperty(window, "Notification", { configurable: true, value: { get permission() { return permission; }, requestPermission: async () => { calls.push("ask"); permission = answer; return answer; } } });
    const media = window.matchMedia.bind(window);
    window.matchMedia = (query: string) => query === "(display-mode: standalone)" ? { ...media(query), matches: true } : media(query);
    Object.assign(window, { badgeCalls: calls });
  }, { answer, isSubscribed, app: APP, heldKey, isRevoked });
}

const badgeCalls = (page: Page) => page.evaluate(() => (window as unknown as { badgeCalls: string[] }).badgeCalls);

// CFO decision 1008918: the app's badge counts each new item while the app is
// closed, asking for notifications in the app's own words only when he turns
// it on.
test("in the Home Screen app, the App badge asks for notifications only when he turns it on, and subscribes the app until he turns it off", async ({ page }) => {
  // Arrange
  await installedApp(page);
  const { panel, pushes } = await open(page, ON, { origin: URL });
  const badge = page.getByRole("switch", { name: "App badge", exact: true });
  await expect(badge).toHaveAttribute("aria-checked", "false");
  expect(await badgeCalls(page)).toEqual([]);

  // Act: on.
  await badge.click();

  // Assert
  await expect(badge).toHaveAttribute("aria-checked", "true");
  expect(await badgeCalls(page)).toEqual(["ask", "register /push-worker.js", "subscribe with a 65-byte key"]);
  expect(pushes).toEqual([{ subscribe: APP }]);

  // Act and assert: off.
  await badge.click();
  await expect(badge).toHaveAttribute("aria-checked", "false");
  expect(pushes).toEqual([{ subscribe: APP }, { unsubscribe: APP.endpoint }]);
  expect(await badgeCalls(page)).toContain("unsubscribe");
  await expect(panel.getByRole("switch", { name: "Tailscale", exact: true })).toBeVisible();
});

test("an app whose notifications he refuses keeps the badge off and says where to allow them", async ({ page }) => {
  // Arrange
  await installedApp(page, { answer: "denied" });
  const { pushes } = await open(page, ON, { origin: URL });
  const badge = page.getByRole("switch", { name: "App badge", exact: true });

  // Act
  await badge.click();

  // Assert
  await expect(page.locator(".app-badge-setting .click-feedback")).toHaveText("Allow notifications for Code Goblins in Settings.");
  await expect(badge).toHaveAttribute("aria-checked", "false");
  expect(pushes).toEqual([]);
  expect(await badgeCalls(page)).toEqual(["ask"]);
});

// An app still subscribed tells the board again as it opens, which may have
// forgotten it; one subscribed with a key the board no longer signs with
// subscribes again with the board's, and one whose notifications he turned
// off in Settings ends its badge.
for (const [name, options, told, calls, isOn] of [
  ["with the board's key", { heldKey: SERVER_KEY }, [{ subscribe: APP }], [], "true"],
  ["with an earlier key", { heldKey: APP.keys.p256dh }, [{ unsubscribe: APP.endpoint }, { subscribe: APP }], ["unsubscribe", "subscribe with a 65-byte key"], "true"],
  ["whose notifications were turned off", { isRevoked: true }, [{ unsubscribe: APP.endpoint }], ["unsubscribe"], "false"],
] as const) {
  test(`an app still subscribed ${name} tells the board as it opens`, async ({ page }) => {
    // Arrange and act
    await installedApp(page, { isSubscribed: true, ...options });
    const { pushes } = await open(page, ON, { origin: URL });

    // Assert
    await expect.poll(() => pushes).toEqual(told);
    expect(await badgeCalls(page)).toEqual(calls);
    await expect(page.getByRole("switch", { name: "App badge", exact: true })).toHaveAttribute("aria-checked", isOn);
  });
}

// His phone's app never asks for Windows notifications: the App badge alone
// asks, when he turns it on. The board in a phone's browser tab still does.
for (const [where, isInstalled, asks] of [["the Home Screen app", true, 0], ["a phone's browser tab", false, 1]] as const) {
  test(`${where} on a new item ${asks ? "asks" : "does not ask"} for Windows notifications`, async ({ page }) => {
    // Arrange: notifications not yet asked for, as on a new phone.
    if (isInstalled) await installedApp(page);
    else await page.addInitScript(() => Object.defineProperty(window, "Notification", { configurable: true, value: { permission: "default", requestPermission: async () => "default" } }));
    const { push } = await open(page, ON, { origin: URL });

    // Act
    await push({ revision: 2, questions: [{ id: "cfo-question-1", identity: "c".repeat(64), text: "Ship it?", status: "pending", created_at: new Date().toISOString() }] });

    // Assert
    await expect(page.getByRole("group", { name: "Windows notifications" })).toHaveCount(asks);
  });
}

test("an app added to the PC's own board has no App badge", async ({ page }) => {
  // Arrange and act
  await installedApp(page);
  const { panel } = await open(page, ON);

  // Assert
  await expect(panel.getByRole("switch", { name: "Tailscale", exact: true })).toBeVisible();
  await expect(page.getByRole("switch", { name: "App badge" })).toHaveCount(0);
});

test("the board in a browser tab, not added to a Home Screen, has no App badge", async ({ page }) => {
  // Arrange and act
  const { panel } = await open(page, ON, { origin: URL });

  // Assert
  await expect(panel.getByRole("switch", { name: "Tailscale", exact: true })).toBeVisible();
  await expect(page.getByRole("switch", { name: "App badge" })).toHaveCount(0);
});
