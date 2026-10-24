import { request } from "./api";

// The longest side a picture reaches this PC's clipboard at. The harnesses
// shrink a larger one before their model sees it, and a phone's photo whole
// would be tens of megabytes on its way to the PC.
const LONGEST_SIDE = 2048;

// pngOf draws a picture as a PNG of at most LONGEST_SIDE a side, upright as
// the device shows it, whatever it came as: a photo library's JPEG or a
// screenshot's PNG.
async function pngOf(picture: Blob): Promise<Blob> {
  const bitmap = await createImageBitmap(picture).catch(() => { throw new Error("That picture could not be read, so nothing was added."); });
  const scale = Math.min(1, LONGEST_SIDE / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(bitmap.width * scale));
  canvas.height = Math.max(1, Math.round(bitmap.height * scale));
  canvas.getContext("2d")?.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  return new Promise((resolve, reject) => canvas.toBlob((png) => png ? resolve(png) : reject(new Error("That picture could not be drawn, so nothing was added.")), "image/png"));
}

// putImage puts a picture on this PC's clipboard, where a terminal's harness
// reads a pasted image from once its image key is pressed, as the board whose
// token is instance. A board on another device, such as his phone, holds the
// picture there and not on this PC, which a desktop paste needs no help with.
export async function putImage(picture: Blob, instance: string): Promise<void> {
  await request("/api/terminal/image", undefined, { method: "POST", headers: { "Content-Type": "image/png", "X-CFO-Token": instance }, body: await pngOf(picture) });
}
