// The files he pastes into, drops on or adds to the message box, which the
// supervisor keeps where the session reads them (internal/supervisor/
// composer_files.go), and the message names by path, one to a line.

const PICTURE = /\.(?:png|jpe?g|gif|webp)$/i;
// A line of his words that is only the path of any file the box kept.
const KEPT_FILE = /^(?:[A-Za-z]:[\\/]|[\\/]).*[\\/]composer-files[\\/][^\\/]+$/i;
// A line of his words that is only the path of a picture the box kept.
const KEPT_PICTURE = /^(?:[A-Za-z]:[\\/]|[\\/]).*[\\/]composer-files[\\/]pasted-[^\\/]+\.(?:png|jpe?g|gif|webp)$/i;
// The paths of pictures the box kept that end a line of his words, joined to
// them by spaces: a harness's record keeps a message as the board typed it,
// on one line. Only a path with no space in it can be told from words there.
const JOINED_PICTURES = /(?:^|\s+)((?:(?:[A-Za-z]:[\\/]|[\\/])\S*[\\/]composer-files[\\/]pasted-[^\\/\s]+\.(?:png|jpe?g|gif|webp)\s*)+)$/i;
// The path of a file the box kept, from its root to the name the box gave it,
// through folders whose names may hold a space, as a Windows account's folder
// may, and never across a pasted text or another path after it on its line.
const KEPT_PATH = String.raw`(?:[A-Za-z]:[\\/]|[\\/])(?:(?!\s(?:Pasted text, |[A-Za-z]:[\\/]|[\\/])).)*?[\\/]composer-files[\\/]pasted-[^\\/\s]+`;
// A line that is only a long paste the box kept as a text file, as Send names
// it (pastedTextLine).
const PASTED_TEXT = new RegExp(String.raw`^Pasted text, (\d{1,3}(?:,\d{3})*) lines?: (${KEPT_PATH}\.txt)$`, "i");
// The last of the pasted texts and other files the box kept that end a line
// of his words, joined to them by spaces as a harness's record keeps them: a
// pasted text by its first two groups, any other kept file by its third.
const JOINED_KEPT = new RegExp(String.raw`(?:^|\s+)(?:Pasted text, (\d{1,3}(?:,\d{3})*) lines?: (${KEPT_PATH}\.txt)|(${KEPT_PATH}))\s*$`, "i");

// A long paste the box kept as a text file the session reads whole (#2404).
export interface PastedText { path: string; lines: number }

export function isPicturePath(path: string): boolean {
  return PICTURE.test(path);
}

// keptPictures splits his words from the pictures the box kept for them, so
// his bubble shows each picture rather than its path.
export function keptPictures(text: string): { text: string; paths: string[] } {
  const paths: string[] = [];
  const words = text.split("\n").flatMap((line) => {
    const joined = line.match(JOINED_PICTURES);
    if (joined) {
      paths.push(...joined[1].trim().split(/\s+/));
      const rest = line.slice(0, joined.index).trimEnd();
      return rest.trim() ? [rest] : [];
    }
    if (!KEPT_PICTURE.test(line.trim())) return [line];
    paths.push(line.trim());
    return [];
  });
  return paths.length ? { text: words.join("\n").trim(), paths } : { text, paths };
}

// pastedTextName is a pasted text's chip: what it is and how many lines it
// holds.
export function pastedTextName(lines: number): string {
  return "Pasted text, " + lines.toLocaleString("en-US") + (lines === 1 ? " line" : " lines");
}

// pastedTextLine is how Send names a pasted text, on a line of its own after
// his words: its file, and its length, so the session reads every line.
export function pastedTextLine(path: string, lines: number): string {
  return pastedTextName(lines) + ": " + path;
}

// pastedTexts splits his words from the pasted texts named in them, so his
// bubble shows each as its chip. A harness's record joins the lines the board
// typed with spaces, so the pasted texts and other files the box kept are
// read from the end of each line, with his words or none before them; each
// other kept file stays in his words, a picture for keptPictures.
export function pastedTexts(text: string): { text: string; pastes: PastedText[] } {
  if (!text.includes("Pasted text, ")) return { text, pastes: [] };
  const pastes: PastedText[] = [];
  const words = text.split("\n").flatMap((line) => {
    const found: PastedText[] = [];
    const files: string[] = [];
    let rest = line;
    for (let kept = rest.match(JOINED_KEPT); kept; kept = rest.match(JOINED_KEPT)) {
      if (kept[3]) files.unshift(kept[3]);
      else found.unshift({ path: kept[2], lines: Number(kept[1].replaceAll(",", "")) });
      rest = rest.slice(0, kept.index);
    }
    if (!found.length) return [line];
    pastes.push(...found);
    const left = [rest.trimEnd(), ...files].filter(Boolean).join(" ");
    return left ? [left] : [];
  });
  return pastes.length ? { text: words.join("\n").trim(), pastes } : { text, pastes };
}

