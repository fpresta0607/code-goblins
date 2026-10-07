import { useState } from "react";
import { message, request } from "./api";
import type { StartAtLoginView } from "./types";
import "./afk.css";

// Start at login in the CFO's panel: whether Windows starts Code Goblins in
// the tray when he signs in, which after a restart brings back the CFO and
// every goblin that was working. It is one setting with the desktop window's
// tray item and the setup's box, and the same in the window and the browser.
export function StartAtLoginSetting({ setting, instance }: { setting: StartAtLoginView; instance: string }) {
  const [pending, setPending] = useState(false);
  const [problem, setProblem] = useState("");
  const turn = async () => {
    setPending(true); setProblem("");
    try {
      await request("/api/start-at-login", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ on: !setting.on }) });
    } catch (error: unknown) { setProblem(message(error)); }
    setPending(false);
  };
  return <div className="start-at-login">
    <div>
      <strong id="start-at-login-name">Start at login</strong>
      <p>{setting.unavailable || "Code Goblins starts in the tray when you sign in to Windows. After a restart it brings back the CFO and every goblin that was working."}</p>
      {problem && <p className="afk-problem" role="alert">{problem}</p>}
    </div>
    <button className="afk-toggle" role="switch" aria-checked={setting.on} aria-labelledby="start-at-login-name" disabled={pending || !!setting.unavailable} onClick={() => void turn()}>
      <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
    </button>
  </div>;
}
