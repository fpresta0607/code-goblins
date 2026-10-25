import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Disclosure } from "../../src/Disclosure";
import "../../src/styles.css";

function DisclosureExample() {
  const [isOpen, setIsOpen] = useState(false);
  return <main>
    <Disclosure id="preview" title="Preview"><input aria-label="Preview edit" defaultValue="Original" /></Disclosure>
    <Disclosure id="retained" title="Retained" keepMounted><input aria-label="Retained edit" defaultValue="Original" /></Disclosure>
    <Disclosure id="controlled" title="Controlled" kind="disclosure-line" open={isOpen} onOpenChange={setIsOpen}><p>Controlled content</p></Disclosure>
    <button onClick={() => setIsOpen(!isOpen)}>Toggle controlled</button>
    <output aria-label="Controlled state">{isOpen ? "Open" : "Closed"}</output>
    <Disclosure id="initially-open" title="Initially open" defaultOpen><p>Initial content</p></Disclosure>
  </main>;
}

createRoot(document.getElementById("root")!).render(<DisclosureExample />);
