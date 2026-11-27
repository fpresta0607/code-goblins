import type { ConnectorScope } from "./connector-status";

export type ConnectorTagTone = ConnectorScope | "repo" | "private";

// A scope's tag (#3010), the same colour wherever it shows: on the tree, on a
// scope's page, on its rows and on Needs a look's cards.
export function ConnectorTag({ tone, text }: { tone: ConnectorTagTone; text: string }) {
  return <span className={"connector-tag is-" + tone}>{text}</span>;
}
