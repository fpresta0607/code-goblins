import { useRef, useState } from "react";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { object, string } from "./types";

export function CfoRestart({ instance, onRestarted }: { instance: string; onRestarted: (source: HTMLElement) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const accept = useRef<HTMLButtonElement>(null);
  const [identity, setIdentity] = useState("");
  const [failure, setFailure] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [isRestarting, setIsRestarting] = useState(false);
  const [session, setSession] = useState("");
  const close = () => {
    dialog.current?.close();
    trigger.current?.focus();
  };
  const open = async () => {
    setIdentity("");
    setSession("");
    setFailure("");
    setIsLoading(true);
    dialog.current?.showModal();
    try {
      const record = object(await request("/api/cfo/resume"));
      const nextIdentity = string(record.identity);
      if (!nextIdentity || !string(record.session)) throw new Error("No recorded CFO conversation is available.");
      setIdentity(nextIdentity);
      setSession(string(record.session));
    } catch (error) {
      setFailure(message(error));
    } finally {
      setIsLoading(false);
      requestAnimationFrame(() => accept.current?.focus());
    }
  };
  const restart = async () => {
    if (!identity || isRestarting) return;
    setIsRestarting(true);
    setFailure("");
    try {
      await request("/api/cfo/restart", undefined, {
        method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance },
        body: JSON.stringify({ identity }),
      });
      close();
      if (trigger.current) onRestarted(trigger.current);
    } catch (error) {
      setFailure(message(error));
    } finally {
      setIsRestarting(false);
    }
  };
  return <>
    <button ref={trigger} type="button" className="icon-button" title="Restart CFO" aria-label="Restart CFO" onClick={() => void open()}><Icon name="refresh" /></button>
    <dialog ref={dialog} className="question-modal cfo-restart" aria-labelledby="cfo-restart-title" onCancel={(event) => { if (isRestarting) event.preventDefault(); }}>
      <form onSubmit={(event) => { event.preventDefault(); void restart(); }}>
        <div className="command-center-heading"><Icon name="refresh" /><h2 id="cfo-restart-title">Restart your CFO?</h2></div>
        <p>The current response will be interrupted. Your CFO resumes the same conversation in its terminal.</p>
        <p className="muted">Your goblins keep running. Their work stays in place.</p>
        {isLoading ? <p role="status">Reading the recorded conversation...</p> : session && <p className="restart-session">Conversation <code>{session}</code></p>}
        {failure && <p role="alert" className="warning-text">{failure}</p>}
        <div className="first-run-actions">
          <button type="button" disabled={isRestarting} onClick={close}>Cancel</button>
          {failure && <button type="button" disabled={isRestarting || isLoading} onClick={() => void open()}>Refresh conversation</button>}
          <button ref={accept} type="submit" className="primary" disabled={!identity || isLoading || isRestarting}><Icon name="refresh" />{isRestarting ? "Restarting CFO..." : "Restart CFO"}</button>
        </div>
        <p className="muted">Enter to restart (default). Esc to cancel.</p>
      </form>
    </dialog>
  </>;
}
