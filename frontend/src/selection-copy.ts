import { copyText } from "./clipboard";
import { CONVERSATION_PICTURE } from "./kept-files";
import { pngOf } from "./terminal-image";

// The pictures a conversation's rows draw, each read from its address: those
// of his prompts and of a call's result, and a call's thumbnails.
const PICTURES = ".message-picture img, .call-picture img";

// The most pictures one copy carries. Each is drawn as a PNG up to 2048 px a
// side and held inline in one HTML string, and none of the copy, not even its
// words, reaches the clipboard until every one is drawn, so a selection that
// takes in more, as a drag across a long run of screenshots does, copies its
// words alone, as it did before #2543.
const MOST_PICTURES = 10;

// The copy of a selection before this one. Chromium fills the clipboard from
// a page's writes one kind of data at a time and puts what it holds on at the
// end of each, so two writes at once split their data between them: the copy
// a key's release makes ran into the one Ctrl+C made and left a picture alone
// on the clipboard, without its words (#2543). Each copy begins once the one
// before it has ended.
let lastCopy: Promise<void> = Promise.resolve();

// A copy of a selection once its pictures are drawn: its HTML, each picture
// inline, and its first picture.
type Drawn = { html: string; png: Blob };

// The copy last made of a selection that takes in pictures: its contents, as
// contentsOf reads them, that copy once its pictures are drawn, and its HTML
// once they are. A copy of the same contents, as Ctrl+C and each key's
// release make of a selection that stands, takes them again rather than
// reading and drawing each picture anew, and the browser's own copy of it
// holds that HTML at once (copiedHtml).
type Kept = { contents: string; drawn: Promise<Drawn>; html: string };
let kept: Kept | null = null;

// imagesIn is every image a selection's contents hold, shown or not, in their
// order, as a copy of those contents holds them.
function imagesIn(range: Range): HTMLImageElement[] {
  const root = range.commonAncestorContainer;
  const scope = root instanceof Element ? root : root.parentElement;
  return [...scope?.querySelectorAll("img") ?? []].filter((image) => range.intersectsNode(image));
}

// picturesIn is the pictures a selection of a conversation shows, in their
// order.
export function picturesIn(range: Range): HTMLImageElement[] {
  return imagesIn(range).filter((image) => image.matches(PICTURES) && image.getClientRects().length > 0);
}

// copySelected copies a selection of a conversation, whose text reads as
// text. One that takes in no picture copies that text alone, as it always
// has. One that does copies one item (#2543): the same text, HTML that holds
// its words and its pictures in their order, each picture inline as PNG data,
// so a rich target shows both and no picture's board address, whose query
// carries the board's token, leaves the board, and its first picture as a
// picture, for a target that takes only that. A picture that cannot be read
// is left out; where none can be, where the selection takes in more pictures
// than one copy carries, or where the browser offers no clipboard for a
// picture, as over plain HTTP, the text is copied alone, and a selection with
// no words copies nothing and fails, so the clipboard keeps what it held.
export function copySelected(range: Range, text: string): Promise<void> {
  const pictures = picturesIn(range);
  const copyWordsAlone = () => text.trim() ? copyText(text) : Promise.reject(new Error("The selection holds no words to copy without its pictures."));
  if (!pictures.length || pictures.length > MOST_PICTURES || !navigator.clipboard?.write || typeof ClipboardItem === "undefined") return inTurn(copyWordsAlone);
  const holder = contentsOf(range, pictures);
  const contents = holder.innerHTML;
  const copy = kept?.contents === contents ? kept : drawing(contents, holder, pictures);
  kept = copy;
  const item = new ClipboardItem({
    "text/plain": new Blob([text], { type: "text/plain" }),
    "text/html": copy.drawn.then(({ html }) => new Blob([html], { type: "text/html" })),
    "image/png": copy.drawn.then(({ png }) => png),
  });
  return inTurn(() => navigator.clipboard.write([item]).catch(copyWordsAlone));
}

// copiedHtml is the HTML a copy of a selection holds, where the copy last
// made was of the same contents and has drawn its pictures, so the browser's
// own copy of it can hold that HTML at once; otherwise it is empty.
export function copiedHtml(range: Range): string {
  return kept?.contents === contentsOf(range, picturesIn(range)).innerHTML ? kept.html : "";
}

// drawing reads and draws the pictures of a selection, whose contents holder
// holds as contentsOf reads them, for its copy.
function drawing(contents: string, holder: HTMLElement, pictures: HTMLImageElement[]): Kept {
  const drawn = Promise.all(pictures.map((image) => fetch(image.src).then((response) => response.blob()).then(pngOf)
    .then(async (png) => ({ png, address: await inlined(png) })).catch(() => null))).then((each) => {
    const png = each.find((picture) => picture !== null)?.png;
    if (!png) throw new Error("No picture of the selection could be read.");
    return { html: htmlOf(holder, each), png };
  });
  const copy: Kept = { contents, drawn, html: "" };
  drawn.then(({ html }) => { copy.html = html; }, () => { if (kept === copy) kept = null; });
  return copy;
}

// inTurn begins a copy once the copy before it has ended, however it ended.
function inTurn(copy: () => Promise<void>): Promise<void> {
  const copied = lastCopy.then(copy);
  lastCopy = copied.catch(() => {});
  return copied;
}

// contentsOf is a selection's contents as its copy holds them: each picture
// it shows in place of the button that opens it, and none of its other
// images, the board's controls and icons, or the card a pointer shows over a
// thumbnail, which a target would draw without the board's styles. Its
// images pair with the selection's own by their order.
function contentsOf(range: Range, pictures: HTMLImageElement[]): HTMLElement {
  const holder = document.createElement("div");
  holder.append(range.cloneContents());
  const images = imagesIn(range);
  holder.querySelectorAll("img").forEach((clone, at) => {
    const place = clone.closest("button") ?? clone;
    if (!pictures.includes(images[at])) place.remove();
    else if (place !== clone) place.replaceWith(clone);
  });
  for (const control of holder.querySelectorAll("button, svg, .picture-hover-card")) control.remove();
  return holder;
}

// htmlOf is a selection's contents, as contentsOf reads them, with each of
// its pictures inline as drawn and marked as a conversation's, and without
// one that could not be read.
function htmlOf(holder: HTMLElement, pictures: ({ address: string } | null)[]): string {
  holder.querySelectorAll("img").forEach((clone, at) => {
    const address = pictures[at]?.address;
    if (!address) {
      clone.remove();
      return;
    }
    const picture = document.createElement("img");
    picture.src = address;
    picture.alt = clone.alt;
    picture.setAttribute(CONVERSATION_PICTURE, "");
    clone.replaceWith(picture);
  });
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
