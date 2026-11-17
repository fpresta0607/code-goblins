import { copyText } from "./clipboard";
import { pngOf } from "./terminal-image";

// The pictures a conversation's rows draw, each read from its address: those
// of his prompts and of a call's result, and a call's thumbnails.
const PICTURES = ".message-picture img, .call-picture img";

// The copy of a selection before this one. Chromium fills the clipboard from
// a page's writes one kind of data at a time and puts what it holds on at the
// end of each, so two writes at once split their data between them: the copy
// a key's release makes ran into the one Ctrl+C made and left a picture alone
// on the clipboard, without its words (#2543). Each copy begins once the one
// before it has ended.
let lastCopy: Promise<void> = Promise.resolve();

// picturesIn is the pictures a selection of a conversation takes in, in their
// order.
export function picturesIn(range: Range): HTMLImageElement[] {
  const root = range.commonAncestorContainer;
  const scope = root instanceof Element ? root : root.parentElement;
  return [...scope?.querySelectorAll<HTMLImageElement>(PICTURES) ?? []].filter((image) => range.intersectsNode(image));
}

// copySelected copies a selection of a conversation, whose text reads as
// text. One that takes in no picture copies that text alone, as it always
// has. One that does copies one item (#2543): the same text, HTML that holds
// its words and its pictures in their order, each picture inline as PNG data,
// so a rich target shows both and no picture's board address, whose query
// carries the board's token, leaves the board, and its first picture as a
// picture, for a target that takes only that. A picture that cannot be read
// is left out; where none can be, or the browser offers no clipboard for a
// picture, as over plain HTTP, the text is copied alone.
export function copySelected(range: Range, text: string): Promise<void> {
  const pictures = picturesIn(range);
  if (!pictures.length || !navigator.clipboard?.write || typeof ClipboardItem === "undefined") return inTurn(() => copyText(text));
  const holder = document.createElement("div");
  holder.append(range.cloneContents());
  const sources = pictures.map((image) => image.src);
  const read = Promise.all(sources.map((source) => fetch(source).then((response) => response.blob()).then(pngOf)
    .then(async (png) => ({ png, address: await inlined(png) })).catch(() => null)));
  const item = new ClipboardItem({
    "text/plain": new Blob([text], { type: "text/plain" }),
    "text/html": read.then((each) => new Blob([htmlOf(holder, sources, each)], { type: "text/html" })),
    "image/png": read.then((each) => each.find((picture) => picture !== null)?.png ?? Promise.reject(new Error("No picture of the selection could be read."))),
  });
  return inTurn(() => navigator.clipboard.write([item]).catch(() => copyText(text)));
}

// inTurn begins a copy once the copy before it has ended, however it ended.
function inTurn(copy: () => Promise<void>): Promise<void> {
  const copied = lastCopy.then(copy);
  lastCopy = copied.catch(() => {});
  return copied;
}

// htmlOf is a selection's copied contents as HTML, each picture inline in
// place of the button that opens it, and without the board's other controls
// and icons, which a target would draw without the board's styles.
function htmlOf(holder: HTMLElement, sources: string[], pictures: ({ address: string } | null)[]): string {
  for (const clone of holder.querySelectorAll("img")) {
    const address = pictures[sources.indexOf(clone.src)]?.address;
    const place = clone.closest("button") ?? clone;
    if (!address) {
      place.remove();
      continue;
    }
    const picture = document.createElement("img");
    picture.src = address;
    picture.alt = clone.alt;
    place.replaceWith(picture);
  }
  for (const control of holder.querySelectorAll("button, svg")) control.remove();
  return holder.innerHTML;
}

// inlined writes a PNG as an address that holds it, as rich HTML carries a
// picture inline.
function inlined(png: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(png);
  });
}
