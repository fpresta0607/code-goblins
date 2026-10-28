import type { ReactNode } from "react";
import { Icon, type IconName } from "./Icon";
import "./notice.css";

export type NoticeTone = "info" | "warning" | "error";

// The board's one look for what it says about trouble or state: an error, a
// refusal, a usage line, a warning, a connection or a terminal's state, each
// drawn as the message board draws a message (the Overlord, 2026-10-27: "Make
// sure they all have this clean, consistent styling with the message board").
// Its words stay in the board's text colour and its tone shows only in its
// mark and its edge, never as coloured words. A line is one quiet line, a box
// a block; actions sit on its right. Nothing in it moves. It is a status
// unless the board allows the alert (error-surfaces.test.ts). className
// places it in its parent; the look is the notice's own.
export function Notice({ tone = "info", form = "line", icon, role = "status", tip, className, actions, children }: {
  tone?: NoticeTone; form?: "line" | "box"; icon?: IconName; role?: "status" | "alert"; tip?: string; className?: string; actions?: ReactNode; children?: ReactNode;
}) {
  const Frame = form === "line" ? "span" : "div";
  return <Frame className={`notice ${form} ${tone}` + (className ? " " + className : "")} role={role} data-tip={tip}>
    {icon ? <Icon name={icon} /> : <span className="notice-dot" aria-hidden="true" />}
    {children !== undefined && children !== "" && <Frame className="notice-words">{children}</Frame>}
    {actions && <span className="notice-actions">{actions}</span>}
  </Frame>;
}
