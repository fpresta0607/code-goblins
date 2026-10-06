import { useState } from "react";
import { comebackLine } from "./comeback";
import type { Comeback } from "./types";

// Which restart's line this browser dismissed, by the sign-in it began.
const DISMISSED_KEY = "cfo-comeback-dismissed";

function dismissed(): string {
  try { return localStorage.getItem(DISMISSED_KEY) || ""; } catch { return ""; }
}

// The board's one quiet line after a restart or sign-out: what the
// supervisor is bringing back while it works, then what resumed, with
// Dismiss once nothing waits. It is the same line in the desktop window and
// in the browser.
export function ComebackBanner({ comeback }: { comeback?: Comeback }) {
  const [hidden, setHidden] = useState(dismissed);
  const line = comebackLine(comeback);
  if (!line || !comeback || line.isDone && hidden === comeback.signed_in) return null;
  const dismiss = () => {
    setHidden(comeback.signed_in);
    try { localStorage.setItem(DISMISSED_KEY, comeback.signed_in); } catch { /* the line comes back on a reload */ }
  };
  return <div className={"update-banner comeback-banner" + (line.isDone ? "" : " working")} role="status">
    <span>{line.text}{line.detail && <> <span className="comeback-detail">{line.detail}</span></>}</span>
    {line.isDone && <button onClick={dismiss}>Dismiss</button>}
  </div>;
}
