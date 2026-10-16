import { useEffect, useRef, useState } from "react";
import { reportToCfo } from "./api";
import { Icon } from "./Icon";
import "./afk.css";

export function AfkReport({ page, hasPendingItems, onClose, onOpenCommand }: { page: string; hasPendingItems: boolean; onClose: () => void; onOpenCommand: (key: string) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [status, setStatus] = useState<"loading" | "ready" | "failed">("loading");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const loaded = (frame: HTMLIFrameElement) => {
    const content = frame.contentDocument;
    if (!content?.querySelector("article.back")) {
      setStatus("failed");
      reportToCfo("AFK report", "The saved report could not be loaded");
      return;
    }
    // The saved overview has its own layout. It inherits only the board's
    // theme tokens, so neither document changes the other's cards.
    const styles = getComputedStyle(document.documentElement);
    for (const name of styles) if (name.startsWith("--")) content.documentElement.style.setProperty(name, styles.getPropertyValue(name));
    content.addEventListener("click", (event) => {
      const element = event.target;
      const view = content.defaultView;
      if (!view || !(element instanceof view.Element)) return;
      const key = element.closest<HTMLElement>("[data-item]")?.dataset.item;
      if (!key) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      onClose();
      onOpenCommand(key);
    }, true);
    content.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && !content.querySelector("dialog[open]")) { event.preventDefault(); onClose(); }
    });
    setStatus("ready");
  };
  return <dialog ref={dialog} className="afk-report" aria-label="Your AFK report" onKeyDown={(event) => event.stopPropagation()} onCancel={(event) => { event.preventDefault(); onClose(); }}>
    <header className="afk-report-bar"><span>Your AFK report</span><button className="icon-button" aria-label="Close AFK report" data-tip="Close" onClick={onClose}><Icon name="close" /></button></header>
    {status === "loading" && <p className="afk-report-message" role="status">Loading your report</p>}
    {status === "failed" && <div className="afk-report-message"><p role="status">Report unavailable</p><button onClick={() => { setStatus("loading"); setAttempt(attempt + 1); }}>Try again</button></div>}
    {page && <iframe key={attempt} src={page} title="What happened while you were away" hidden={status === "failed"} className={status === "ready" ? "is-ready" : ""} onLoad={(event) => loaded(event.currentTarget)} />}
    <footer className="afk-report-actions">
      {!hasPendingItems && <p>Nothing is waiting in Command Center.</p>}
      <button className="primary" disabled={!hasPendingItems} onClick={() => { onClose(); onOpenCommand(""); }}>Go through what needs you</button>
    </footer>
  </dialog>;
}
