// xterm's supported document override lets its generated styles carry this
// page's CSP nonce before insertion, without altering global DOM operations.
export function terminalDocument(nonce: string): Document {
  return new Proxy(document, {
    get(target, property) {
      if (property === "createElement") return <K extends keyof HTMLElementTagNameMap>(name: K, options?: ElementCreationOptions) => {
        const element = target.createElement(name, options);
        if (element instanceof HTMLStyleElement) element.nonce = nonce;
        // xterm 6's viewport creates its scrollbar style through the main
        // document. Apply the same nonce at this terminal-owned boundary.
        const append: <T extends Node>(child: T) => T = element.appendChild.bind(element);
        element.appendChild = <T extends Node>(child: T): T => {
          if (child instanceof HTMLStyleElement) child.nonce = nonce;
          return append(child);
        };
        return element;
      };
      const value: unknown = Reflect.get(target, property, target);
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
}
