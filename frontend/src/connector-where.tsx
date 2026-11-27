import type { ReactNode } from "react";
import { ConnectorRow } from "./connector-row";
import type { Place } from "./connector-place";
import type { ConnectorRow as Row } from "./connector-rows";
import { ConnectorTag, type ConnectorTagTone } from "./connector-tag";
import { Disclosure } from "./Disclosure";
import type { ConnectorActions } from "./use-connectors";

// One place a scope's connectors come from (#3010): where it is, how many it
// holds, who shares it, its rows, and the rows past the first few folded.
export function ConnectorWhere({ tone, title, file = "", count, why, rows = [], empty = "", fold, now, actions, goblinName = "", onOpen, children }: {
  tone: ConnectorTagTone; title: string; file?: string; count: string; why: string; rows?: Row[]; empty?: string; fold?: { title: string; rows: Row[] };
  now: number; actions: ConnectorActions; goblinName?: string; onOpen?: (place: Place) => void; children?: ReactNode;
}) {
  const list = (each: Row[]) => <ul className="connector-rows">
    {each.map((row) => <ConnectorRow key={row.key} row={row} now={now} goblinName={goblinName} actions={actions} onOpen={onOpen} />)}
  </ul>;
  return <section className="connector-where" aria-label={title}>
    <div className="connector-where-head">
      <h4><ConnectorTag tone={tone} text={title} />{file && <code>{file}</code>}</h4>
      <span className="connector-chip">{count}</span>
      <p className="connector-why">{why}</p>
    </div>
    {rows.length > 0 ? list(rows) : empty && <p className="connector-empty">{empty}</p>}
    {fold && fold.rows.length > 0 && <Disclosure kind="connector-fold" title={fold.title}>{list(fold.rows)}</Disclosure>}
    {children}
  </section>;
}
