/* showcase-axi frame helper. Appended to the live preview response for an
   HTML artifact only - never written to the artifact file and never into an
   export. The frame is sandboxed without allow-same-origin, so the review
   page cannot read a selection made inside it; this forwards one.

   selectorFor and nearestSection mirror app.js. They cannot be shared: they
   run in a different document, in an opaque origin, with no access to the
   parent's script. */
(function () {
  "use strict";

  function selectorFor(node) {
    var parts = [];
    while (node && node.tagName && node !== document.documentElement) {
      if (node.id) {
        parts.unshift("#" + node.id);
        return parts.join(" > ");
      }
      var tag = node.tagName.toLowerCase();
      var parent = node.parentElement;
      if (parent) {
        var same = Array.prototype.filter.call(parent.children, function (c) {
          return c.tagName === node.tagName;
        });
        if (same.length > 1) tag += ":nth-of-type(" + (same.indexOf(node) + 1) + ")";
      }
      parts.unshift(tag);
      node = parent;
    }
    return parts.join(" > ");
  }

  function nearestSection(node) {
    while (node && node.tagName) {
      var sibling = node.previousElementSibling;
      while (sibling) {
        var heading = /^H[1-6]$/.test(sibling.tagName)
          ? sibling
          : sibling.querySelector && sibling.querySelector("h1, h2, h3, h4, h5, h6");
        if (heading) return heading.textContent.replace(/\s+/g, " ").trim().slice(0, 120);
        sibling = sibling.previousElementSibling;
      }
      var own = node.querySelector && node.querySelector(":scope > header");
      if (own) return own.textContent.replace(/\s+/g, " ").trim().slice(0, 120);
      node = node.parentElement;
    }
    return "";
  }

  function forward() {
    var sel = window.getSelection();
    var quote = sel && !sel.isCollapsed && sel.rangeCount ? sel.toString().replace(/\s+/g, " ").trim() : "";
    if (!quote) {
      // An empty quote tells the review page to drop its affordance.
      parent.postMessage({ __showcase: "selection", quote: "" }, "*");
      return;
    }
    var range = sel.getRangeAt(0);
    var rect = range.getBoundingClientRect();
    var node = range.commonAncestorContainer;
    if (node && node.nodeType === 3) node = node.parentElement;
    parent.postMessage({
      __showcase: "selection",
      quote: quote.slice(0, 500),
      selector: node ? selectorFor(node) : "",
      section: node ? nearestSection(node) : "",
      left: rect.left,
      bottom: rect.bottom
    }, "*");
  }

  function schedule() {
    setTimeout(forward, 0);
  }

  document.addEventListener("mouseup", schedule);
  document.addEventListener("keyup", function (event) {
    if (event.shiftKey || event.key === "Shift" || (event.ctrlKey || event.metaKey) && event.key === "a") schedule();
  });
  window.addEventListener("scroll", function () {
    // The forwarded rect is frame-viewport relative; once the frame scrolls
    // it no longer points at the selection, so retire the affordance.
    parent.postMessage({ __showcase: "selection", quote: "" }, "*");
  }, true);
})();
