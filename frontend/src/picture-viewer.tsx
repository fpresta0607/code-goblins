import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Icon } from "./Icon";
import { PictureImage } from "./picture-image";

// A touch that moves this far sideways, and less than it up or down, steps
// to the picture beside the one shown.
const SWIPE_PX = 60;

// Pictures one at a time over the whole window, with the board dimmed under
// them, as Claude's app shows a conversation's: the picture as large as the
// window allows, and under it the arrows to the one before and the one after
// with its place among them all (38 of 40). The Left and Right keys and a
// swipe step too, a click on the picture shows it at its own size and back,
// and Escape, Close or a click beside the picture puts the viewer away and
// returns the keyboard to what opened it. source is the picture shown, by the
// address it is read from, among sources; only that one is loaded, and only
// once it shows. When sources no longer holds it, as when its conversation is
// replaced, the viewer closes itself. Nothing it holds reaches the message
// under it, whose click shows its Copy row.
export function PictureViewer({ sources, source, onSource, onClose }: {
  sources: string[]; source: string; onSource: (source: string) => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const view = useRef<HTMLDivElement>(null);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const [isZoomed, setZoomed] = useState(false);
  const index = sources.indexOf(source);
  useLayoutEffect(() => {
    const opened = dialog.current;
    opened?.showModal();
    view.current?.focus({ preventScroll: true });
    return () => opened?.close();
  }, []);
  useEffect(() => { if (index < 0) onClose(); }, [index, onClose]);
  if (index < 0) return <dialog ref={dialog} className="picture-viewer" aria-label="Pictures" />;
  const step = (by: number) => {
    const next = sources[index + by];
    if (next === undefined) return;
    setZoomed(false);
    onSource(next);
  };
  return <dialog ref={dialog} className="picture-viewer" aria-label="Pictures"
    onCancel={(event) => { event.preventDefault(); onClose(); }} onClick={(event) => event.stopPropagation()}>
    <div ref={view} className="picture-view" tabIndex={-1}
      onKeyDown={(event) => {
        event.stopPropagation();
        if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
        event.preventDefault();
        step(event.key === "ArrowLeft" ? -1 : 1);
      }}>
      <div className={"picture-stage" + (isZoomed ? " zoomed" : "")}
        onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
        onPointerDown={(event) => { swipe.current = event.pointerType === "touch" ? { x: event.clientX, y: event.clientY } : null; }}
        onPointerUp={(event) => {
          const start = swipe.current;
          swipe.current = null;
          if (!start || isZoomed) return;
          const across = event.clientX - start.x;
          if (Math.abs(across) > SWIPE_PX && Math.abs(event.clientY - start.y) < SWIPE_PX) step(across < 0 ? 1 : -1);
        }}>
        <PictureImage src={source} alt={"Picture " + (index + 1) + " of " + sources.length} onClick={() => setZoomed((prior) => !prior)}>This image is no longer available.</PictureImage>
      </div>
      <div className="picture-steps">
        <button type="button" className="previous" aria-label="Previous picture" aria-disabled={index === 0} onClick={() => step(-1)}><Icon name="chevron" /></button>
        <span role="status">{index + 1} of {sources.length}</span>
        <button type="button" aria-label="Next picture" aria-disabled={index === sources.length - 1} onClick={() => step(1)}><Icon name="chevron" /></button>
      </div>
      <button type="button" className="picture-close" aria-label="Close" onClick={onClose}><Icon name="close" /></button>
    </div>
  </dialog>;
}
