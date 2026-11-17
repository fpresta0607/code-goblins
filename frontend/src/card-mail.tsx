import { useState } from "react";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { copyText } from "./clipboard";
import { CopyValue } from "./CopyValue";
import { Icon } from "./Icon";
import type { Mail } from "./types";
import { literalLinks } from "./messageText";
import "./card-mail.css";

const COPIED_MS = 1400;

// A message a card gives the Overlord to send himself, such as an email (the
// Overlord, 2026-10-17: "you should have a little like copy email body or
// copy email that I could then send. Instead of having to go to like this
// file"): where it goes, the address to copy or the page to open, its
// subject, and its body in a box that reads as the email will. A copy icon
// on the subject's line and one in the body's bottom corner each copy exactly
// that text (the Overlord, 2026-11-13: "it can just be the copy icon").
export function CardMail({ mail }: { mail: Mail }) {
  const [copied, setCopied] = useState("");
  const [feedback, showFeedback] = useClickFeedback();
  const copy = (what: string, text: string) => copyText(text).then(() => {
    showFeedback("");
    setCopied(what);
    setTimeout(() => setCopied(""), COPIED_MS);
  }, () => showFeedback("Not copied. Select the text and press Ctrl+C."));
  const copyButton = (what: "subject" | "body", text: string) => <button type="button" className={"icon-button card-mail-copy" + (copied === what ? " copied" : "")}
    aria-label={"Copy " + what} onClick={() => void copy(what, text)}><Icon name={copied === what ? "check" : "copy"} /></button>;
  return <section className="card-mail" aria-label="Message to send">
    {mail.to && <p className="card-mail-line"><span className="card-mail-label">To</span>
      {mail.to.startsWith("https://") ? <a href={mail.to} target="_blank" rel="noreferrer"><span>{mail.to}</span><Icon name="external" /></a> : <CopyValue value={mail.to} />}</p>}
    {mail.subject && <p className="card-mail-line"><span className="card-mail-label">Subject</span><span className="card-mail-subject">{literalLinks(mail.subject)}</span>{copyButton("subject", mail.subject)}</p>}
    <div className="card-mail-box">
      <pre className="card-mail-body" tabIndex={0} aria-label="Message body">{literalLinks(mail.body)}</pre>
      {copyButton("body", mail.body)}
    </div>
    <p className="sr-only" role="status">{copied && (copied === "subject" ? "Subject" : "Body") + " copied"}</p>
    <ClickFeedback text={feedback} />
  </section>;
}
