import { expect, type Page } from "./site";

// A board with two Claude Code goblins, Zeke and Ivy, each in a native
// terminal, and a CFO, for the specs of a session's messages and its box. The
// supervisor is played here: the board's snapshot and each session's messages
// on held streams, the speech model, and the terminal's relay, which records
// what reached the terminal, counts the sizes the board asks for and tells
// the view what a relay tells it. Every conversation is made up.
export const at = "2026-10-22T09:00:00Z";
export const GOBLIN = {
  id: "cg-touch", title: "Swipes scroll the phone terminal; Claude Code", goblin_name: "Zeke", goblin_title: "Touch Wrangler", project: "code-goblins", phase: "working", verified: false,
  generation: "cg-touch-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", since: at,
};
export const OTHER = { ...GOBLIN, id: "cg-other", title: "Another goblin; Claude Code", goblin_name: "Ivy", goblin_title: "Window Painter", generation: "cg-other-1" };
export const snapshot = (fields: Record<string, unknown> = {}, goblin: Record<string, unknown> = {}) => ({
  healthy: true, instance: "fixture", revision: 1, attention: [], cfo_runs: true, cfo_terminal: "cfo-term", cfo_harness: "claude", afk: { state: "off" }, tasks: [{ ...GOBLIN, ...goblin }, OTHER], ...fields,
});
export const PAGE = { messages: [{ id: "m0", seq: 0, at, kind: "prompt", source: "overlord", text: "Read the brief" }, { id: "m1", seq: 1, at, kind: "reply", blocks: [{ kind: "text", text: "On it." }] }], more: false };
export const NAME = "Zeke - Touch Wrangler";
// A paste in a program that turned bracketed paste on.
export const pasted = (text: string) => "\x1b[200~" + text + "\x1b[201~";

export interface Relay {
  // Every view's typing in order, its resizes, and how many views connected.
  typed: string[];
  resizes: { cols: number; rows: number }[];
  views: number;
  submissions: string[];
  addresses: string[];
  inputs: { address: string; text: string }[];
  write: (text: string) => void;
  end: () => void;
  // say tells the goblin's newest view a relay message, or another's while
  // its terminal has not connected; drop closes that view as a relay that
  // went away does, which it reconnects from.
  say: (message: object) => void;
  drop: () => void;
  // refuse closes each new view of the goblin's terminal at once, as a relay
  // that has not come back does, so the board stays in its reconnect until
  // accept lets the next view connect whole.
  refuse: () => void;
  accept: () => void;
}