// keptFiles splits his words from every file the box kept for them, each by
// its path, and from each pasted text, by its line as Send named it, so a
// queued message taken back into the box shows them as it did.
export function keptFiles(text: string): { text: string; paths: string[] } {
  const paths: string[] = [];
  const words = text.split("\n").filter((line) => {
    const path = line.trim();
    if (!KEPT_FILE.test(path) && !PASTED_TEXT.test(path)) return true;
    paths.push(path);
    return false;
  });
  return paths.length ? { text: words.join("\n").trim(), paths } : { text, paths };
}

// keptFileAddress reads a kept picture back from the supervisor, with the
// board's token in the query, as an <img> sends no header.
export function keptFileAddress(instance: string, task: string | undefined, path: string): string {
  const query = new URLSearchParams({ token: instance });
  if (task) query.set("task", task);
  query.set("path", path);
  return "/api/composer/file?" + query.toString();
}

// filesOf is what a paste or a drop holds as files. A screenshot pasted in
// some browsers arrives only as an item of kind "file" with an empty files
// list, so the items are read when the list is empty.
export function filesOf(data: DataTransfer | null): File[] {
  if (!data) return [];
  if (data.files.length) return Array.from(data.files);
  return Array.from(data.items).flatMap((item) => {
    const file = item.kind === "file" ? item.getAsFile() : null;
    return file ? [file] : [];
  });
}

// The attribute that marks each picture a selection copied from a
// conversation holds inline in its HTML (#2543), which no other HTML's
// pictures carry.
export const CONVERSATION_PICTURE = "data-conversation-picture";

// A picture a paste's HTML holds inline as base64 PNG data, as a selection
// copied from a conversation holds each of its pictures (#2543).
const INLINE_PICTURE = /^data:image\/png;base64,((?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?)$/;

// inlinePictures is the pictures a selection copied from a conversation
// holds inline in a paste's HTML, in their order, and none of any other
// HTML's.
export function inlinePictures(html: string): File[] {
  if (!html.includes(CONVERSATION_PICTURE)) return [];
  return Array.from(new DOMParser().parseFromString(html, "text/html").querySelectorAll(`img[${CONVERSATION_PICTURE}]`)).flatMap((image) => {
    const data = INLINE_PICTURE.exec(image.getAttribute("src") ?? "")?.[1];
    return data ? [new File([Uint8Array.from(atob(data), (character) => character.charCodeAt(0))], "pasted.png", { type: "image/png" })] : [];
  });
}

// pastedPictures is the pictures a paste brings a box, in their order, and
// whether the box takes the paste's words as text too: a selection copied from
// a conversation brings each picture it holds inline, with its words (#2543),
// and otherwise the picture files the clipboard holds are the paste, which the
// box takes alone.
export function pastedPictures(clipboard: DataTransfer): { pictures: File[]; shouldKeepWords: boolean } {
  const inline = inlinePictures(clipboard.getData("text/html"));
  if (inline.length) return { pictures: inline, shouldKeepWords: true };
  return { pictures: filesOf(clipboard).filter((file) => file.type.startsWith("image/")), shouldKeepWords: false };
}

// clipboardPicture reads the picture the clipboard holds, for a phone's
// Paste a picture: an iPhone offers its own Paste in a plain text box only
// for text (WebKit's supportedPasteboardTypesForCurrentSelection), so a
// copied photo or screenshot never reaches the box's paste. Safari asks with
// its own Paste button before it hands the picture over.
export async function clipboardPicture(): Promise<{ picture: File } | { error: string }> {
  if (!navigator.clipboard?.read) return { error: "This browser cannot read a picture from the clipboard." };
  try {
    for (const item of await navigator.clipboard.read()) {
      const type = item.types.find((each) => each.startsWith("image/"));
      if (type) return { picture: new File([await item.getType(type)], "pasted.png", { type }) };
    }
    return { error: "The clipboard holds no picture. Copy one, then paste it here." };
  } catch {
    return { error: "The clipboard was not read, so no picture was added." };
  }
}
