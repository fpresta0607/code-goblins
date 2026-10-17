import type { Ticket } from "./types";
import { Icon } from "./Icon";
import { safeGitHubLink, ticketLook, ticketWords } from "./people";
import "./people.css";

// A task's ticket: GitHub's issue mark in the color of where it stands and
// its number, opening the issue; its tip names it and its state. isIconOnly
// draws the mark alone.
export function TicketLink({ ticket, className, isIconOnly = false }: { ticket: Ticket; className: string; isIconOnly?: boolean }) {
  const url = safeGitHubLink(ticket.url);
  const look = ticketLook(ticket);
  if (!url) return null;
  return <a className={className + " ticket-link ticket-" + look} href={url} target="_blank" rel="noreferrer" aria-label={"Open " + ticketWords(ticket)} data-tip={ticketWords(ticket)}>
    <Icon name={look === "open" ? "issue" : "issue-closed"} />{!isIconOnly && <span>#{ticket.number}</span>}
  </a>;
}
