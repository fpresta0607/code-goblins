import { ConnectorTag, type ConnectorTagTone } from "./connector-tag";

// The head of a scope's page: its name, its tag and what it is.
export function ConnectorTitle({ name, tone, tag, about }: { name: string; tone: ConnectorTagTone; tag: string; about: string }) {
  return <div className="connector-title">
    <h3>{name}</h3>
    <ConnectorTag tone={tone} text={tag} />
    <span className="connector-title-about">{about}</span>
  </div>;
}
