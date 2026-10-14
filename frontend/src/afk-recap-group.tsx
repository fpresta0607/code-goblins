import type { ReactNode } from "react";
import { Disclosure } from "./Disclosure";
import "./afk.css";

// One heading of the AFK recap with its count and its lines, as one card, behind
// the board's one thin caret. It starts closed, showing only its name and its
// count, and opens at a tap.
export function AfkRecapGroup({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  return <section className="afk-group" aria-label={title}>
    <Disclosure kind="afk-drawer" title={<>{title} <span className="column-count">{count}</span></>}><ul className="afk-rows">{children}</ul></Disclosure>
  </section>;
}
