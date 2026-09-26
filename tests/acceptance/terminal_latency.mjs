// Key-to-echo latency of the board's terminal panel, timed where the Overlord
// types: a key enters the panel's own xterm, and the clock stops when its echo
// is painted. It drives a headless Edge over the DevTools protocol against a
// running board, opens the named task's terminal from its card, types into it
// and prints one JSON line: the time to a live screen and the echo
// percentiles. It starts only its own browser and ends it by its pid.
//
//   node tests/acceptance/terminal_latency.mjs --url http://127.0.0.1:PORT --title "Build review panel" --keys 100 --label before
//
// The terminal must hold a program that echoes typed text on its input line,
// such as the native-board fixture harness or cmd.exe; Escape clears the line
// every 40 keys so it never wraps.
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i += 2) options[process.argv[i].replace(/^--/, "")] = process.argv[i + 1];
if (!options.url || !options.title) throw new Error("--url and --title are required");
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const profile = mkdtempSync(join(tmpdir(), "cfo-latency-"));
const edge = spawn(browser, ["--headless=new", "--disable-gpu", "--disable-webgl", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=1600,1000", "about:blank"], { stdio: "ignore" });

// measure runs in the board's page.
async function measure(title, keys) {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const until = async (found, ms, what) => {
    for (const end = performance.now() + ms; performance.now() < end; await pause(20)) {
      const value = found();
      if (value) return value;
    }
    throw new Error("timed out waiting for " + what);
  };
  const button = await until(() => document.querySelector(`[aria-label="Open the terminal of ${CSS.escape(title)}"]`), 30000, "the task's terminal button");
  const opened = performance.now();
  button.click();
  // The terminal in sight: a native one once its screen is whole, a Herdr view
  // once it is live.
  const root = await until(() => {
    const slot = document.querySelector(".deck-slot:not([hidden])");
    if (!slot || slot.querySelector(".terminal-cover")) return null;
    const view = slot.querySelector(".terminal-view:not(.staged)") || slot;
    const live = slot.querySelector(".host-terminal") || slot.querySelector(".terminal-state.live");
    return live && view.querySelector(".xterm-rows") ? view : null;
  }, 30000, "a live terminal");
  const attach = performance.now() - opened;
  const textarea = root.querySelector(".xterm-helper-textarea");
  const rows = root.querySelector(".xterm-rows");
  const count = () => (rows.textContent.match(/x/g) || []).length;
  // The same path a typed key takes after xterm's key handling: onData.
  const type = (data) => textarea.dispatchEvent(new InputEvent("input", { data, inputType: "insertText", bubbles: true }));
  const settle = async () => {
    let last = rows.textContent;
    for (let quiet = performance.now(); performance.now() - quiet < 250; await pause(25)) {
      if (rows.textContent !== last) { last = rows.textContent; quiet = performance.now(); }
    }
  };
  const key = () => new Promise((resolve) => {
    const before = count();
    let done = false;
    const start = performance.now();
    const observer = new MutationObserver(() => {
      if (done || count() <= before) return;
      done = true;
      observer.disconnect();
      requestAnimationFrame(() => resolve(performance.now() - start));
    });
    observer.observe(rows, { childList: true, subtree: true, characterData: true });
    type("x");
    setTimeout(() => { if (!done) { done = true; observer.disconnect(); resolve(null); } }, 5000);
  });
  await settle();
  // The first keys warm the program's input path; time the steady state.
  for (let i = 0; i < 3; i++) { await key(); await settle(); }
  type("\x1b");
  await settle();
  const painted = [];
  let missed = 0;
  for (let i = 0; i < keys; i++) {
    const elapsed = await key();
    if (elapsed === null) missed++; else painted.push(elapsed);
    await settle();
    if ((i + 1) % 40 === 0) { type("\x1b"); await settle(); }
  }
  type("\x1b");
  const sorted = [...painted].sort((a, b) => a - b);
  const at = (fraction) => {
    const k = (sorted.length - 1) * fraction, low = Math.floor(k), high = Math.min(low + 1, sorted.length - 1);
    return +(sorted[low] + (sorted[high] - sorted[low]) * (k - low)).toFixed(1);
  };
  return { keys, missed, attach_ms: +attach.toFixed(0), p50_ms: at(0.5), p95_ms: at(0.95), max_ms: +sorted[sorted.length - 1].toFixed(1) };
}

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
  await call("Page.navigate", { url: options.url });
  const result = await call("Runtime.evaluate", { expression: `(${measure})(${JSON.stringify(options.title)}, ${Number(options.keys || 100)})`, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
  console.log(JSON.stringify({ label: options.label || "run", title: options.title, when: new Date().toISOString(), ...result.result.value }));
  socket.close();
} finally {
  spawnSync("taskkill", ["/T", "/F", "/PID", String(edge.pid)], { stdio: "ignore" });
  await sleep(300);
  try { rmSync(profile, { recursive: true, force: true }); } catch { /* Edge may still hold a file for a moment */ }
}
