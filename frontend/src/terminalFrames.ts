// A terminal's output drawn a whole redraw at a time, the way a native
// terminal shows a program that repaints. A chunk smaller than one host read is
// a whole redraw and is written as it is; a redraw larger than that goes inside
// a synchronized update (DECSET 2026), which xterm paints only once the update
// ends, when the redraw's short tail arrives or the stream goes quiet. The
// markers are only ever written where the stream sits between sequences and
// outside any update the program opened itself, so they never split an escape
// sequence or a character, and xterm ends an update left open after a second.

export const SYNC_START = "\x1b[?2026h";
export const SYNC_END = "\x1b[?2026l";
// The host reads the pseudo console 32 KiB at a time, so a chunk that size is
// likely part of a larger redraw, and a smaller one ends one.
const HOST_READ = 32 * 1024;
const SHORT_QUIET_MS = 1;

type State = "ground" | "escape" | "csi" | "string" | "string-escape";

// StreamTracker follows the byte stream just far enough to know whether it
// sits between sequences and characters, and whether the program has a
// synchronized update of its own open.
export class StreamTracker {
  private state: State = "ground";
  private params = "";
  // continuation bytes the current UTF-8 character still needs
  private pending = 0;
  programSync = false;

  get between(): boolean {
    return this.state === "ground" && this.pending === 0;
  }

  feed(bytes: Uint8Array): void {
    for (const byte of bytes) this.step(byte);
  }

  private step(byte: number): void {
    // CAN and SUB cancel any sequence; ESC starts a new one.
    if (this.state !== "ground" && (byte === 0x18 || byte === 0x1a)) { this.state = "ground"; return; }
    switch (this.state) {
      case "ground":
        if (byte === 0x1b) { this.state = "escape"; this.pending = 0; }
        else if (byte >= 0x80 && byte <= 0xbf) this.pending = Math.max(0, this.pending - 1);
        else if (byte >= 0xc0 && byte <= 0xdf) this.pending = 1;
        else if (byte >= 0xe0 && byte <= 0xef) this.pending = 2;
        else if (byte >= 0xf0 && byte <= 0xf7) this.pending = 3;
        else this.pending = 0;
        return;
      case "escape":
        if (byte === 0x5b) { this.state = "csi"; this.params = ""; }
        else if (byte === 0x5d || byte === 0x50 || byte === 0x58 || byte === 0x5e || byte === 0x5f) this.state = "string";
        else if (byte >= 0x30 && byte <= 0x7e) this.state = "ground";
        // Intermediates, a repeated ESC and other controls keep the escape open.
        return;
      case "csi":
        if (byte === 0x1b) this.state = "escape";
        else if (byte >= 0x40 && byte <= 0x7e) { this.finish(byte); this.state = "ground"; }
        else if (byte >= 0x20 && byte <= 0x3f) this.params += String.fromCharCode(byte);
        return;
      case "string":
        if (byte === 0x07) this.state = "ground";
        else if (byte === 0x1b) this.state = "string-escape";
        return;
      case "string-escape":
        // ESC \ ends the string; any other ESC starts a new sequence.
        if (byte === 0x5c) { this.state = "ground"; return; }
        this.state = "escape";
        this.step(byte);
        return;
    }
  }

  private finish(final: number): void {
    if (!this.params.startsWith("?") || (final !== 0x68 && final !== 0x6c)) return;
    if (this.params.slice(1).split(";").includes("2026")) this.programSync = final === 0x68;
  }
}

export class FrameWriter {
  private readonly tracker = new StreamTracker();
  private readonly write: (data: string | Uint8Array, done?: () => void) => void;
  private readonly quietMs: number;
  private readonly ended: () => void;
  private open = false;
  private timer: ReturnType<typeof setTimeout> | undefined;

  // ended is called once an update's end has been written, when xterm paints it.
  constructor(write: (data: string | Uint8Array, done?: () => void) => void, quietMs = 8, ended: () => void = () => {}) {
    this.write = write;
    this.quietMs = quietMs;
    this.ended = ended;
  }

  get updating(): boolean {
    return this.open;
  }

  push(bytes: Uint8Array, done: () => void): void {
    const large = bytes.length >= HOST_READ;
    if (large && !this.open && this.tracker.between && !this.tracker.programSync) {
      this.write(SYNC_START);
      this.open = true;
    }
    this.write(bytes, done);
    this.tracker.feed(bytes);
    if (!this.open) return;
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.end(), large ? this.quietMs : SHORT_QUIET_MS);
  }

  private end(): void {
    this.timer = undefined;
    if (!this.open || !this.tracker.between || this.tracker.programSync) return;
    this.open = false;
    this.write(SYNC_END, this.ended);
  }

  dispose(): void {
    clearTimeout(this.timer);
  }
}
