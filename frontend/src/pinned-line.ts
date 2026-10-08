import { Terminal } from "@xterm/xterm";
import { lineInput, pinnedLine } from "./terminal-pin";
import { terminalDocument } from "./terminalDocument";

// PinnedLine keeps the live line he types on in sight while he reads a
// terminal's history, as the Overlord asked on 2026-10-08: "terminals in
// command center should have sticky input line just like cfo with scroll and
// jump to bottom". A one-row terminal over the panel's last row draws the
// cursor's row as the terminal holds it, in the same font and colors, and the
// history scrolls above it. It draws nothing at the live end, nor in a
// full-screen program, which keeps its own input line.
export class PinnedLine {
  private readonly container: HTMLElement;
  private readonly element: HTMLDivElement;
  private line: Terminal | null = null;
  private drawn = "";

  constructor(container: HTMLElement) {
    this.container = container;
    this.element = document.createElement("div");
    this.element.className = "terminal-pin";
    this.element.hidden = true;
    this.element.setAttribute("aria-hidden", "true");
    container.append(this.element);
  }

  // update draws the pin for the terminal as it stands and returns where the
  // history ends while he reads it, the top of the panel's last row in px
  // from the panel's top, or null at the live end.
  update(term: Terminal, isCursorShown: boolean): number | null {
    const buffer = term.buffer.active;
    const pinned = pinnedLine({ viewportY: buffer.viewportY, baseY: buffer.baseY, cursorY: buffer.cursorY, rows: term.rows });
    this.element.hidden = pinned === null;
    // At the live end nothing is measured, so output there costs no layout.
    const screen = buffer.viewportY < buffer.baseY ? term.element?.querySelector(".xterm-screen")?.getBoundingClientRect() : undefined;
    if (!screen) return null;
    const panel = this.container.getBoundingClientRect();
    const row = screen.height / term.rows;
    const top = screen.top - panel.top + (term.rows - 1) * row;
    if (pinned === null) return top;
    const line = this.line ?? this.open(term);
    // The cursor is drawn as the terminal draws its own: a block while it
    // has the keyboard, an outline while it does not.
    const cursorInactiveStyle = document.activeElement === term.textarea ? "block" : "outline";
    Object.assign(line.options, { fontSize: term.options.fontSize, fontFamily: term.options.fontFamily, lineHeight: term.options.lineHeight, cursorInactiveStyle });
    if (line.cols !== term.cols) { line.resize(term.cols, 1); this.drawn = ""; }
    Object.assign(this.element.style, { left: screen.left - panel.left + "px", top: top + "px", width: screen.width + "px", height: row + "px" });
    const input = lineInput(buffer.getLine(pinned)!, term.cols, buffer.cursorX, isCursorShown);
    if (input !== this.drawn) { this.drawn = input; line.write(input); }
    return top;
  }

  dispose(): void {
    this.line?.dispose();
    this.element.remove();
  }

  private open(term: Terminal): Terminal {
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    const line = new Terminal({ documentOverride: terminalDocument(nonce), cols: term.cols, rows: 1, scrollback: 0, disableStdin: true, cursorBlink: false, fontSize: term.options.fontSize, fontFamily: term.options.fontFamily, lineHeight: term.options.lineHeight, theme: term.options.theme });
    line.open(this.element);
    if (line.textarea) line.textarea.tabIndex = -1;
    // xterm draws no cursor until its terminal is typed into, focused or
    // switched to the alternate screen, and this one is never typed into or
    // focused. The alternate screen also keeps no history, which a single
    // drawn line needs none of.
    line.write("\x1b[?1049h");
    this.line = line;
    return line;
  }
}
