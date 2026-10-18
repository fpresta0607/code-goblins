import { useEffect, useState } from "react";
import { message, request } from "./api";
import { Icon } from "./Icon";
import { onThisMachine } from "./credentials";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { RestartPcDialog } from "./restart-pc-dialog";
import { isPastPoolLine, kernelPools } from "./start";
import type { Snapshot } from "./types";
import "./harness-update.css";

// Restart PC, before the AFK switch in the CFO panel's header (the Overlord,
// 2026-10-18: "the header shows the restart button in terminal cfo for safe
// pc restart"). It shows only on the board on this PC, never on his private
// board, as AFK mode's on, and only while the kernel's pools are past the
// line, or while the restart it began stops the fleet, when it says so and
// takes no press. His press asks once; the supervisor proves the board his
// and does the rest. What a press could not do, the supervisor's refusal or
// a restart that stopped short, says so in a few words beside it for a
// moment, as Update does, and the CFO is told why.
export function RestartPcButton({ snapshot }: { snapshot: Snapshot }) {
  const [isAsking, setAsking] = useState(false);
  const [isPending, setPending] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const memory = snapshot.memory;
  const restart = snapshot.pc_restart;
  const stoppedShort = restart?.state === "stopped" ? restart.at : "";
  useEffect(() => { if (stoppedShort) showFeedback("The PC did not restart, and the CFO knows why."); }, [stoppedShort, showFeedback]);
  if (!onThisMachine(window.location.hostname)) return null;
  if (restart?.state === "stopping" || restart?.state === "restarting") return <button className="harness-update pc-restart" disabled aria-busy="true"><span className="card-start-spinner" aria-hidden="true" />Restarting the PC…</button>;
  if (!memory || !isPastPoolLine(memory)) return <ClickFeedback text={feedback} />;
  const gigabytes = (bytes: number) => (bytes / 2 ** 30).toFixed(1);
  const press = async () => {
    setPending(true);
    showFeedback("");
    try {
      await request("/api/pc/restart", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: "{}" });
    } catch (error: unknown) {
      showFeedback(message(error));
    } finally {
      setPending(false);
      setAsking(false);
    }
  };
  return <>
    <button className="harness-update pc-restart" aria-label="Restart the PC" data-tip="The kernel's pools are past the line where free memory can no longer hold the fleet. Restart the PC safely to free them." onClick={() => { showFeedback(""); setAsking(true); }}><Icon name="refresh" />Restart PC</button>
    <ClickFeedback text={feedback} />
    {isAsking && <RestartPcDialog pools={gigabytes(kernelPools(memory))} line={gigabytes(memory.pool_line)} isPending={isPending} onRestart={() => void press()} onClose={() => setAsking(false)} />}
  </>;
}
