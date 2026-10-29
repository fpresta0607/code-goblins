import { PANEL_KEYS } from "./panel-keys.ts";
import { watchLink, type TerminalLink } from "./terminal-link.ts";

// The words for keys the terminal did not take.
export const MODE_NOT_SENT = "not sent";

// cycleMode sets the permission mode of the session in the terminal of key,
// whose link is link, as he would in its terminal: Claude Code takes no mode
// by name, so it presses Shift+Tab, once per mode its footer names, until the
// footer names target. A mode the session never reaches leaves it in the mode
// it started in, once the cycle comes back to it. done hears "" once the
// footer names target, MODE_NOT_SENT when the terminal did not take a press,
// or why not after wait ms or a whole cycle. It returns what stops it.
export function cycleMode(key: string, link: TerminalLink, target: string, name: string, wait: number, done: (failed: string) => void): () => void {
  const started = link.screen?.mode ?? "";
  // The mode the footer named at the last press, which it names until the
  // press lands.
  let pressedAt = "";
  let isOver = false;
  const finish = (failed: string) => {
    if (isOver) return;
    isOver = true;
    stopWatching();
    clearTimeout(timer);
    done(failed);
  };
  const look = (current: TerminalLink | undefined) => {
    const mode = current?.screen?.mode;
    if (isOver || !current || !mode || mode === pressedAt) return;
    if (mode === target) { finish(""); return; }
    if (pressedAt && mode === started) { finish("The session does not offer " + name + "."); return; }
    if (!current.control(PANEL_KEYS.backTab)) { finish(MODE_NOT_SENT); return; }
    pressedAt = mode;
  };
  const timer = setTimeout(() => finish("The session did not change its mode to " + name + "."), wait);
  const stopWatching = watchLink(key, look);
  look(link);
  return () => { isOver = true; stopWatching(); clearTimeout(timer); };
}
