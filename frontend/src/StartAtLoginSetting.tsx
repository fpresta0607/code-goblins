import { useState } from "react";
import { message, request } from "./api";
import type { StartAtLoginView } from "./types";
import "./afk.css";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// Start at login in the CFO's panel: whether Windows starts Code Goblins in
// the tray when he signs in, which after a restart brings back the CFO and
// every goblin that was working. It is one setting with the desktop window's
// tray item and the setup's box, and the same in the window and the browser.
// What it does, or why it cannot be set, is in its tip, and a refusal says so
// in a few words beside it for a moment.
export function StartAtLoginSetting({ setting, instance }: { setting: StartAtLoginView; instance: string }) {
  const [pending, setPending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const turn = async () => {
    setPending(true);
    showFeedback("");
    try {
      await request("/api/start-at-login", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ on: !setting.on }) });
    } catch (error: unknown) { showFeedback(message(error)); }
    setPending(false);
  };
  return <div className="start-at-login">
    <strong id="start-at-login-name">Start at login</strong>
    <button className="afk-toggle" role="switch" aria-checked={setting.on} aria-labelledby="start-at-login-name" data-tip={setting.unavailable || "Starts Code Goblins in the tray at sign-in and brings back the CFO and its goblins after a restart."} disabled={pending || !!setting.unavailable} onClick={() => void turn()}>
      <span className="afk-toggle-track" aria-hidden="true"><span className="afk-toggle-thumb" /></span>
    </button>
    <ClickFeedback text={feedback} />
  </div>;
}
