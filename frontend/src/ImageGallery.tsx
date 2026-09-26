import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icon";

export interface GalleryImage { src: string; label: string; value: string; text: string }

// A question's or review item's images one at a time at full size: swipe, arrow keys or the
// side buttons move between them, and a click or tap zooms in and out.
export function ImageGallery({ images, index, lavish, onIndex, onClose, onChoose }: {
  images: GalleryImage[]; index: number; lavish?: string; onIndex: (index: number) => void; onClose: () => void; onChoose?: (value: string) => void;
}) {
  const [zoomed, setZoomed] = useState(false);
  const [missing, setMissing] = useState<Set<string>>(new Set());
  const root = useRef<HTMLDivElement>(null);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  useEffect(() => { root.current?.focus({ preventScroll: true }); }, []);
  const image = images[index];
  const go = (step: number) => { setZoomed(false); onIndex((index + step + images.length) % images.length); };
  return <div ref={root} className="gallery" role="group" aria-roledescription="gallery" aria-label={"Image " + (index + 1) + " of " + images.length + ": option " + image.label} tabIndex={-1}
    onKeyDown={(event) => {
      if (event.key === "ArrowRight") { event.preventDefault(); go(1); }
      if (event.key === "ArrowLeft") { event.preventDefault(); go(-1); }
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); onClose(); }
    }}>
    <div className="gallery-top">
      <span className="count-pill">{index + 1} of {images.length}</span>
      <strong>{image.label}. {image.text}</strong>
      {lavish && <a className="icon-button raised pill-link" href={lavish} target="_blank" rel="noreferrer" aria-label="Annotate in Lavish" data-tip="Annotate in Lavish"><Icon name="external" /><span>Lavish</span></a>}
      <button type="button" className="icon-button raised" aria-label={zoomed ? "Zoom out" : "Zoom in"} data-tip={zoomed ? "Zoom out" : "Zoom in"} onClick={() => setZoomed(!zoomed)}><Icon name={zoomed ? "minus" : "plus"} /></button>
      <button type="button" className="icon-button raised" aria-label="Back to the question" data-tip="Back to the question" data-tip-align="end" onClick={onClose}><Icon name="close" /></button>
    </div>
    <div className={"gallery-stage" + (zoomed ? " zoomed" : "")}
      onPointerDown={(event) => { swipe.current = { x: event.clientX, y: event.clientY }; }}
      onPointerUp={(event) => {
        const start = swipe.current;
        swipe.current = null;
        if (!start || zoomed) return;
        const dx = event.clientX - start.x;
        if (Math.abs(dx) > 60 && Math.abs(event.clientY - start.y) < 60) go(dx < 0 ? 1 : -1);
      }}>
      {images.length > 1 && <button type="button" className="icon-button raised gallery-step previous" aria-label="Previous image" data-tip="Previous" data-tip-align="start" onClick={() => go(-1)}><Icon name="back" /></button>}
      {missing.has(image.src)
        ? <p className="image-missing"><Icon name="images" />This image is no longer available.</p>
        : <img src={image.src} alt={"Option " + image.label + ": " + image.text} draggable={false} onClick={() => setZoomed(!zoomed)} onError={() => setMissing((prior) => new Set([...prior, image.src]))} />}
      {images.length > 1 && <button type="button" className="icon-button raised gallery-step next" aria-label="Next image" data-tip="Next" data-tip-align="end" onClick={() => go(1)}><Icon name="next" /></button>}
    </div>
    {images.length > 1 && <div className="gallery-strip">
      {images.map((thumb, i) => <button type="button" key={thumb.value} aria-pressed={i === index} aria-label={"Image for option " + thumb.label} onClick={() => { setZoomed(false); onIndex(i); }}>{missing.has(thumb.src) ? <span className="image-missing"><Icon name="images" /></span> : <img src={thumb.src} alt="" onError={() => setMissing((prior) => new Set([...prior, thumb.src]))} />}<span>{thumb.label}</span></button>)}
    </div>}
    {onChoose && <button type="button" className="primary gallery-choose" onClick={() => onChoose(image.value)}><Icon name="check" />Choose {image.label}</button>}
  </div>;
}
