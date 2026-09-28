// The board terminal checklist, checked where the Overlord looks: a headless
// Edge over the DevTools protocol opens the named task's terminal from its
// card and measures the panel with real wheel and key input. It prints one
// JSON line: the padding on each side of the drawn grid at each text size,
// the scroll bars in sight, whether the wheel shows history and the view
// comes back to the bottom, and whether typing returns there. It starts only
// its own browser and ends it by its pid.
//
//   node tests/acceptance/terminal_checklist.mjs --url http://127.0.0.1:PORT --title "Terminal demo" --fill "1..200 | % { \"line $_\" }"
//
// The terminal must hold a shell, such as PowerShell: --fill is typed and
// entered once to give it history, and the checks type one character, which
// the shell's line editor echoes. WebGL is off so the rows are in the DOM.
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i++) options[process.argv[i].replace(/^--/, "")] = process.argv[++i];
if (!options.url || !options.title || !options.fill) throw new Error("--url, --title and --fill are required");
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const profile = mkdtempSync(join(tmpdir(), "cfo-checklist-"));
const edge = spawn(browser, ["--headless=new", "--disable-gpu", "--disable-webgl", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=1600,1000", "about:blank"], { stdio: "ignore" });

// These run in the board's page.
const open = async (title) => {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  for (const end = performance.now() + 30000; performance.now() < end; await pause(50)) {
    [...document.querySelectorAll("button")].find((button) => button.textContent.includes("Open the board without a CFO"))?.click();
    const button = document.querySelector(`[aria-label="Open the terminal of ${CSS.escape(title)}"]`);
    if (button) { button.click(); break; }
  }
  for (const end = performance.now() + 30000; performance.now() < end; await pause(50)) {
    const view = document.querySelector(".deck-slot:not([hidden]) .terminal-view:not(.staged)");
    if (view?.querySelector(".xterm-rows > div")) { view.querySelector(".xterm-helper-textarea").focus(); return true; }
  }
  throw new Error("timed out waiting for a live terminal");
};
const look = () => {
  const slot = document.querySelector(".deck-slot:not([hidden])");
  const view = slot.querySelector(".terminal-view:not(.staged)");
  const surface = slot.querySelector(".terminal-surface").getBoundingClientRect();
  const screen = view.querySelector(".xterm-screen").getBoundingClientRect();
  const rows = [...view.querySelectorAll(".xterm-rows > div")];
  const round = (n) => Math.round(n * 10) / 10;
  // A scroll bar in sight is one the page draws for an element that
  // overflows, or one of xterm's own that is displayed with a width.
  const bars = [...slot.querySelectorAll("*")].filter((element) => {
    const style = getComputedStyle(element);
    if (element.classList.contains("scrollbar")) return style.display !== "none" && style.visibility !== "hidden" && element.getBoundingClientRect().width > 0;
    return (element.offsetWidth - element.clientWidth > 1 && /auto|scroll/.test(style.overflowY)) || (element.offsetHeight - element.clientHeight > 1 && /auto|scroll/.test(style.overflowX));
  }).map((element) => element.className || element.tagName);
  const lines = rows.map((row) => row.textContent.replace(/\s+$/, ""));
  const cursor = rows.findIndex((row) => row.querySelector(".xterm-cursor"));
  return {
    font: parseFloat(getComputedStyle(view.querySelector(".xterm-rows")).fontSize),
    rows: rows.length,
    row: round(screen.height / rows.length),
    left: round(screen.left - surface.left), right: round(surface.right - screen.right),
    top: round(screen.top - surface.top), bottom: round(surface.bottom - screen.bottom),
    bars,
    first: lines.find(Boolean) || "",
    last: lines.filter(Boolean).at(-1) || "",
    cursorRow: cursor, rowsShown: rows.length,
    center: [screen.left + screen.width / 2, screen.top + screen.height / 2],
  };
};

try {
  let port = "";
  for (let i = 0; i < 200 && !port; i++) {
    await sleep(100);
    try { port = readFileSync(join(profile, "DevToolsActivePort"), "utf8").split("\n")[0].trim(); } catch { /* not written yet */ }
  }
  if (!port) throw new Error("Edge did not open its DevTools port");
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  const socket = new WebSocket(targets.find((target) => target.type === "page").webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let next = 0;
  const waiting = new Map();
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (waiting.has(message.id)) { waiting.get(message.id)(message); waiting.delete(message.id); }
  };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++next;
    waiting.set(id, (message) => message.error ? reject(new Error(message.error.message)) : resolve(message.result));
    socket.send(JSON.stringify({ id, method, params }));
  });
  const run = async (fn, ...args) => {
    const result = await call("Runtime.evaluate", { expression: `(${fn})(...${JSON.stringify(args)})`, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  };
  const key = async (key, code, keyCode, modifiers = 0, text = "") => {
    await call("Input.dispatchKeyEvent", { type: text ? "keyDown" : "rawKeyDown", key, code, windowsVirtualKeyCode: keyCode, modifiers, text });
    await call("Input.dispatchKeyEvent", { type: "keyUp", key, code, windowsVirtualKeyCode: keyCode, modifiers });
  };
  const wheel = async (deltaY) => {
    const [x, y] = (await run(look)).center;
    await call("Input.dispatchMouseEvent", { type: "mouseWheel", x, y, deltaX: 0, deltaY });
    await sleep(150);
  };
  await call("Page.navigate", { url: options.url });
  await sleep(1500);
  await run(open, options.title);
  // The fill is typed on a clear line and entered once; the screen is
  // settled once it holds a fresh prompt and has stopped changing.
  const quiet = async () => {
    let before = "";
    for (let i = 0; i < 50; i++) {
      await sleep(250);
      const now = await run(look);
      const text = now.first + "\n" + now.last;
      if (text === before && /[>$#]\s*$/.test(now.last)) return now;
      before = text;
    }
    return run(look);
  };
  await key("Escape", "Escape", 27);
  await sleep(300);
  await call("Input.insertText", { text: options.fill });
  await sleep(300);
  await key("Enter", "Enter", 13, 0, "\r");
  const settled = await quiet();
  // The wheel is turned a notch at a time until the view stops moving, up to
  // the top of the history and back down to the live screen.
  const turn = async (deltaY) => {
    let last = await run(look), notches = 0;
    for (; notches < 80; notches++) {
      await wheel(deltaY);
      const now = await run(look);
      if (now.first === last.first && now.last === last.last) break;
      last = now;
    }
    return { ...last, notches };
  };
  const scrolledUp = await turn(-120);
  const scrolledDown = await turn(120);
  await wheel(-360);
  await call("Input.insertText", { text: "x" });
  await sleep(600);
  const typedInHistory = await run(look);
  await key("Backspace", "Backspace", 8, 0, "\b");
  const sizes = [settled];
  for (const [k, code, keyCode] of [["=", "Equal", 187], ["=", "Equal", 187], ["-", "Minus", 189], ["-", "Minus", 189], ["-", "Minus", 189], ["=", "Equal", 187]]) {
    await key(k, code, keyCode, 2);
    await sleep(900);
    sizes.push(await run(look));
  }
  const even = (m) => Math.abs(m.left - m.right) <= 1 && Math.abs(m.bottom - m.left) <= 1 && m.top >= m.left - 1 && m.top < m.left + m.row;
  const pad = (m) => ({ font: m.font, rows: m.rows, left: m.left, right: m.right, top: m.top, bottom: m.bottom, row: m.row });
  const seen = (m) => [m.first, m.last];
  console.log(JSON.stringify({
    label: options.label || "run", title: options.title, when: new Date().toISOString(),
    padding: sizes.map(pad),
    even_padding: sizes.every(even),
    input_line_at_bottom: sizes.every((m) => m.bottom <= m.left + 1),
    scroll_bars: [...new Set(sizes.flatMap((m) => m.bars))],
    wheel_shows_history: scrolledUp.first !== settled.first,
    history_reaches: scrolledUp.first, history_notches: scrolledUp.notches,
    wheel_returns_to_bottom: scrolledDown.last === settled.last && scrolledDown.first === settled.first,
    typing_returns_to_bottom: typedInHistory.last.endsWith("x"),
    zoom: sizes.map((m) => m.font),
    zoom_steps: sizes[1].font > sizes[0].font && sizes[3].font < sizes[2].font,
    seen: { settled: seen(settled), wheel_up: seen(scrolledUp), wheel_down: seen(scrolledDown), typed: seen(typedInHistory) },
  }));
  socket.close();
} finally {
  spawnSync("taskkill", ["/T", "/F", "/PID", String(edge.pid)], { stdio: "ignore" });
  await sleep(300);
  try { rmSync(profile, { recursive: true, force: true }); } catch { /* Edge may still hold a file for a moment */ }
}
