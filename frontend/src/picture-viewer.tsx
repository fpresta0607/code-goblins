import { useEffect, useRef } from "react";
import { ImageGallery } from "./ImageGallery";

// A message's pictures larger, over the board, in the board's picture
// gallery: Escape, a click beside it or Close puts it away. Nothing it holds
// reaches the message under it, whose click shows its Copy row.
export function PictureViewer({ sources, index, onIndex, onClose }: {
  sources: string[]; index: number; onIndex: (index: number) => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="picture-viewer" aria-label="Pictures"
    onCancel={(event) => { event.preventDefault(); onClose(); }} onKeyDown={(event) => event.stopPropagation()}
    onClick={(event) => { event.stopPropagation(); if (event.target === event.currentTarget) onClose(); }}>
    <ImageGallery images={sources.map((src, at) => ({ src, value: src, text: "Picture " + (at + 1) }))} index={index} closeLabel="Close" onIndex={onIndex} onClose={onClose} />
  </dialog>;
}
