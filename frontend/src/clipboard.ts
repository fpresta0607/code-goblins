// Copies text to the clipboard. A page served over plain HTTP, such as across
// the tailnet, has no navigator.clipboard, so there the text is copied from a
// field off screen, placed in the open dialog when the click came from one,
// since nothing outside a modal dialog takes a selection. The promise rejects
// when neither way copies.
export function copyText(text: string): Promise<void> {
  if (navigator.clipboard) return navigator.clipboard.writeText(text);
  const focused = document.activeElement;
  const field = document.createElement("textarea");
  field.value = text;
  field.readOnly = true;
  field.className = "sr-only";
  (focused?.closest("dialog") || document.body).append(field);
  field.focus({ preventScroll: true });
  field.setSelectionRange(0, text.length);
  const copied = document.execCommand("copy");
  field.remove();
  if (focused instanceof HTMLElement) focused.focus({ preventScroll: true });
  return copied ? Promise.resolve() : Promise.reject(new Error("This page cannot copy to the clipboard."));
}
