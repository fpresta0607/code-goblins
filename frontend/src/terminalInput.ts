export const maxInputBytes = 64 * 1024;
export const inputBytes = (text: string) => new TextEncoder().encode(text).byteLength;
export function bracketedPaste(text: string): string {
  const input = "\x1b[200~" + text.replaceAll("\x1b[200~", "").replaceAll("\x1b[201~", "") + "\x1b[201~";
  if (inputBytes(input) > maxInputBytes) throw new Error("Paste exceeds 64 KiB. Paste a smaller selection; nothing was sent.");
  return input;
}
