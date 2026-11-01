import { useMemo, type ReactNode } from "react";
import { DocumentContext } from "./document-context";

export function MessageDocumentScope({ query, instance, children }: { query: string; instance: string; children: ReactNode }) {
  const scope = useMemo(() => ({ query, instance }), [query, instance]);
  return <DocumentContext value={scope}>{children}</DocumentContext>;
}
