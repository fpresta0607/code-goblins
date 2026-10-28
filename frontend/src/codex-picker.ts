import { codexChoice } from "./engine-choices.ts";
import { chooseKeys, PANEL_KEYS } from "./panel-keys.ts";
import { watchLink, type TerminalLink } from "./terminal-link.ts";

// The words for a choice Codex's picker never offered, and for keys its
// terminal did not take.
export const CODEX_NOT_OFFERED = "Codex did not offer that choice in its model picker.";
export const CODEX_NOT_SENT = "not sent";

// driveCodexPicker answers Codex's own /model picker for the box, which has
// already typed /model into the terminal of key: Codex 0.160.0's /model takes
// no value, so it picks the model in its model list and then the effort in
// its reasoning list by their digits, as he would, while the terminal stays
// out of sight, and ends once Codex closes its picker, so its list is never
// drawn. A model with no reasoning list ends after the model, and with an
// empty effort Enter takes the level Codex's reasoning list has focused, once
// its model list is gone. done hears "" once it is set, CODEX_NOT_SENT when
// the terminal did not take a pick, or CODEX_NOT_OFFERED after wait ms
// without the choice, when the picker, if open, is closed with Escape. It
// returns what stops it.
export function driveCodexPicker(key: string, target: { model: string; name: string; effort: string }, wait: number, done: (failed: string) => void): () => void {
  let step: "model" | "effort" | "closing" = "model";
  let latest: TerminalLink | undefined;
  let isOver = false;
  const finish = (failed: string) => {
    if (isOver) return;
    isOver = true;
    stopWatching();
    clearTimeout(timer);
    done(failed);
  };
  const look = (link: TerminalLink | undefined) => {
    latest = link;
    if (isOver || !link?.screen) return;
    if (step !== "model" && !link.screen.isOpen) { finish(""); return; }
    const panel = link.screen.panel;
    if (!panel || step === "closing") return;
    let keys: string | null = PANEL_KEYS.enter;
    if (step === "effort" && !target.effort) {
      if (codexChoice(panel, "model", target.model, target.name) >= 0) return;
    } else {
      const at = codexChoice(panel, step, step === "model" ? target.model : target.effort, step === "model" ? target.name : "");
      if (at < 0) return;
      keys = chooseKeys(panel, at);
    }
    if (!keys || !link.control(keys)) { finish(CODEX_NOT_SENT); return; }
    step = step === "model" ? "effort" : "closing";
  };
  const timer = setTimeout(() => {
    if (latest?.screen?.isOpen) latest.escape();
    finish(CODEX_NOT_OFFERED);
  }, wait);
  const stopWatching = watchLink(key, look);
  return () => { isOver = true; stopWatching(); clearTimeout(timer); };
}
