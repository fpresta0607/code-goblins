import type { Terminal } from "@xterm/xterm";

const extendedKeys = new WeakMap<Terminal, () => boolean>();

// Like a real terminal, report a modified Enter only to a program that asked
// for extended keys, through the kitty keyboard protocol or modifyOtherKeys.
export function trackKeyboardModes(terminal: Terminal): void {
  let kitty = [0];
  let otherKeys = 0;
  const flags = () => kitty[kitty.length - 1];
  extendedKeys.set(terminal, () => flags() > 0 || otherKeys > 0);
  terminal.parser.registerCsiHandler({ prefix: "?", final: "u" }, () => { terminal.input(`\x1b[?${flags()}u`, false); return true; });
  terminal.parser.registerCsiHandler({ prefix: ">", final: "u" }, (params) => { kitty = [...kitty, Number(params[0]) || 0].slice(-16); return true; });
  terminal.parser.registerCsiHandler({ prefix: "<", final: "u" }, (params) => { kitty = kitty.slice(0, Math.max(1, kitty.length - (Number(params[0]) || 1))); return true; });
  terminal.parser.registerCsiHandler({ prefix: "=", final: "u" }, (params) => {
    const [value, mode] = [Number(params[0]) || 0, Number(params[1]) || 1];
    kitty = [...kitty.slice(0, -1), mode === 2 ? flags() | value : mode === 3 ? flags() & ~value : value];
    return true;
  });
  terminal.parser.registerCsiHandler({ prefix: ">", final: "m" }, (params) => {
    if (Number(params[0]) === 4) otherKeys = Number(params[1]) || 0;
    return false;
  });
}

export function terminalKey(event: KeyboardEvent, terminal: Terminal, copy: () => void, modes: Terminal = terminal): boolean | null {
  if (event.ctrlKey && !event.altKey && !event.metaKey) {
    // Keep the browser's paste event; xterm would send Ctrl+V as SYN.
    if (event.key.toLowerCase() === "v") return false;
    if (event.key.toLowerCase() === "c" && (event.shiftKey || terminal.hasSelection())) {
      event.preventDefault();
      if (event.type === "keydown") {
        copy();
        if (!event.shiftKey) terminal.clearSelection();
      }
      return false;
    }
  }
  if (event.key === "Enter" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && extendedKeys.get(modes)?.()) {
    event.preventDefault();
    if (event.type === "keydown") terminal.input("\x1b[13;2u", true);
    return false;
  }
  return null;
}
