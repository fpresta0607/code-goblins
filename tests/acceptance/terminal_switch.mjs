// Switching between goblin terminals on the board, timed from the click to
// the next painted frame, with every frame in between inspected. It drives a
// headless Edge over the DevTools protocol against a running board, opens each
// named task's terminal from its card, then switches between them and prints
// one JSON line: the switch percentiles and how many frames showed a blank or
// half-drawn terminal. It starts only its own browser and ends it by its pid.
//
//   node tests/acceptance/terminal_switch.mjs --url http://127.0.0.1:PORT --titles "native-a,native-b" --rounds 40 --label after
//
// The browser runs with WebGL off, so xterm draws its rows into the DOM where
// each frame's text can be read: a frame counts as half drawn when the
// terminal is in sight but its text is not yet the text it settles on, and as
// blank when it is covered or empty. Each terminal needs text of its own on
// screen, such as a line echoed into it, so the two can be told apart.
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

const options = {};
for (let i = 2; i < process.argv.length; i += 2) options[process.argv[i].replace(/^--/, "")] = process.argv[i + 1];
if (!options.url || !options.titles) throw new Error("--url and --titles are required");
const browser = options.browser || "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe";
const profile = mkdtempSync(join(tmpdir(), "cfo-switch-"));
const edge = spawn(browser, ["--headless=new", "--disable-gpu", "--disable-webgl", "--no-first-run", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=1600,1000", "about:blank"], { stdio: "ignore" });

// measure runs in the board's page.
async function measure(titles, rounds) {
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const frame = () => new Promise((resolve) => requestAnimationFrame(() => resolve(performance.now())));
  const until = async (found, ms, what) => {
    for (const end = performance.now() + ms; performance.now() < end; await pause(20)) {
      const value = found();
      if (value) return value;
    }
    throw new Error("timed out waiting for " + what);
  };
  // What the panel shows now: the terminal in sight and its text, or why none.
  const screen = () => {
    const covered = [...document.querySelectorAll(".terminal-cover")].some((cover) => cover.offsetParent !== null);
    const views = [...document.querySelectorAll(".xterm-rows")].filter((rows) => rows.offsetParent !== null && getComputedStyle(rows.closest(".terminal-view") || rows).visibility !== "hidden");
    const connecting = [...document.querySelectorAll(".terminal-state")].some((state) => state.offsetParent !== null && /Connecting/.test(state.textContent || ""));
    const text = views.length === 1 ? views[0].textContent.replace(/\s+/g, " ").trim() : "";
    return { covered, connecting, count: views.length, text };
  };
  const drawn = (state, marker) => !state.covered && !state.connecting && state.count === 1 && state.text.includes(marker);
  // Watch every frame from the click until the terminal has been still for
  // half a second; the drawn time is the first frame showing its text.
  const watch = async (marker, act) => {
    const frames = [];
    const start = performance.now();
    act();
    let drawnAt = 0;
    for (let still = 0; still < 30;) {
      const at = await frame();
      const state = screen();
      frames.push(state);
      if (!drawnAt && drawn(state, marker)) drawnAt = at - start;
      const prior = frames[frames.length - 2];
      still = prior && prior.text === state.text && drawnAt ? still + 1 : 0;
      if (at - start > 15000) break;
    }
    const final = frames[frames.length - 1].text;
    const first = frames.findIndex((state) => drawn(state, marker));
    const shown = frames.slice(Math.max(0, first));
    return {
      ms: drawnAt,
      // Blank: after the click, a frame with no terminal text in sight.
      blank: frames.filter((state) => state.covered || state.count === 0 || !state.text).length,
      // Half drawn: the terminal in sight but not yet showing what it settles on.
      half: first < 0 ? frames.length : shown.filter((state) => state.text !== final).length,
    };
  };
  const button = (title) => document.querySelector(`[aria-label="Open the terminal of ${CSS.escape(title)}"]`);
  const markers = {};
  const attach = [];
  for (const title of titles) {
    await until(() => button(title), 30000, "the terminal button of " + title);
    markers[title] = "marker-" + title;
    attach.push(await watch("", () => button(title).click()));
    const textarea = await until(() => [...document.querySelectorAll(".xterm-helper-textarea")].find((area) => area.offsetParent !== null || area.closest(".terminal-view:not(.staged)")?.offsetParent), 30000, "the terminal input");
    textarea.dispatchEvent(new InputEvent("input", { data: "echo " + markers[title] + "\r", inputType: "insertText", bubbles: true }));
    // The typed command and its output each show the marker once.
    await until(() => screen().text.split(markers[title]).length > 2, 15000, "the marker in " + title);
    await pause(400);
  }
  const switches = [];
  for (let i = 0; i < rounds; i++) {
    const title = titles[(i + 1) % titles.length];
    switches.push(await watch(markers[title], () => button(title).click()));
    await pause(150);
  }
  const times = switches.map((each) => each.ms).sort((a, b) => a - b);
  const at = (fraction) => {
    const k = (times.length - 1) * fraction, low = Math.floor(k), high = Math.min(low + 1, times.length - 1);
    return +(times[low] + (times[high] - times[low]) * (k - low)).toFixed(1);
  };
  return {
    rounds,
    switch_p50_ms: at(0.5), switch_p95_ms: at(0.95), switch_max_ms: +times[times.length - 1].toFixed(1),
    switch_blank_frames: switches.reduce((n, each) => n + each.blank, 0),
    switch_half_drawn_frames: switches.reduce((n, each) => n + each.half, 0),
    attach_ms: attach.map((each) => +each.ms.toFixed(0)),
    attach_half_drawn_frames: attach.reduce((n, each) => n + each.half, 0),
  };
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
  const titles = options.titles.split(",").map((title) => title.trim());
  const result = await call("Runtime.evaluate", { expression: `(${measure})(${JSON.stringify(titles)}, ${Number(options.rounds || 40)})`, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
  console.log(JSON.stringify({ label: options.label || "run", titles, when: new Date().toISOString(), ...result.result.value }));
  socket.close();
} finally {
  spawnSync("taskkill", ["/T", "/F", "/PID", String(edge.pid)], { stdio: "ignore" });
  await sleep(300);
  try { rmSync(profile, { recursive: true, force: true }); } catch { /* Edge may still hold a file for a moment */ }
}
