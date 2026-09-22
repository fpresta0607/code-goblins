import type { Snapshot } from "./types";
import { parseCFO } from "./types";
import { useNativeOutput } from "./api";
import { MessageComposer, type MessageControls } from "./Messages";
import { Avatar } from "./Avatar";
import { age } from "./presentation";

export function CFOPane({ snapshot, messages, connected, visible }: {
  snapshot: Snapshot | null; messages: MessageControls; connected: boolean; visible: boolean;
}) {
  const output = useNativeOutput(visible && connected ? "/api/cfo" : null, parseCFO);
  const sent = snapshot?.actions.filter((action) => action.kind === "cfo_message").slice(-12) || [];
  return <section className="cfo-pane" aria-label="CFO conversation">
    <header className="conversation-header"><Avatar persona="cfo" small /><div><h2>CFO</h2><p>Your supervisor{output.data?.harness && " · " + output.data.harness}</p></div></header>
    <div className="native-conversation">
      {sent.map((action) => <article className="submitted-message" key={action.id}><p className="muted">You · {age(action.updated_at)}</p><p>{action.text}</p><small>{action.status}: {action.message || "Queued"}</small></article>)}
      <div className="native-output-heading"><h3>Native session</h3><button disabled={output.loading || !connected} onClick={output.reload}>Refresh</button></div>
      {output.loading && !output.data && <p className="loading" role="status">Connecting to the registered CFO…</p>}
      {(output.error || (!output.data?.available && output.data?.reason)) && <p className="error-box" role="status">{output.error || output.data?.reason}</p>}
      {output.data?.text && <pre className="native-session-output" tabIndex={0}>{output.data.text}</pre>}
      {!output.data?.text && !output.loading && !output.error && <p className="muted">The registered CFO's actual session output appears here.</p>}
      <p className="transport-note">Captured native output · submitted messages</p>
    </div>
    <MessageComposer messages={messages} channel="cfo" recipient={{ kind: "cfo_message", generation: output.data?.identity || "" }} connected={connected} disabled={!output.data?.available || !!output.error} />
  </section>;
}
