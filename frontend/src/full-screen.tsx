import { useLayoutEffect, useRef, type ReactNode } from "react";

// FullScreen shows a card over the whole window, the board dimmed under it
// (#2524: "The full screen should open up like a full window or full
// width"), until Escape or the card's own button closes it and the keyboard
// returns to what opened it. Its keys, its clicks and its cancel stay its
// own, so Escape closes it alone and reaches nothing under it: React hands a
// dialog's cancel to the dialog that holds it too, as the Command Center
// holds a command's card.
export function FullScreen({ label, onClose, children }: { label: string; onClose: () => void; children: ReactNode }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const opened = dialog.current;
    opened?.showModal();
    return () => opened?.close();
  }, []);
  return <dialog ref={dialog} className="full-screen" aria-label={label} onCancel={(event) => { event.preventDefault(); event.stopPropagation(); onClose(); }}
    onKeyDown={(event) => event.stopPropagation()} onClick={(event) => event.stopPropagation()}>{children}</dialog>;
}
