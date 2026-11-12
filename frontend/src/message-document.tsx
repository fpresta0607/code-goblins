import { useContext, useEffect, useRef, useState } from "react";
import { DocumentContext } from "./document-context";
import { Icon } from "./Icon";

type DocumentState = { identity: string; phase: "loading" } | { identity: string; phase: "error"; message: string } | { identity: string; phase: "ready"; url: string; shouldFocus: boolean };
const UNAVAILABLE = "That PDF is unavailable for this session.";
const TIMED_OUT = "Opening that PDF timed out. Try again.";

const focusLink = (link: HTMLAnchorElement | null) => link?.focus();

export function MessageDocument({ label, target }: { label: string; target: string }) {
  const scope = useContext(DocumentContext);
  const identity = JSON.stringify([scope.query, scope.instance, target]);
  const [state, setState] = useState<DocumentState | null>(null);
  const controller = useRef<AbortController | null>(null);
  const objectUrl = useRef("");
  const current = state?.identity === identity ? state : null;
  useEffect(() => () => {
    controller.current?.abort();
    if (objectUrl.current) URL.revokeObjectURL(objectUrl.current);
  }, [identity]);

  const load = async (shouldFocus: boolean) => {
    controller.current?.abort();
    const pending = new AbortController();
    controller.current = pending;
    const timeout = setTimeout(() => pending.abort(TIMED_OUT), 30_000);
    setState({ identity, phase: "loading" });
    try {
      const query = new URLSearchParams(scope.query);
      query.delete("token");
      query.set("document", target);
      const response = await fetch("/api/messages?" + query, { headers: { "X-CEO-Token": scope.instance }, signal: pending.signal, cache: "no-store" }).finally(() => clearTimeout(timeout));
      if (!response.ok || response.headers.get("Content-Type") !== "application/pdf") throw new Error(UNAVAILABLE);
      const pdf = await response.blob();
      if (pending.signal.aborted) return;
      if (objectUrl.current) URL.revokeObjectURL(objectUrl.current);
      const url = URL.createObjectURL(pdf);
      objectUrl.current = url;
      setState({ identity, phase: "ready", url, shouldFocus });
      window.open(url, "_blank", "noopener,noreferrer");
    } catch {
      if (controller.current === pending) setState({ identity, phase: "error", message: pending.signal.reason === TIMED_OUT ? TIMED_OUT : UNAVAILABLE });
    }
  };

  return <span className="message-document" role="group" aria-label={label}>
    <span className="message-document-icon" aria-hidden="true"><Icon name="file" /><span>PDF</span></span>
    <span className="message-document-body"><span className="message-document-label">{label}</span>
      <span className="message-document-actions">{current?.phase === "ready" ? <>
        <a ref={current.shouldFocus ? focusLink : undefined} href={current.url} target="_blank" rel="noreferrer">Open PDF</a>
        <a href={current.url} download={decodeURIComponent(target).split(/[\\/]/).at(-1)}>Download PDF</a>
      </> : <button type="button" disabled={current?.phase === "loading"} onClick={(event) => void load(event.detail === 0)}>{current?.phase === "loading" ? "Loading PDF..." : "Open PDF"}</button>}</span>
      {current?.phase === "error" && <span role="status">{current.message}</span>}
    </span>
  </span>;
}
