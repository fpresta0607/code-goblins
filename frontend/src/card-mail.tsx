import { useState } from "react";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { copyText } from "./clipboard";
import { CopyValue } from "./CopyValue";
import { Icon } from "./Icon";
import type { Mail } from "./types";
import "./card-mail.css";

const COPIED_MS = 1400;

// A message a card gives the Overlord to send himself, such as an email (the
// Overlord, 2026-10-17: "you should have a little like copy email body or
// copy email that I could then send. Instead of having to go to like this
// file"): where it goes, the address to copy or the page to open, its
// subject, and its body in a box that reads as the email will, with Copy
// email, and Copy subject when it has one, each copying exactly that text.
export function CardMail({ mail }: { mail: Mail }) {
  const [copied, setCopied] = useState("");
  const [feedback, showFeedback] = useClickFeedback();
  const copy = (what: string, text: string) => copyText(text).then(() => {
    showFeedback("");
    setCopied(what);
    setTimeout(() => setCopied(""), COPIED_MS);
  }, () => showFeedback("Not copied. Select the text and press Ctrl+C."));
  return <section className="card-mail" aria-label="Message to send">
    {mail.to && <p className="card-mail-line"><span className="card-mail-label">To</span>
      {mail.to.startsWith("https://") ? <a href={mail.to} target="_blank" rel="noreferrer"><span>{mail.to}</span><Icon name="external" /></a> : <CopyValue value={mail.to} />}</p>}
    {mail.subject && <p className="card-mail-line"><span className="card-mail-label">Subject</span><span>{mail.subject}</span></p>}
    <pre className="card-mail-body" tabIndex={0} aria-label="Message body">{mail.body}</pre>
    <div className="card-mail-actions">
      <ClickFeedback text={feedback} />
      {mail.subject && <button type="button" className="icon-button raised pill-link" onClick={() => void copy("subject", mail.subject)}><Icon name={copied === "subject" ? "check" : "copy"} /><span>{copied === "subject" ? "Copied" : "Copy subject"}</span></button>}
      <button type="button" className="icon-button raised pill-link" onClick={() => void copy("email", mail.body)}><Icon name={copied === "email" ? "check" : "copy"} /><span>{copied === "email" ? "Copied" : "Copy email"}</span></button>
    </div>
  </section>;
}
