import { devices, expect, holdStream, servePages, test } from "./site";

// The Overlord, 2026-10-18 about 12:40Z: "terminal windows in mobile via vpn
// are completely blank black screens". On his iPhone the board is WebKit,
// which every browser there is, at the private board's https address. Safari
// has no idle callback, and dictation's call to it threw from every view with
// a microphone and took the whole board down; and in Playwright's WebKit
// xterm's WebGL renderer drew nothing. So this file runs in WebKit as an
// iPhone, under the address the private board has, which also keeps WebKit
// off the suite's own address on port 1, a port it refuses. It takes the idle
// callback away itself, so it keeps the first cause covered whatever WebKit
// it runs in, and counts the WebGL contexts the page asks for.
const PRIVATE = "https://pc.example.ts.net:4311";
const SESSION = "claude/9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34";
const snapshot = {
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, afk: { state: "off" },
  tasks: [{ id: "nw-invoice-export", title: "Export invoices as CSV", project: "northwind-api", phase: "working", verified: false, generation: "nw-invoice-export-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", session: SESSION }],
  sessions: [{ id: SESSION, native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "nw-invoice-export", role: "goblin", task_id: "nw-invoice-export", generation: "nw-invoice-export-1", harness: "claude", phase: "working" }],
};
const SCREEN = "\x1b[2J\x1b[H\x1b[32mExporting invoices\x1b[0m\r\n$ ";

interface Probe { webglAsked: number; audioMade: number }

test.use({ ...devices["iPhone 15"], browserName: "webkit" });

test("on an iPhone over the private board a goblin's terminal draws and takes typing", async ({ page }) => {
  // Arrange: Safari's gaps, and a stand-in Web Audio whose recorder never
  // starts, so dictation's preparation runs and fails quietly as it would
  // anywhere. The terminal's host, played by its socket, repaints after each
  // size the view asks for, as the real one does.
  await page.addInitScript(() => {
    const probe: Probe = { webglAsked: 0, audioMade: 0 };
    Object.assign(window, { probe });
    for (const target of [window, Window.prototype]) for (const name of ["requestIdleCallback", "cancelIdleCallback"]) Reflect.deleteProperty(target, name);
    const getContext = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, kind: string, ...rest: unknown[]) {
      if (kind.startsWith("webgl")) probe.webglAsked++;
      return (getContext as (...args: unknown[]) => unknown).call(this, kind, ...rest);
    } as typeof getContext;
    class StandInAudio {
      audioWorklet = { addModule: () => Promise.reject(new Error("no recorder in this test")) };
      constructor() { probe.audioMade++; }
      close() { return Promise.resolve(); }
    }
    Object.assign(window, { AudioContext: StandInAudio });
  });
  const errors: string[] = [];
  page.on("pageerror", (error) => { if (!error.message.startsWith("ResizeObserver loop")) errors.push(error.message); });
  const sockets: string[] = [];
  const typed: string[] = [];
  await holdStream(page, snapshot);
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    sockets.push(new URL(socket.url()).protocol);
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from(SCREEN));
    socket.onMessage((data) => {
      if (typeof data !== "string") { typed.push(Buffer.from(data).toString()); return; }
      const control: { type?: string; cols?: number; rows?: number } = JSON.parse(data);
      if (control.type !== "resize") return;
      socket.send(JSON.stringify({ type: "size", cols: control.cols, rows: control.rows }));
      socket.send(Buffer.from(SCREEN));
    });
  });
  await servePages(page.context(), PRIVATE);
  // He chose the Terminal view, which a session then opens on, and which
  // this spec is about.
  await page.addInitScript(() => localStorage.setItem("cfo-panel-view", "terminal"));
  await page.goto(PRIVATE + "/");
  const board = page.getByRole("button", { name: "Board", exact: true });
  if (await board.isVisible()) await board.click();
  const card = page.locator(".task-board .task-card-shell").filter({ hasText: "Export invoices as CSV" }).locator(".task-card");
  const probe = () => page.evaluate(() => (window as unknown as { probe: Probe }).probe);

  // Act
  await card.focus();
  await page.keyboard.press("Enter");
  const terminal = page.getByRole("region", { name: "Goblin terminal" });
  await expect(terminal.locator(".xterm-rows")).toContainText("Exporting invoices");
  await terminal.locator(".xterm").click();
  await page.keyboard.type("ls");

  // Assert: drawn by the DOM renderer, typed into over wss, dictation made
  // ready without the idle callback, and nothing thrown.
  await expect.poll(() => typed.join("")).toBe("ls");
  expect(sockets).toContain("wss:");
  await expect.poll(async () => (await probe()).audioMade).toBeGreaterThan(0);
  expect((await probe()).webglAsked).toBe(0);
  expect(errors).toEqual([]);
});
