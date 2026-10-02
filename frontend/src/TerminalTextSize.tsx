import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icon";
import { followFontSize, fontSizeFor, MAX_FONT_SIZE, MIN_FONT_SIZE, storedFontSize, storeFontSize } from "./terminalStream";

// The terminals' text size, for a terminal's panel: smaller, larger, and the
// size itself, which puts it back. It is the one size every terminal draws
// at, so it shows what the keys and the wheel chose as well, and its buttons
// do what those keys do. While the keyboard is anywhere in the panel but the
// terminal, which takes these keys itself, Ctrl with plus, minus or zero
// sizes the text too: choosing a goblin leaves the keyboard on the panel, and
// the keys did nothing there.
export function TerminalTextSize() {
  const [size, setSize] = useState(storedFontSize);
  const group = useRef<HTMLDivElement>(null);
  useEffect(() => followFontSize(setSize), []);
  useEffect(() => {
    const panel = group.current?.closest<HTMLElement>(".context-pane");
    if (!panel) return;
    const key = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !event.ctrlKey || event.altKey || event.metaKey) return;
      const next = fontSizeFor(event.key, storedFontSize());
      if (next === null) return;
      event.preventDefault();
      storeFontSize(next);
    };
    panel.addEventListener("keydown", key);
    return () => panel.removeEventListener("keydown", key);
  }, []);
  const choose = (key: string) => storeFontSize(fontSizeFor(key, size) ?? size);
  return <div className="text-size" role="group" aria-label="Terminal text size" ref={group}>
    <button className="icon-button" aria-label="Smaller terminal text" data-tip="Smaller text" data-tip-align="end" disabled={size <= MIN_FONT_SIZE} onClick={() => choose("-")}><Icon name="minus" /></button>
    <button className="text-size-reset" aria-label={"Reset the terminal text size, now " + size + " pixels"} data-tip="Reset text size" data-tip-align="end" onClick={() => choose("0")}>{size}</button>
    <button className="icon-button" aria-label="Larger terminal text" data-tip="Larger text" data-tip-align="end" disabled={size >= MAX_FONT_SIZE} onClick={() => choose("+")}><Icon name="plus" /></button>
  </div>;
}
