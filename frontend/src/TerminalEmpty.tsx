import { Icon } from "./Icon";

// A terminal slot with nothing to show: it says why, and for a child session
// offers the task that owns it. It belongs to no terminal backend.
export function TerminalEmpty({ text, onOwner }: { text: string; onOwner?: () => void }) {
  return <div className="terminal-empty"><Icon name="terminal" /><p>{text}</p>{onOwner && <button className="primary" onClick={onOwner}>Open owning task</button>}</div>;
}