export async function open(page: Page, first: object = snapshot(), heard = "rebase onto main", messages: object = PAGE, output = "> ready\r\n"): Promise<Relay> {
  const relay: Relay = { typed: [], resizes: [], views: 0, submissions: [], addresses: [], inputs: [], write: () => {}, end: () => {}, say: () => {}, drop: () => {}, refuse: () => {}, accept: () => {} };
  let isRefused = false;
  let goblinSpoke = false;
  await page.addInitScript(() => {
    if (!localStorage.getItem("cfo-first-open")) localStorage.setItem("cfo-first-open", "shown");
    localStorage.setItem("cfo-voice-hint-v1", "dismissed");
  });
  await page.addInitScript(({ data, messages }) => {
    // The board's snapshot stream, which a test sends a later snapshot down,
    // and each session's message stream.
    class Stream extends EventTarget {
      static CONNECTING = 0; static OPEN = 1; static CLOSED = 2;
      onerror: (() => void) | null = null;
      readyState = 1;
      private publish = (event: Event) => { if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) })); };
      private update = (event: Event) => { if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent(event.detail.type, { data: JSON.stringify(event.detail.data) })); };
      constructor(address: string | URL) {
        super();
        const url = new URL(address, location.href);
        if (url.pathname === "/api/messages") {
          window.addEventListener("fixture-messages", this.update);
          // A test that holds the session's messages back (holdMessages)
          // lets them through with a fixture-release-messages event.
          const first = () => this.dispatchEvent(new MessageEvent("page", { data: JSON.stringify(messages) }));
          if ((window as { fixtureHoldsMessages?: boolean }).fixtureHoldsMessages) window.addEventListener("fixture-release-messages", first, { once: true });
          else setTimeout(first);
          return;
        }
        window.addEventListener("fixture-snapshot", this.publish);
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(data) })));
      }
      close() { this.readyState = 2; window.removeEventListener("fixture-snapshot", this.publish); window.removeEventListener("fixture-messages", this.update); }
    }
    Object.defineProperty(window, "EventSource", { value: Stream });
  }, { data: first, messages });
  await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
  await page.route("**/api/dictation/warm", (route) => route.fulfill({ status: 202 }));
  await page.route("**/api/dictation", (route) => route.request().method() === "GET" ? route.fulfill({ json: { engine: "test-model", state: "ready" } }) : route.fulfill({ json: { text: heard, engine: "test-model" } }));
  const holds = new Map<string, object>();
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    relay.views++;
    relay.addresses.push(socket.url());
    const isGoblin = new URL(socket.url()).searchParams.get("task") === GOBLIN.id;
    let submission = ""
    const spoke = {
      say: (message: object) => {
        if ("type" in message && message.type === "held") holds.set(socket.url(), message);
        socket.send(JSON.stringify("type" in message && message.type === "screen" ? { submission, ...message } : message));
      },
      drop: () => socket.close({ code: 1001 }),
      end: () => socket.close({ code: 1000, reason: "No terminal is running for this task." }),
      write: (text: string) => socket.send(Buffer.from(text)),
    };
    // The relay speaks for the goblin's terminal, whichever view of it
    // connected last, and for another's only while its own has not connected.
    if (isGoblin || !goblinSpoke) Object.assign(relay, spoke);
    if (isGoblin) goblinSpoke = true;
    const whole = () => {
      socket.send(JSON.stringify({ type: "history", bytes: 0 }));
      if (holds.has(socket.url())) socket.send(JSON.stringify(holds.get(socket.url())));
    };
    relay.refuse = () => { isRefused = true; };
    relay.accept = () => { isRefused = false; };
    if (isGoblin && isRefused) { socket.close({ code: 1001 }); return; }
    whole();
    socket.onMessage((data) => {
      if (typeof data !== "string") { const text = data.toString("utf8"); relay.typed.push(text); relay.inputs.push({ address: socket.url(), text }); return; }
      const sent: unknown = JSON.parse(data);
      if (typeof sent === "object" && sent !== null && "type" in sent && sent.type === "submit" && "submission" in sent && typeof sent.submission === "string") {
        submission = sent.submission
        relay.submissions.push(submission)
        return
      }
      if (typeof sent !== "object" || sent === null || !("type" in sent) || sent.type !== "resize" || !("cols" in sent) || !("rows" in sent)) return;
      const size = { cols: Number(sent.cols), rows: Number(sent.rows) };
      relay.resizes.push(size);
      socket.send(JSON.stringify({ type: "size", ...size }));
      // A program that turned bracketed paste on, as Claude Code does.
      socket.send(Buffer.from("\x1b[?2004h" + output));
    });
  });
  await page.goto("/");
  await expect(page.locator(".board-column").first()).toBeVisible();
  return relay;
}

export const card = (page: Page, name: string) => page.locator(".task-board .task-card-shell").filter({ hasText: name }).locator(".task-card");
export const box = (page: Page, name = NAME) => page.getByRole("region", { name: "Messages to " + name });
export const field = (page: Page, name = NAME) => box(page, name).getByRole("textbox", { name: "Message " + name });
export const send = (page: Page, name = NAME) => box(page, name).getByRole("button", { name: "Send", exact: true });
export const typed = (relay: Relay) => relay.typed.join("");
// boxMenu opens the box's style menu, which holds Speech history and Open in
// Windows Terminal beside the chat style, and answers the box.
export async function boxMenu(page: Page, name = NAME) {
  const menu = box(page, name).getByRole("button", { name: /^Style: / });
  if (await menu.getAttribute("aria-expanded") !== "true") await menu.click();
  return box(page, name);
}

// openGoblin opens Zeke's panel from his card on his messages, with his
// terminal whole behind them.
export async function openGoblin(page: Page) {
  await card(page, NAME).click();
  await expect(page.locator("#panel-title")).toHaveText(NAME);
  await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
  await expect(page.locator(".terminal-view:not(.staged)")).toHaveCount(1);
}

// holdMessages keeps every session's first page of messages back, so its view
// stays connecting until releaseMessages lets it through.
export async function holdMessages(page: Page) {
  await page.addInitScript(() => { (window as { fixtureHoldsMessages?: boolean }).fixtureHoldsMessages = true; });
}

export async function releaseMessages(page: Page) {
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("fixture-release-messages")));
}
