import { useState } from "react";
import { crossingLine } from "./crossing";
import { Notice } from "./notice";
import type { CrossingView } from "./types";

// Which move this browser dismissed, by when it was made.
const DISMISSED_KEY = "goblins-crossing-dismissed";

function dismissed(): string {
  try { return localStorage.getItem(DISMISSED_KEY) || ""; } catch { return ""; }
}

// The board's one notice after an update or an install moved the home to
// goblins' names: what changed, which terminals restart and when, and the way
// back, with Dismiss once nothing waits to restart.
export function CrossingBanner({ crossing }: { crossing?: CrossingView }) {
  const [hidden, setHidden] = useState(dismissed);
  const line = crossingLine(crossing);
  if (!line || !crossing || line.isDone && hidden === crossing.at) return null;
  const dismiss = () => {
    setHidden(crossing.at);
    try { localStorage.setItem(DISMISSED_KEY, crossing.at); } catch { /* the notice comes back on a reload */ }
  };
  return (
    <Notice form="box" actions={line.isDone && <button onClick={dismiss}>Dismiss</button>}>
      {line.text}{line.undo && <> <code>{line.undo}</code></>}
    </Notice>
  );
}
