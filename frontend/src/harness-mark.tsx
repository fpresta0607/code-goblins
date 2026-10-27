import { memo } from "react";
import { ConnectorMark } from "./ConnectorMark";
import { harnessMark, hasHarnessMark } from "./connectors";
import { harnessName } from "./workflow";

// The mark of the harness a session runs, Claude Code's, Codex's or Pi's,
// as the board draws it on its cards, with label in its tip; a harness with
// no mark of its own shows its name instead, never another harness's mark
// (the Overlord, 2026-10-27: "make sure that whatever harness is being used
// inside the modern chat, that it is using the right icon").
// It is drawn again only when its harness or label changes, so a snapshot that
// changes nothing draws none.
export const HarnessMark = memo(function HarnessMark({ harness, label = harnessName(harness) }: { harness: string; label?: string }) {
  if (!hasHarnessMark(harness)) return <span className="harness-name" data-tip={label}>{harnessName(harness)}</span>;
  return <ConnectorMark mark={harnessMark(harness)} label={label} />;
});
