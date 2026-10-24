import type { Terminal } from "@xterm/xterm";

// The key that erases the character before the cursor, as Backspace sends it.
const ERASE = "\x7f";

// The keys that make a program's input line, whose end mirrors a text box,
// follow the box from before to after. Text before floor holds what was
// written before a key the program got straight from the keyboard, and is
// never erased: the key followed it in the program. What changed is erased
// back to where the two first differ, never splitting a character, and typed
// again from there.
export function mirroredEdit(before: string, after: string, floor: number): string {
  let common = 0;
  while (common < before.length && common < after.length && before[common] === after[common]) common++;
  const code = before.charCodeAt(common - 1);
  if (common > 0 && code >= 0xd800 && code <= 0xdbff) common--;
  const from = Math.max(common, floor);
  return ERASE.repeat([...before.slice(from)].length) + after.slice(from);
}

// attachDictatedInput sends what is written into a terminal's text box
// without a key, as a phone's dictation, an emoji picker or a suggestion
// writes, as the change it made to the box. A phone's dictation writes each
// partial result over the one before it, and xterm sent each as new keys, so
// a dictated sentence reached the program once per partial (the Overlord,
// 2026-10-21, "why is everything double printing"). Keys, pastes and an input
// method's composition stay xterm's. It returns what detaches it.
export function attachDictatedInput(term: Terminal, send: (keys: string) => void): () => void {
  const element = term.element, box = term.textarea;
  if (!element || !box) return () => {};
  // known is the box's text as last seen. xterm empties the box in the
  // handler of the Enter or Ctrl+C it sends, so the box is read again once a
  // key has been handled.
  let known = box.value, floor = 0, isKeyHeld = false;
  let reread: ReturnType<typeof setTimeout> | undefined;
  const input = (event: Event) => {
    if (event.target !== box) return;
    floor = Math.min(floor, known.length);
    if (!isKeyHeld && event instanceof InputEvent && !event.isComposing && event.inputType !== "insertCompositionText") {
      // xterm's own handler, on the box, would send the partial whole.
      event.stopPropagation();
      const keys = mirroredEdit(known, box.value, floor);
      if (keys) send(keys);
    }
    known = box.value;
    floor = Math.min(floor, known.length);
  };
  const keyDown = (event: KeyboardEvent) => {
    isKeyHeld = true;
    if (!event.isComposing && event.keyCode !== 229) floor = box.value.length;
    clearTimeout(reread);
    reread = setTimeout(() => { known = box.value; });
  };
  // A key let go after the box lost the focus never says so here.
  const keyUp = () => { isKeyHeld = false; known = box.value; };
  element.addEventListener("input", input, true);
  element.addEventListener("keydown", keyDown, true);
  element.addEventListener("keyup", keyUp, true);
  box.addEventListener("blur", keyUp);
  return () => {
    clearTimeout(reread);
    element.removeEventListener("input", input, true);
    element.removeEventListener("keydown", keyDown, true);
    element.removeEventListener("keyup", keyUp, true);
    box.removeEventListener("blur", keyUp);
  };
}
